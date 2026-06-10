// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
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

// dataEntry describes a constant pool word referenced by a PC-relative load.
type dataEntry struct {
	size uint8 // 2 = .data.w (16-bit), 4 = .data.l (32-bit)
}

// regLoad records the 32-bit value most recently loaded into a register by a
// PC-relative long load (MOV.L @(disp,PC),Rn), used to resolve register-
// indirect branch targets.
type regLoad struct {
	val   uint32
	valid bool
}

// disassembleLines renders n instructions starting at startAddr (mapped at
// baseAddr) and writes one formatted line per instruction to w. Constant-pool
// words are shown as .data; register-indirect branch targets (BSRF/BRAF/JSR/
// JMP) are resolved against the most recent PC-relative load of that register
// when known, and delay-slot instructions are marked. Output is streamed: each
// line is written as it is produced rather than accumulated, and the pool is
// seeded inline as PC-relative loads are decoded (loads always precede their
// data, so no look-ahead is needed) and pruned once consumed, so peak memory is
// independent of the input size. The caller is responsible for validating that
// the [startAddr, startAddr+n*2) range fits within the file.
func disassembleLines(w io.Writer, data []byte, baseAddr, startAddr uint32, n int) error {
	fileSize := uint32(len(data))

	pool := make(map[uint32]dataEntry)
	var lastLoad [16]regLoad
	prevDelayed := false
	// Branch destination of the previous (delayed-branch) instruction when
	// statically known. A PC-relative instruction in a delay slot resolves
	// against the branch destination + 2, not its own address + 4.
	var prevTarget uint32
	prevTargetKnown := false

	addr := startAddr
	for i := 0; i < n; i++ {
		offset := addr - baseAddr
		if offset+2 > fileSize {
			break
		}
		op := binary.BigEndian.Uint16(data[offset : offset+2])

		var line string
		if entry, ok := pool[addr]; ok {
			switch {
			case entry.size == 4 && offset+4 <= fileSize:
				val := binary.BigEndian.Uint32(data[offset : offset+4])
				line = fmt.Sprintf("$%06X: %02X %02X  .data.l H'%08X", addr, op>>8, op&0xFF, val)
			case entry.size == 2:
				line = fmt.Sprintf("$%06X: %02X %02X  .data.w H'%04X", addr, op>>8, op&0xFF, op)
			default:
				// Second half of a .data.l, or a 32-bit word that would overrun
				// the file: show the raw bytes only.
				line = fmt.Sprintf("$%06X: %02X %02X", addr, op>>8, op&0xFF)
			}
			// Addresses are processed in increasing order, so a consumed pool
			// entry is never revisited.
			delete(pool, addr)
			prevDelayed = false
			prevTargetKnown = false
		} else {
			text := sh2.Disassemble(addr, op)
			rn := uint8((op >> 8) & 0xF)

			// Branch destination of this instruction, when statically
			// knowable; becomes prevTarget for the next instruction.
			var branchTarget uint32
			branchTargetKnown := false

			switch {
			case op&0xF0FF == 0x0003, op&0xF0FF == 0x0023:
				// BSRF/BRAF Rn: register holds a PC-relative displacement.
				if lastLoad[rn].valid {
					branchTarget = addr + 4 + lastLoad[rn].val
					branchTargetKnown = true
					text += fmt.Sprintf("   ; -> $%06X", branchTarget)
					lastLoad[rn].valid = false
				}
			case op&0xF0FF == 0x400B, op&0xF0FF == 0x402B:
				// JSR/JMP @Rn: register holds an absolute target address.
				if lastLoad[rn].valid {
					branchTarget = lastLoad[rn].val
					branchTargetKnown = true
					text += fmt.Sprintf("   ; -> $%06X", branchTarget)
					lastLoad[rn].valid = false
				}
			case op&0xF000 == 0xA000, op&0xF000 == 0xB000:
				// BRA/BSR: signed 12-bit displacement.
				d := int32(op & 0x0FFF)
				if d >= 0x800 {
					d -= 0x1000
				}
				branchTarget = addr + 4 + uint32(d*2)
				branchTargetKnown = true
			case op&0xFD00 == 0x8D00:
				// BT/S (8Dxx) / BF/S (8Fxx): signed 8-bit displacement.
				branchTarget = addr + 4 + uint32(int32(int8(op&0xFF))*2)
				branchTargetKnown = true
			case op&0xF000 == 0x9000:
				// MOV.W @(disp,PC),Rn - 16-bit constant pool reference.
				disp := uint32(op&0xFF) * 2
				target := addr + 4 + disp
				seed := true
				if prevDelayed {
					if prevTargetKnown {
						target = prevTarget + 2 + disp
						text = fmt.Sprintf("MOV.W @(H'%08X),R%d", target, rn)
					} else {
						seed = false
						text = fmt.Sprintf("MOV.W @(%d,PC),R%d   ; PC-rel base unknown", disp, rn)
					}
				}
				if seed && target >= baseAddr && target-baseAddr+2 <= fileSize {
					pool[target] = dataEntry{size: 2}
				}
			case op&0xF000 == 0xD000:
				// MOV.L @(disp,PC),Rn - 32-bit constant pool reference; also
				// record the loaded value for register-indirect resolution.
				disp := uint32(op&0xFF) * 4
				target := (addr &^ 3) + 4 + disp
				seed := true
				if prevDelayed {
					if prevTargetKnown {
						target = ((prevTarget + 2) &^ 3) + disp
						text = fmt.Sprintf("MOV.L @(H'%08X),R%d", target, rn)
					} else {
						seed = false
						text = fmt.Sprintf("MOV.L @(%d,PC),R%d   ; PC-rel base unknown", disp, rn)
						lastLoad[rn].valid = false
					}
				}
				if seed && target >= baseAddr && target-baseAddr+4 <= fileSize {
					pool[target] = dataEntry{size: 4}
					// Second half of the 32-bit value.
					pool[target+2] = dataEntry{size: 0}
					lastLoad[rn] = regLoad{binary.BigEndian.Uint32(data[target-baseAddr : target-baseAddr+4]), true}
				}
			case op&0xFF00 == 0xC700:
				// MOVA @(disp,PC),R0.
				if prevDelayed {
					disp := uint32(op&0xFF) * 4
					if prevTargetKnown {
						text = fmt.Sprintf("MOVA @(H'%08X),R0", ((prevTarget+2)&^3)+disp)
					} else {
						text = fmt.Sprintf("MOVA @(%d,PC),R0   ; PC-rel base unknown", disp)
					}
				}
			}

			if prevDelayed {
				text += "   ; delay slot"
			}
			line = fmt.Sprintf("$%06X: %02X %02X  %s", addr, op>>8, op&0xFF, text)
			prevDelayed = sh2.IsDelayedBranch(op)
			prevTarget = branchTarget
			prevTargetKnown = branchTargetKnown && prevDelayed
		}

		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
		addr += 2
	}
	return nil
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
	} else if uint64(addr-baseAddr)+uint64(n)*2 > uint64(fileSize) {
		// 64-bit math so a very large -count cannot wrap the range check.
		fmt.Fprintf(os.Stderr, "error: address range exceeds file size (0x%08X-0x%08X)\n",
			baseAddr, baseAddr+fileSize-1)
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
