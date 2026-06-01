// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command extract_bioslibs extracts and decompresses the compressed bodies
// stored inside the US Sega Saturn BIOS:
//
//	bios_fonts.bin   - bitmap font / glyph bitmaps, ROM $005240
//	bootlib.bin      - boot library: Saturn logo / disc-check animation, ROM $007000
//	bios_cdfs.bin    - BIOS-internal CD filesystem driver (SH-2 host-side, runs
//	                   in WRAM-H on the CD-game boot path; NOT the SH-1 CD-block
//	                   controller firmware), ROM $01D000
//	app_videocd.bin  - SEGA PLAYER app: Video-CD/MPEG + disc security (dir 0),  ROM $040448
//	app_cdg.bin      - SEGA PLAYER app: CD+G player (dir 1),                    ROM $04B134
//	app_graphics.bin - SEGA PLAYER shared graphics resources (dir 13),          ROM $058F64
//	app_data.bin     - SEGA PLAYER shared data resources (dir 14),              ROM $062CC0
//	app_playerui.bin - SEGA PLAYER player UI / common code (dir 9),             ROM $068478
//	app_settings.bin - SEGA PLAYER app: System Settings / Memory Manager (dir 8), ROM $0748A0
//	per_driver.bin   - PER + BUP peripheral driver, ROM $07D660
//
// The app_* bodies belong to the SEGA PLAYER multimedia shell; see
// docs/bios/system_applications.md for how the BIOS loads them. All bodies use
// the BIOS sub_1F04 LZSS format. The input must be the US BIOS, validated by
// size and SHA-256. Output files are written to the current directory or the
// directory given by -out.
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// usBIOSSize is the exact byte length of the Saturn BIOS image.
	usBIOSSize = 524288
	// usBIOSSHA256 identifies the US BIOS (BTR_1.000U941115).
	usBIOSSHA256 = "96e106f740ab448cf89f0dd49dfbac7fe5391cb6bd6e14ad5e3061c13330266f"
)

// libSpec describes one compressed body to extract from the BIOS.
type libSpec struct {
	name   string // output filename
	offset int    // ROM offset of the sub_1F04 header
	maxOut int    // R6 output cap the BIOS passes to sub_1F04
}

// libs lists the compressed bodies to extract, in ROM order. offset is the
// sub_1F04 header (for the app_* bodies this is the MB record's body, +0x10
// past the "MB" record header). maxOut is a guard cap; every body reaches its
// natural stream end under it, so the cap only bounds a malformed stream.
var libs = []libSpec{
	{"bios_fonts.bin", 0x005240, 0x00001000},
	{"bootlib.bin", 0x007000, 0x00040000},
	{"bios_cdfs.bin", 0x01D000, 0x00040000},
	{"app_videocd.bin", 0x040448, 0x00040000},
	{"app_cdg.bin", 0x04B134, 0x00040000},
	{"app_graphics.bin", 0x058F64, 0x00040000},
	{"app_data.bin", 0x062CC0, 0x00040000},
	{"app_playerui.bin", 0x068478, 0x00040000},
	{"app_settings.bin", 0x0748A0, 0x00040000},
	{"per_driver.bin", 0x07D660, 0x00004000},
}

// validateUSBIOS confirms data is the US BIOS by size and SHA-256.
func validateUSBIOS(data []byte) error {
	if len(data) != usBIOSSize {
		return fmt.Errorf("not a Saturn BIOS: size %d, want %d", len(data), usBIOSSize)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != usBIOSSHA256 {
		return fmt.Errorf("not the US BIOS: sha256 %s, want %s", got, usBIOSSHA256)
	}
	return nil
}

// decompress expands a sub_1F04 LZSS body. src begins at the 2-byte header;
// trailing bytes past the body are ignored. Output is capped at maxOut bytes:
// per-token byte counts are clamped against the remaining room exactly as the
// BIOS clamps against R6, so a stream that would emit more than maxOut bytes is
// truncated to maxOut rather than overrunning. All src reads are bounds-checked;
// a stream that ends prematurely returns an error instead of panicking.
func decompress(src []byte, maxOut int) ([]byte, error) {
	c := cursor{buf: src}

	header, err := c.readWord()
	if err != nil {
		return nil, err
	}
	if header&1 == 0 {
		return nil, errors.New("raw transfer not supported")
	}

	blockCount, err := c.readWord()
	if err != nil {
		return nil, err
	}
	if blockCount == 0 {
		return nil, errors.New("block_count is zero")
	}

	out := make([]byte, 0, maxOut)

	// back returns the output byte at index i, or 0 for indices before the
	// start of output (modeling reads into zero-initialized destination RAM).
	back := func(i int) byte {
		if i >= 0 {
			return out[i]
		}
		return 0
	}

	// runBlock processes one block of tokenCount tokens.
	runBlock := func(tokenCount int) error {
		selector, err := c.readWord()
		if err != nil {
			return err
		}
		for bit := 0; bit < tokenCount; bit++ {
			token, err := c.readWord()
			if err != nil {
				return err
			}
			remaining := maxOut - len(out)

			if selector&(1<<uint(bit)) != 0 {
				// Literal: high byte then low byte, clamped to remaining.
				if remaining >= 1 {
					out = append(out, byte(token>>8))
				}
				if remaining >= 2 {
					out = append(out, byte(token))
				}
				if remaining < 2 {
					return nil
				}
				continue
			}

			// Back-reference: top nibble selects the type.
			var length, offset int
			switch token & 0xF000 {
			case 0x0000:
				length = int(token&0x0FFF) + 3
				offset = 1
			case 0x1000:
				extra, err := c.readByte()
				if err != nil {
					return err
				}
				length = int(extra) + 17
				offset = int(token & 0x0FFF)
			default:
				length = int(token>>12) + 1
				offset = int(token & 0x0FFF)
			}
			if length > remaining {
				length = remaining
			}
			for i := 0; i < length; i++ {
				out = append(out, back(len(out)-offset))
			}
		}
		return nil
	}

	for i := 0; i < int(blockCount); i++ {
		if err := runBlock(16); err != nil {
			return nil, err
		}
	}

	trunc, err := c.readByte()
	if err != nil {
		return nil, err
	}
	tail, err := c.readByte()
	if err != nil {
		return nil, err
	}
	if int8(tail) >= 1 {
		if err := runBlock(int(tail)); err != nil {
			return nil, err
		}
	}
	if n := int(trunc); n > 0 {
		if n > len(out) {
			n = len(out)
		}
		out = out[:len(out)-n]
	}

	return out, nil
}

// cursor reads big-endian values from a byte slice with bounds checking.
type cursor struct {
	buf []byte
	pos int
}

func (c *cursor) readWord() (uint16, error) {
	if c.pos+2 > len(c.buf) {
		return 0, errors.New("unexpected end of compressed stream")
	}
	v := binary.BigEndian.Uint16(c.buf[c.pos:])
	c.pos += 2
	return v, nil
}

func (c *cursor) readByte() (byte, error) {
	if c.pos+1 > len(c.buf) {
		return 0, errors.New("unexpected end of compressed stream")
	}
	v := c.buf[c.pos]
	c.pos++
	return v, nil
}

func main() {
	outDir := flag.String("out", ".", "output directory for the extracted .bin files")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: extract_bioslibs [-out DIR] <bios.bin>\n")
		os.Exit(1)
	}
	biosPath := flag.Arg(0)

	data, err := os.ReadFile(biosPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading BIOS: %v\n", err)
		os.Exit(1)
	}
	if err := validateUSBIOS(data); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating output directory: %v\n", err)
		os.Exit(1)
	}

	for _, lib := range libs {
		body, err := decompress(data[lib.offset:], lib.maxOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error decompressing %s: %v\n", lib.name, err)
			os.Exit(1)
		}
		path := filepath.Join(*outDir, lib.name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("%s: %d bytes\n", path, len(body))
	}
}
