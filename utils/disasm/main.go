// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/user-none/erings/core/sh2"
)

func parseHexAddr(s string) (uint32, error) {
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")

	var addr uint64
	_, err := fmt.Sscanf(s, "%x", &addr)
	if err != nil {
		return 0, fmt.Errorf("invalid hex address: %s", s)
	}
	if addr > 0xFFFFFFFF {
		return 0, fmt.Errorf("address out of 32-bit range: %s", s)
	}
	return uint32(addr), nil
}

// dataEntry describes a constant pool reference found during the first pass.
type dataEntry struct {
	size uint8 // 2 = .data.w (16-bit), 4 = .data.l (32-bit)
}

// collectData scans the instruction range and records addresses referenced by
// PC-relative MOV.W and MOV.L instructions as constant pool data.
func collectData(data []byte, baseAddr uint32, startAddr uint32, fileSize uint32, count int) map[uint32]dataEntry {
	pool := make(map[uint32]dataEntry)
	addr := startAddr
	for i := 0; i < count; i++ {
		offset := addr - baseAddr
		if offset+2 > fileSize {
			break
		}
		op := binary.BigEndian.Uint16(data[offset : offset+2])
		switch op >> 12 {
		case 0x9:
			// MOV.W @(disp,PC),Rn - 16-bit constant
			target := addr + 4 + uint32(op&0xFF)*2
			if target >= baseAddr && target-baseAddr+2 <= fileSize {
				pool[target] = dataEntry{size: 2}
			}
		case 0xD:
			// MOV.L @(disp,PC),Rn - 32-bit constant (aligned)
			target := (addr & 0xFFFFFFFC) + 4 + uint32(op&0xFF)*4
			if target >= baseAddr && target-baseAddr+4 <= fileSize {
				pool[target] = dataEntry{size: 4}
				// Second half of the 32-bit value
				pool[target+2] = dataEntry{size: 0}
			}
		}
		addr += 2
	}
	return pool
}

// regLoad records the 32-bit value most recently loaded into a register by a
// PC-relative long load (MOV.L @(disp,PC),Rn), used to resolve register-
// indirect branch targets.
type regLoad struct {
	val   uint32
	valid bool
}

// disassembleLines renders n instructions starting at startAddr (mapped at
// baseAddr) into formatted output lines. Constant-pool words are shown as
// .data; register-indirect branch targets (BSRF/BRAF/JSR/JMP) are resolved
// against the most recent PC-relative load of that register when known, and
// delay-slot instructions are marked. The caller is responsible for validating
// that the [startAddr, startAddr+n*2) range fits within the file.
func disassembleLines(data []byte, baseAddr, startAddr uint32, n int) []string {
	fileSize := uint32(len(data))
	pool := collectData(data, baseAddr, startAddr, fileSize, n)

	var lastLoad [16]regLoad
	prevDelayed := false

	lines := make([]string, 0, n)
	addr := startAddr
	for i := 0; i < n; i++ {
		offset := addr - baseAddr
		if offset+2 > fileSize {
			break
		}
		op := binary.BigEndian.Uint16(data[offset : offset+2])

		if entry, ok := pool[addr]; ok {
			switch {
			case entry.size == 4 && offset+4 <= fileSize:
				val := binary.BigEndian.Uint32(data[offset : offset+4])
				lines = append(lines, fmt.Sprintf("$%06X: %02X %02X  .data.l H'%08X", addr, op>>8, op&0xFF, val))
			case entry.size == 2:
				lines = append(lines, fmt.Sprintf("$%06X: %02X %02X  .data.w H'%04X", addr, op>>8, op&0xFF, op))
			default:
				// Second half of a .data.l, or a 32-bit word that would overrun
				// the file: show the raw bytes only.
				lines = append(lines, fmt.Sprintf("$%06X: %02X %02X", addr, op>>8, op&0xFF))
			}
			prevDelayed = false
			addr += 2
			continue
		}

		text := sh2.Disassemble(addr, op)
		rn := uint8((op >> 8) & 0xF)
		switch {
		case op&0xF0FF == 0x0003, op&0xF0FF == 0x0023:
			// BSRF/BRAF Rn: register holds a PC-relative displacement.
			if lastLoad[rn].valid {
				text += fmt.Sprintf("   ; -> $%06X", addr+4+lastLoad[rn].val)
				lastLoad[rn].valid = false
			}
		case op&0xF0FF == 0x400B, op&0xF0FF == 0x402B:
			// JSR/JMP @Rn: register holds an absolute target address.
			if lastLoad[rn].valid {
				text += fmt.Sprintf("   ; -> $%06X", lastLoad[rn].val)
				lastLoad[rn].valid = false
			}
		case op&0xF000 == 0xD000:
			// MOV.L @(disp,PC),Rn - record the loaded pool value for Rn.
			ptr := (addr &^ 3) + 4 + uint32(op&0xFF)*4
			if ptr >= baseAddr && ptr-baseAddr+4 <= fileSize {
				lastLoad[rn] = regLoad{binary.BigEndian.Uint32(data[ptr-baseAddr : ptr-baseAddr+4]), true}
			}
		}

		if prevDelayed {
			text += "   ; delay slot"
		}
		lines = append(lines, fmt.Sprintf("$%06X: %02X %02X  %s", addr, op>>8, op&0xFF, text))
		prevDelayed = sh2.IsDelayedBranch(op)
		addr += 2
	}
	return lines
}

func main() {
	filePath := flag.String("file", "", "path to SH1/SH2 binary file")
	baseStr := flag.String("base", "0", "hex base address the file is mapped at")
	addrStr := flag.String("addr", "", "hex start address ($, 0x prefix or bare hex)")
	count := flag.Int("count", 20, "number of instructions to disassemble")
	all := flag.Bool("all", false, "disassemble from addr to end of file")
	flag.Parse()

	if *filePath == "" {
		fmt.Fprintf(os.Stderr, "error: -file is required\n")
		os.Exit(1)
	}
	if !*all && *addrStr == "" {
		fmt.Fprintf(os.Stderr, "error: -addr is required (or use -all)\n")
		os.Exit(1)
	}

	data, err := os.ReadFile(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
		os.Exit(1)
	}
	fileSize := uint32(len(data))
	if fileSize == 0 {
		fmt.Fprintf(os.Stderr, "error: file is empty\n")
		os.Exit(1)
	}
	if fileSize%2 != 0 {
		fmt.Fprintf(os.Stderr, "error: file size must be even (SH2 instructions are 16-bit), got %d bytes\n", fileSize)
		os.Exit(1)
	}

	baseAddr, err := parseHexAddr(*baseStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid -base: %v\n", err)
		os.Exit(1)
	}

	var addr uint32
	if *addrStr != "" {
		addr, err = parseHexAddr(*addrStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		addr = baseAddr
	}

	// Strip cache-through mirror bit (bit 29).
	addr &^= 0x20000000

	if addr < baseAddr || addr-baseAddr >= fileSize {
		fmt.Fprintf(os.Stderr, "error: address 0x%08X is outside the file range (0x%08X-0x%08X)\n",
			addr, baseAddr, baseAddr+fileSize-1)
		os.Exit(1)
	}

	n := *count
	if *all {
		n = int(fileSize-(addr-baseAddr)) / 2
	} else if n <= 0 {
		fmt.Fprintf(os.Stderr, "error: -count must be positive\n")
		os.Exit(1)
	} else if addr-baseAddr+uint32(n)*2 > fileSize {
		fmt.Fprintf(os.Stderr, "error: address range exceeds file size (0x%08X-0x%08X)\n",
			baseAddr, baseAddr+fileSize-1)
		os.Exit(1)
	}

	for _, line := range disassembleLines(data, baseAddr, addr, n) {
		fmt.Println(line)
	}
}
