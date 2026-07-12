// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package statedump explodes serialized save states into per-field
// binaries for offline inspection.
package statedump

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/s2"
)

// Save state container layout (mirrors core/savestate.go):
//
//	header: magic[8] "ERINGSST" | version u32 | gameIDLen u8 | gameID |
//	        biosHash[32] | dataCRC u32 | segmented S2-compressed body
//	chunk:  tag[16, zero-filled ASCII] | length u32 | payload[length]
//	field:  nameLen u8 | name | size u32 | data[size]
const (
	stateMagic  = "ERINGSST"
	stateTagLen = 16
	biosHashLen = 32
)

// Explode writes a state's decoded header to header.txt in dir and one
// <field>.bin per chunk field under a subdirectory named after the
// chunk tag, returning the chunk and field counts. dir must exist.
func Explode(state []byte, dir string) (chunks, fields int, err error) {
	// Header.
	pos := 0
	if len(state) < len(stateMagic)+4+1 {
		return 0, 0, fmt.Errorf("state too short (%d bytes)", len(state))
	}
	if string(state[:len(stateMagic)]) != stateMagic {
		return 0, 0, fmt.Errorf("bad magic %q", state[:len(stateMagic)])
	}
	pos += len(stateMagic)
	version := binary.BigEndian.Uint32(state[pos:])
	pos += 4
	idLen := int(state[pos])
	pos++
	if pos+idLen+biosHashLen+4 > len(state) {
		return 0, 0, fmt.Errorf("header truncated")
	}
	gameID := string(state[pos : pos+idLen])
	pos += idLen
	biosHash := state[pos : pos+biosHashLen]
	pos += biosHashLen
	dataCRC := binary.BigEndian.Uint32(state[pos:])
	pos += 4

	biosDesc := hex.EncodeToString(biosHash)
	if bytes.Equal(biosHash, make([]byte, biosHashLen)) {
		biosDesc = "all-zero (HLE BIOS)"
	}
	hdr := fmt.Sprintf("version: %d\ngameID: %q\nbiosHash: %s\ndataCRC: %08x\nfileSize: %d\n",
		version, gameID, biosDesc, dataCRC, len(state))
	if err := os.WriteFile(filepath.Join(dir, "header.txt"), []byte(hdr), 0o644); err != nil {
		return 0, 0, err
	}

	// Body: decompress the segment table (u32 count, then per segment
	// u32 uncompressed length, u32 compressed length, S2 block) and
	// explode every chunk field to its own file.
	payload := state[pos:]
	if len(payload) < 4 {
		return 0, 0, fmt.Errorf("segment table truncated")
	}
	segCount := int(binary.BigEndian.Uint32(payload))
	sp := 4
	var body []byte
	for i := 0; i < segCount; i++ {
		if sp+8 > len(payload) {
			return 0, 0, fmt.Errorf("segment %d header truncated", i)
		}
		rawLen := int(binary.BigEndian.Uint32(payload[sp:]))
		compLen := int(binary.BigEndian.Uint32(payload[sp+4:]))
		sp += 8
		if compLen > len(payload)-sp {
			return 0, 0, fmt.Errorf("segment %d overruns payload", i)
		}
		seg, err := s2.Decode(nil, payload[sp:sp+compLen])
		if err != nil {
			return 0, 0, fmt.Errorf("segment %d decompress: %w", i, err)
		}
		if len(seg) != rawLen {
			return 0, 0, fmt.Errorf("segment %d decompressed to %d bytes, declared %d", i, len(seg), rawLen)
		}
		body = append(body, seg...)
		sp += compLen
	}
	if sp != len(payload) {
		return 0, 0, fmt.Errorf("%d trailing bytes after segments", len(payload)-sp)
	}

	bp := 0
	for bp < len(body) {
		if bp+stateTagLen+4 > len(body) {
			return 0, 0, fmt.Errorf("chunk header truncated at offset %d", bp)
		}
		rawTag := body[bp : bp+stateTagLen]
		nameEnd := bytes.IndexByte(rawTag, 0)
		if nameEnd <= 0 {
			return 0, 0, fmt.Errorf("malformed chunk tag at offset %d", bp)
		}
		tag := string(rawTag[:nameEnd])
		bp += stateTagLen
		clen := int(binary.BigEndian.Uint32(body[bp:]))
		bp += 4
		if bp+clen > len(body) {
			return 0, 0, fmt.Errorf("chunk %s length %d overruns body", tag, clen)
		}

		chunkDir := filepath.Join(dir, tag)
		if err := os.MkdirAll(chunkDir, 0o755); err != nil {
			return 0, 0, err
		}
		payload := body[bp : bp+clen]
		fp := 0
		for fp < len(payload) {
			nlen := int(payload[fp])
			fp++
			if nlen == 0 || fp+nlen+4 > len(payload) {
				return 0, 0, fmt.Errorf("chunk %s: field header invalid at offset %d", tag, fp-1)
			}
			name := string(payload[fp : fp+nlen])
			fp += nlen
			fsize := int(binary.BigEndian.Uint32(payload[fp:]))
			fp += 4
			if fp+fsize > len(payload) {
				return 0, 0, fmt.Errorf("chunk %s field %s size %d overruns chunk", tag, name, fsize)
			}
			file := filepath.Join(chunkDir, name+".bin")
			if err := os.WriteFile(file, payload[fp:fp+fsize], 0o644); err != nil {
				return 0, 0, err
			}
			fp += fsize
			fields++
		}
		bp += clen
		chunks++
	}

	return chunks, fields, nil
}
