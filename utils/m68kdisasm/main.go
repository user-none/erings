// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Command m68kdisasm disassembles MC68000 code from a flat binary file.
// It mirrors the SH-2 disasm tool's interface: -file, -base, -addr,
// -count, -all. Instructions are variable length, so -count counts
// instructions, not words; the sweep advances by each instruction's
// decoded length.
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/user-none/go-chip-m68k"
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

// disassembleLines renders up to n instructions starting at startAddr
// (mapped at baseAddr) and writes one line per instruction to w. An
// instruction truncated by the end of the file is rendered as DC.W
// lines, one per remaining word.
func disassembleLines(w io.Writer, data []byte, baseAddr, startAddr uint32, n int) error {
	fileSize := uint32(len(data))

	fetch := func(addr uint32) uint16 {
		if addr < baseAddr {
			return 0
		}
		off := addr - baseAddr
		if off+2 > fileSize {
			return 0
		}
		return binary.BigEndian.Uint16(data[off : off+2])
	}

	line := func(addr uint32, words []byte, text string) error {
		hex := ""
		for j := 0; j+2 <= len(words); j += 2 {
			if j > 0 {
				hex += " "
			}
			hex += fmt.Sprintf("%04X", binary.BigEndian.Uint16(words[j:j+2]))
		}
		// Widest instruction is 10 bytes = five 4-digit groups.
		_, err := fmt.Fprintf(w, "$%06X: %-24s  %s\n", addr, hex, text)
		return err
	}

	addr := startAddr
	for i := 0; i < n; i++ {
		offset := addr - baseAddr
		if offset+2 > fileSize {
			break
		}
		text, size := m68k.Disassemble(addr, fetch)
		if offset+size > fileSize {
			// The decode ran past the end of the file; render the real
			// remaining words individually instead of dropping them.
			for ; offset+2 <= fileSize; offset, addr = offset+2, addr+2 {
				word := binary.BigEndian.Uint16(data[offset : offset+2])
				if err := line(addr, data[offset:offset+2], fmt.Sprintf("DC.W $%04X", word)); err != nil {
					return err
				}
			}
			break
		}

		if err := line(addr, data[offset:offset+size], text); err != nil {
			return err
		}
		addr += size
	}
	return nil
}

func main() {
	filePath := flag.String("file", "", "path to m68k binary file")
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
		fmt.Fprintf(os.Stderr, "error: file size must be even (m68k instructions are word-aligned), got %d bytes\n", fileSize)
		os.Exit(1)
	}

	baseAddr, err := parseHexAddr(*baseStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid -base: %v\n", err)
		os.Exit(1)
	}
	// The 68000 has 24 address bits; the disassembler masks branch
	// targets accordingly, so a file mapped past $FFFFFF would render
	// inconsistent addresses.
	if uint64(baseAddr)+uint64(fileSize) > 1<<24 {
		fmt.Fprintf(os.Stderr, "error: -base 0x%08X puts the file past the 24-bit address space\n", baseAddr)
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

	if addr%2 != 0 {
		fmt.Fprintf(os.Stderr, "error: address 0x%08X is odd (m68k instructions are word-aligned)\n", addr)
		os.Exit(1)
	}
	if addr < baseAddr || addr-baseAddr >= fileSize {
		fmt.Fprintf(os.Stderr, "error: address 0x%08X is outside the file range (0x%08X-0x%08X)\n",
			addr, baseAddr, baseAddr+fileSize-1)
		os.Exit(1)
	}

	n := *count
	if *all {
		// Upper bound: every remaining word as a 2-byte instruction.
		n = int(fileSize-(addr-baseAddr)) / 2
	} else if n <= 0 {
		fmt.Fprintf(os.Stderr, "error: -count must be positive\n")
		os.Exit(1)
	}

	w := bufio.NewWriter(os.Stdout)
	if err := disassembleLines(w, data, baseAddr, addr, n); err != nil {
		fmt.Fprintf(os.Stderr, "error writing output: %v\n", err)
		os.Exit(1)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "error writing output: %v\n", err)
		os.Exit(1)
	}
}
