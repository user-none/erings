// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"
	"fmt"

	"github.com/user-none/erings/core/sh2"
)

// =============================================================================
// HLE BIOS - design reference
// =============================================================================
//
// The HLEBIOS struct stands in for the real Saturn BIOS when no licensed
// BIOS image is supplied. It does the minimum work needed for a disc IP
// (Initial Program) + game to run: validate the IP, copy it into WRAM-H,
// populate the BIOS-published data tables, hook the SH-2 so future BIOS-
// service calls land in Go code, and start the master CPU at the IP entry
// point.
//
// This is not a comprehensives bios replacement implantation. Only feature
// supported by the emulator are replicated by the HLEBIOS.
//
// This block documents the design. The rest of this file (and the
// hlebios_*.go siblings) implements it. Read this first.
//
// -----------------------------------------------------------------------------
// 1. What "BIOS service" means here
// -----------------------------------------------------------------------------
//
// Game code calls BIOS routines indirectly: it loads a function pointer
// from a well-known WRAM-H slot (e.g. $06000300 = SYS_SETUINT) and JSRs
// through it. On real hardware the slot holds the address of a small
// BIOS-ROM or WRAM-H routine that does the actual work and RTSes back.
//
// The HLE keeps the slot-based contract intact - games still JSR through
// the same slot at the same address - but redirects each pointer to a
// reserved magic address. When the SH-2 fetches an instruction from a
// magic address, the emulator dispatches to a Go function instead.
//
// The list of BIOS-published slots and which routine each holds is
// documented in docs/bios/system_services.md and docs/bios/rom_layout.md.
// All slot offsets used by the HLE are named at the top of this file
// (wramH*) or in the slot-table comment block in populateDataTables.
//
// -----------------------------------------------------------------------------
// 2. The magic-address trap
// -----------------------------------------------------------------------------
//
// Magic addresses live in the $A0000000-$A0000FFF range. Nothing on real
// hardware would put code there: the range is reserved by the HLE.
// Constants defined below give them human-readable names (hleSysSetUint,
// hleBiosPERInit, etc.).
//
// The trap fires inside sh2.CPU's execute loop. Before fetching the next
// instruction, the loop checks `PC >> 12 == 0xA0000`. If true:
//
//   1. The CPU calls HLEHook(PC) - a closure dispatch returned a service
//      function for that exact address registered earlier.
//   2. The Go service runs. It reads its arguments from cpu.Registers()
//      (R4..R7, etc.), mutates bus memory and CPU state directly, and
//      writes any return value to R0 via cpu.SetReg(0, val).
//   3. The CPU sets PC := PR - exactly what RTS would have done - and
//      returns to the caller. No instruction is ever fetched from the
//      magic address; the bus has no special-case logic for the range.
//
// From the game's perspective the JSR returns normally. The fact that
// the body ran in Go rather than SH-2 is invisible.
//
// This is the entire dispatch mechanism. Five rules follow from it:
//
//   - Magic addresses are NOT real callable code. A game that JMPs to
//     one and never JSRs (so PR is wrong) will trap, run the service,
//     then jump to whatever PR happened to hold. Always reach magic
//     addresses via JSR (or a JSR-equivalent: BSR, slave reset vector
//     setting PR explicitly).
//   - The Go service has full read/write access to bus and CPU state.
//     The trap is positioned so that PC, PR, SR, and all R registers
//     are exactly what they were when the caller did the JSR.
//   - The trap is uniform: any path that puts PC in the magic range
//     fires it. Hardware-driven dispatch (reset vector, VBR-based IRQ
//     entry) works the same way as game-driven JSR.
//   - Don't put magic addresses into slots that games read as DATA
//     (rare but happens - see "sentinel vs no-op handler" below).
//   - The mechanism is in core/sh2/execute.go (~6 lines). The HLE
//     side is dispatch+register in this file.
//
// -----------------------------------------------------------------------------
// 3. Lifecycle: Boot order matters
// -----------------------------------------------------------------------------
//
// HLEBIOS.Boot runs once before the master SH-2 starts. The order is:
//
//   copyIP             - IP image into WRAM-H at $06002000
//   installNoopHandler - RTS+NOP stub at $0600083C (must exist before
//                        anything points at it)
//   populateDataTables - every BIOS-set WRAM-H value (slot pointers,
//                        workspace, IMS shadow, semaphores, System ID
//                        cache, etc.)
//   installIntStubs    - SCU interrupt vector trampolines + dispatcher
//                        (these MUST be real SH-2 code; see 5)
//   populateVBRTable   - master VBR with default-RTE everywhere + the
//                        32 SCU stub addresses at vec $40-$5F
//   installVirtualBIOS - 512 KB virtual BIOS ROM with reset vectors
//                        + a slave halt loop + sentinels in the
//                        BIOS-ROM public routine table
//   registerServices   - bind each magic address to its Go function
//   HLEHook wiring     - both master and slave CPUs get the dispatch
//                        closure, so any subsequent magic-address fetch
//                        traps to Go
//   CPU initial state  - master PC, R15, VBR, SR, GBR set to match the
//                        captured real-BIOS handoff dump
//
// If registerServices runs before populateDataTables, the data tables hold
// magic addresses that aren't yet bound to handlers, and the first JSR
// through one becomes a silent no-op.
//
// -----------------------------------------------------------------------------
// 4. Slots, sentinels, and no-op handlers
// -----------------------------------------------------------------------------
//
// Three values can appear in a dispatch slot:
//
//   (a) A magic address bound to a real Go service:
//         e.g. mem.L[$06000300] = hleSysSetUint ($A0000000)
//       JSR -> trap -> hleSysSetUintService -> return.
//
//   (b) hleSentinel ($A0000020) bound to hleSentinelService (empty body).
//       Used for slots where we know the call must succeed but the
//       routine has no observable effect we need to model. JSR -> trap
//       -> empty Go function -> return. Equivalent to RTS+NOP, but
//       reached via the trap pathway.
//
//   (c) wramHNoopHandlerAddr ($0600083C), a real WRAM-H location holding
//       RTS+NOP. The address points at actual SH-2 code that the CPU
//       fetches and executes; no trap fires. Used in two cases:
//         - Slots that game code reads back as DATA (not just as
//           function pointers). A magic address has high half $A000
//           which sign-extends negative when reinterpreted as a 16-bit
//           value; the real-BIOS address $0600 stays positive. The SDK
//           and game code that arithmetic on slot values relies on this.
//         - SCU user-handler table entries for vectors that don't have
//           a game-installed handler. The IRQ dispatcher (see 5)
//           JSRs through whatever's in the slot; sentinels work, but
//           the real-BIOS bytes ($0600083C) are what the BIOS itself
//           writes post-Phase 4, so we match.
//
// Rule of thumb: pick (a) when you have a Go implementation. Pick (b)
// when the slot is a function-pointer-only contract that must "succeed."
// Pick (c) only when slot reads need a non-negative high half.
//
// -----------------------------------------------------------------------------
// 5. Real SH-2 code embedded in WRAM-H
// -----------------------------------------------------------------------------
//
// Three places lay down actual SH-2 instructions, because the SH-2
// reaches them on paths the magic-address trap can't intercept:
//
//   wramHDefaultRTE  ($0600094A):  "RTE; NOP" - slave VBR points all
//                                  128 entries here so unexpected
//                                  exceptions just return.
//   wramHNoopHandler ($0600083C):  "RTS; NOP" - see 4(c).
//   wramHIntStubBase ($06000600+): 32 per-vector SCU stubs + a common
//                                  dispatcher (~270 bytes total). When
//                                  the SCU asserts an IRQ the SH-2
//                                  hardware reads VBR[vec], pushes
//                                  SR+PC, and jumps there. We can't
//                                  trap that fetch with HLEHook because
//                                  it'd be wrong to RTS-return from an
//                                  IRQ (need RTE). The dispatcher
//                                  preserves caller-save regs around
//                                  a JSR to the game-installed handler
//                                  at $06000900+vec*4, then RTEs.
//
// The slave SH-2's reset vector at mem.L[$00000000] is the EXCEPTION:
// it's set to a magic address (hleSlaveInit) and the trap handles it.
// That works because reset comes via SMPC SSHON, not a hardware IRQ,
// and the slave-init service explicitly sets PR before returning so
// the PC := PR exit lands at the game's slave entry.
//
// -----------------------------------------------------------------------------
// 6. Adding a new service
// -----------------------------------------------------------------------------
//
//   1. Pick a magic address in the $A00000xx range that's not already
//      taken (grep the file for "0xA00000" - gaps are obvious).
//   2. Add a constant: `hleBiosFooBar = 0xA00000NN` with a comment
//      describing which BIOS routine it replaces.
//   3. Write the Go service with signature
//      `func(cpu *sh2.CPU, bus *Bus)`. Read args from cpu.Registers(),
//      mutate bus / cpu state, set R0 via cpu.SetReg(0, ...) if the
//      caller expects a return value. See hlebios_services.go for
//      examples (every SYS_* service is a 4-10 line function).
//   4. Register in registerServices(): `h.register(hleBiosFooBar,
//      hleBiosFooBarService)`.
//   5. Write the magic address into the WRAM-H slot the routine
//      replaces: usually a line in populateDataTables(), or in
//      biosPointerSlotTargets[] for $06000234-$0600034C BIOS-internal
//      function-pointer slots.
//
// That's it. JSR through the slot now lands in your Go service.
//
// -----------------------------------------------------------------------------
// 7. Debugging
// -----------------------------------------------------------------------------
//
//   - A service that "doesn't fire": is the slot actually written to
//     the magic address? Print mem.L[slot] at run time and compare to
//     the magic constant. If the slot still holds hleSentinel
//     ($A0000020), the routine isn't bound (yet).
//   - A service that fires but produces wrong output: check the
//     register state at entry by printing cpu.Registers() at the
//     top of the service. The SDK call's R4..R7 are what the game
//     passed; verify against the documentation in docs/bios/.
//   - Game crashes immediately after a JSR through a slot: the trap
//     ran, the Go service set wrong PR or didn't set R0 the caller
//     expected. PR is whatever the JSR pushed; do NOT overwrite it
//     unless the service is the slave-init kind that uses PR as a
//     redirect target.
//   - "Address $A000xxxx" appears in game state where a real-BIOS
//     address belongs: a slot is being read as data but holds a
//     magic address. Use (c) above - a real WRAM-H code location.
//   - Hardware-IRQ misbehavior: not magic-address-related. See the
//     SCU dispatcher block in installIntStubs.
//
// =============================================================================

// IP.BIN layout on a Saturn disc.
//
// The first 16 sectors of the data track hold the Initial Program.
// Each raw CD-XA Mode 1 sector is 2352 bytes: 12 sync + 4 header +
// 2048 user + 288 EDC/ECC. The BIOS validates the IP from its System
// ID header (offset 0..$FF) and loads it into Work-RAM-H at
// $06002000 as a 32 KB window the disc's own code subsequently runs
// from.
//
// The IP's internal layout (per Disc Format Standards ST-040):
//
//	$0000-$00FF  System ID header (256 B)
//	$0100-...    Security code SYS_SEC.OBJ (~3.3 KB, SDK-provided)
//	~$0E00       Area code SYS_ARE.OBJ (~256 B, SDK-provided)
//	~$0F00+      Application Initial Program (AIP, game bootstrap)
//
// The BIOS hands off to SYS_SEC at $06002100 (= $06002000 + $100).
// Captured runtime observation confirms this is the master SH-2
// PC at the moment a real BIOS_USA boot stops running its own code
// and the disc's code starts running.
const (
	ipSectors    = 16
	ipSectorSize = 2048
	ipSectorRaw  = 2352
	ipUserOffset = 16 // 12 sync + 4 sector header
	ipLoadAddr   = 0x06002000
	ipEntry      = 0x06002100 // IP+$100, SYS_SEC entry; observed BIOS handoff PC
	ipStack      = 0x06002000 // top of IP load window; stack grows down
)

// Magic addresses for each HLE BIOS service. Each constant names a
// distinct $A00000xx slot bound by registerServices() to a Go function
// in hlebios_services.go (or hlebios_per.go / hlebios_bup.go). See
// "HLE BIOS - design reference" at the top of the file for how these
// are dispatched.
const (
	hleSysSetUint  = 0xA0000000
	hleSysGetUint  = 0xA0000004
	hleSysSetSint  = 0xA0000010
	hleSysGetSint  = 0xA0000014
	hleSentinel    = 0xA0000020 // default empty handler (no-op service)
	hleSysTassem   = 0xA0000030
	hleSysClrsem   = 0xA0000034
	hleSysSetScuim = 0xA0000040
	hleSysChgScuim = 0xA0000044
	hleSysChgSysCk = 0xA0000048

	// BIOS-published WRAM-H routine replacements (slots at
	// $06000234-$0600034C; see docs "BIOS-Published WRAM-H
	// Function-Pointer Slots"). Each slot's BIOS pointer is replaced
	// with one of these magic addresses; the dispatcher invokes the
	// matching Go function.
	hleBiosFill        = 0xA0000064 // sub_02AC: table-driven longword fill
	hleBiosCopy        = 0xA0000068 // sub_02BC: table-driven longword copy
	hleBiosScuISTClear = 0xA000006C // $06000664: SCU IST register clear
	hleBiosByteExpand  = 0xA0000070 // $06000810: byte-array sign-extend to longwords
	// Warning-only handlers (not implemented yet; print on call).
	hleBiosWarnSMPCInit   = 0xA0000074 // sub_04C8
	hleBiosWarnGBRZero    = 0xA0000078 // sub_1800
	hleBiosWarnRegionInit = 0xA000007C // sub_1A18
	hleBiosWarnCDBlock    = 0xA0000084 // shared across 13 CD-block helpers

	// sub_1C90: a Go shortcut that reads
	// the game executable directly from disc into $06004000 (the
	// application entry the IP JSRs to after the security screen).
	// See hleBiosSub1C90LoadGameService for the full rationale.
	hleBiosSub1C90Load = 0xA0000080

	// PER_Init service. Slot $06000358 dispatches the BIOS PER_Init
	// trampoline (real BIOS body at $0007D600). The Go service
	// installs an RTS+NOP at the caller's R4 (driver buffer addr),
	// fills peripheral records at the caller's R6 (output buffer),
	// and shuffles R4/R5 to mirror real BIOS's exit register state.
	hleBiosPERInit = 0xA000008C

	// Slave SH-2 init. Slave's reset-vector PC (mem.L[$00000000])
	// points at this magic address, so a slave.Reset (via SMPC
	// SSHON -> smpc.cmdSSHON -> slave.Reset -> LoadResetVectors)
	// lands the slave directly at this PC with PR=0. The Go service
	// reproduces the structurally-meaningful effects of real BIOS
	// slave init at $00000200 + $06000600: set slave VBR, signal
	// "2RDS" to master, set slave SP, and SetPR to the game's slave
	// entry. After the service returns, the SH-2 execute loop's
	// magic-address trap sets PC := PR, landing the slave there.
	hleSlaveInit = 0xA00000F0
)

// Work-RAM-H offsets the BIOS pre-populates and that disc/SDK code
// reads. See project_hle_bios_state_inventory.md.
const (
	wramHIPEntryPtr   = 0x254 // IP entry pointer ($06002100)
	wramHWorkspacePtr = 0x260 // workspace pointer ($06000D00)
	wramHSysTable     = 0x300 // SYS_*/PER_* dispatch table
	wramHIMSShadow    = 0x348 // SCU IMS shadow (SDK reads this for
	//                           SYS_GETSCUIM; live IMS register is write-only)
	wramHUIntTable = 0x900 // SCU user-handler table base. SETUINT
	//                           writes at base + R4*4; for SCU
	//                           vectors $40-$5F the effective slots
	//                           are $0A00-$0A7F.
	wramHPriMaskTable = 0x980 // SCU priority/mask table base. Same
	//                           base+vec*4 trick: SCU vec entries
	//                           land at $0A80-$0AFF.
	wramHSemArray   = 0xB00 // SYS_TASSEM/CLRSEM byte array (256 entries)
	wramHSysIDCache = 0xC00 // per-disc System ID cache (256 B copied from IP)
	wramHWorkspace  = 0xD00 // BIOS workspace (16 B zero-filled)

	// Default RTE+NOP exception handler. Matches the real-BIOS
	// address ($0600094A) so the slave VBR table at $06000400 (per
	// SDK convention) and the master VBR table at $06000000 can
	// both point at the same handler. Real-BIOS handoff dumps
	// confirm this is where the live RTE+NOP code lives.
	wramHDefaultRTE = 0x94A
	// Slave SH-2 VBR table. SDK convention places it at $06000400;
	// real BIOS pre-populates the 128 entries with default-RTE
	// pointers so the slave (started later by SMPC SSH_ON) gets a
	// safe handler for any exception until the game installs its
	// own slave vectors. The previous HLE layout put master
	// interrupt-dispatch code in this region, corrupting slave
	// dispatches when the slave's VBR landed here.
	wramHSlaveVBR = 0x400
	// Master SCU vector trampoline stubs. Located above the slave
	// VBR window so the two don't collide. Each stub is 6 bytes
	// (push R0, BRA to common dispatcher, load vector into R0 in
	// the delay slot); the common dispatcher follows the stub block.
	wramHIntStubBase = 0x600
	hleIntStubStride = 6
	// Common SCU dispatcher follows the 32 trampoline stubs.
	wramHIntDispatcher = wramHIntStubBase + 0x20*hleIntStubStride

	// No-op handler at $0600083C. Real BIOS deposits a 2-instruction
	// RTS+NOP here during Phase 4 and uses $0600083C as the sentinel
	// value for every unused dispatch-table slot, SCU-handler slot
	// after SETUINT(0), and other "this slot is wired but does
	// nothing" location. The address is the salient detail: game
	// code reads slot values as 16-bit halves and treats the high
	// half ($0600) as the top of a pointer; a magic-address sentinel
	// like $A0000020 has a high half of $A000 which sign-extends
	// negative and breaks the game's pointer arithmetic.
	wramHNoopHandler     = 0x83C
	wramHNoopHandlerAddr = 0x06000000 + wramHNoopHandler

	// Per-vector interrupt default-handler table in BIOS ROM at $0600
	// (128 entries x 4 B), read by SYS_SETSINT for handler=0.
	wramHIntDefaultTable = 0x600
)

// biosPointerSlots lists WRAM-H offsets the real BIOS populates
// with BIOS-internal FUNCTION POINTERS (BIOS-ROM addresses or
// WRAM-H BIOS-code addresses). Disc IP code dereferences each
// slot and JSRs through the resulting address. HLE installs
// hleSentinel at each so the call returns cleanly until the
// corresponding Go replacement is wired in.
//
// Excluded from this list (NOT sentinel'd - they are data, not
// function pointers, and the IP reads them as constants):
//
//	$023C  data-table head address ($00000350 in real BIOS)
//	$02B0  1st Read Address; populated separately in populateDataTables
//	        from System ID +$F0 (= $06004000 for typical Saturn discs)
//
// See docs/bios/system_services.md "BIOS-Published WRAM-H
// Function-Pointer Slots" for the per-slot purpose audit.
var biosPointerSlots = []uint32{
	0x234, 0x238, // sub_02AC fill, sub_02BC copy
	0x250, 0x258, 0x25C, // boot-helper pointers ($06000646 halt, $0600083C sentinel x2)
	0x268, 0x26C, 0x270, 0x274, // sub_1A18, sub_186C, sub_30DC, sub_3174
	0x280, 0x284, 0x288, 0x28C, // $06000810 atomic memcpy, sub_1C90 int setup, sub_18A8, sub_1874
	0x298, 0x29C, 0x2A0, // sub_31F4, sub_1904, sub_1800
	0x2C4, 0x2C8, 0x2CC, 0x2D0, 0x2D4, 0x2D8, 0x2DC, // CD-block helpers
	0x328, 0x32C, // sub_04C8 SMPC init, sub_1800 (second ref)
	0x34C, // $06000664 SCU IST clear
}

// readIPImage reads what would be the disc's Initial Program (16
// sectors of user data, 32 KB) into a contiguous slice. No content
// validation is performed: the disc may not even be a Saturn game
// (an audio CD legitimately plays under the real BIOS). HLEBIOS.Boot
// is responsible for verifying the bytes form a usable game image.
// Returns nil if any sector read fails or is short.
func readIPImage(d DiscReader) []byte {
	ip := make([]byte, ipSectors*ipSectorSize)
	for s := 0; s < ipSectors; s++ {
		raw, err := d.ReadSector(s)
		if err != nil || len(raw) < ipUserOffset+ipSectorSize {
			return nil
		}
		copy(ip[s*ipSectorSize:], raw[ipUserOffset:ipUserOffset+ipSectorSize])
	}
	return ip
}

// hleFunc is the signature every HLE BIOS service implements. The
// service reads arguments from cpu.Registers(), mutates memory via
// bus, and writes results back via cpu.SetReg. The SH-2 execute
// loop's magic-address trap calls this function instead of fetching
// an instruction; after it returns, the SH-2 jumps to PR (the RTS
// return path) without ever executing a real opcode at the magic
// address.
type hleFunc func(cpu *sh2.CPU, bus *Bus)

// HLEBIOS replaces the real Saturn BIOS for boots that don't have a
// licensed BIOS image attached. It owns the SH-2 magic-address hook
// table and the Work-RAM-H data-table state the BIOS would normally
// publish.
//
// Lifecycle: constructed locally in Emulator.Start when !hasBIOS,
// Boot is called once, and the struct is reached thereafter only
// through the closures held in CPU.HLEHook. The Emulator keeps no
// direct reference; the closure capture keeps the struct alive.
type HLEBIOS struct {
	bus    *Bus
	master *sh2.CPU
	slave  *sh2.CPU
	hooks  map[uint32]hleFunc
}

// NewHLEBIOS constructs an HLEBIOS bound to a specific bus and the
// master/slave SH-2 pair. Boot must be called to actually publish
// the dispatch table and wire the CPU hooks.
func NewHLEBIOS(bus *Bus, master, slave *sh2.CPU) *HLEBIOS {
	return &HLEBIOS{
		bus:    bus,
		master: master,
		slave:  slave,
		hooks:  make(map[uint32]hleFunc),
	}
}

// Boot performs the minimum-state High-Level Emulation of the
// Saturn BIOS startup against the provided IP image:
//
//  1. Validates the bytes form a Saturn game image by checking the
//     System ID hardware identifier.
//  2. Copies the IP into Work-RAM-H at $06002000-$0600A000.
//  3. Populates every BIOS-set Work-RAM-H data table that
//     disc/SDK code expects to find pre-initialized.
//  4. Registers every SYS_*/PER_*/BUP_* service hook.
//  5. Wires CPU.HLEHook on master and slave.
//  6. Sets master SH-2 PC=$06002100 (IP+$100, SYS_SEC entry) and
//     R15=$06002000 (top of IP window).
//
// Returns an error if no IP is provided or the bytes don't look
// like a Saturn game (HLE has no CD-player path; only a real BIOS
// can boot non-game discs).
func (h *HLEBIOS) Boot(ip []byte) error {
	if ip == nil {
		return errors.New("HLEBIOS.Boot: no IP image")
	}
	if len(ip) < 16 {
		return fmt.Errorf("HLEBIOS.Boot: IP image too short (%d bytes)", len(ip))
	}
	if got := string(ip[0:16]); got != "SEGA SEGASATURN " {
		return fmt.Errorf("HLEBIOS.Boot: not a Saturn game disc (System ID %q); HLE cannot boot non-game media", got)
	}

	h.copyIP(ip)
	h.installNoopHandler()
	h.populateDataTables(ip)
	h.installIntStubs()
	h.populateVBRTable()
	h.installVirtualBIOS()
	h.populateIntDefaultTable()
	h.registerServices()

	h.master.HLEHook = h.dispatch(h.master)
	h.slave.HLEHook = h.dispatch(h.slave)

	h.master.SetPC(ipEntry)
	h.master.SetReg(15, ipStack)
	h.master.SetVBR(0x06000000)
	h.master.SetSR(0x00000001)
	h.master.SetGBR(0x25D00000)
	return nil
}

// register binds a magic address to a Go service implementation.
// Called by registerServices.
func (h *HLEBIOS) register(addr uint32, fn hleFunc) {
	h.hooks[addr] = fn
}

// dispatch returns the closure wired into CPU.HLEHook for the given
// CPU. The closure captures both the HLEBIOS and the CPU, so even
// though the Emulator holds no reference to HLEBIOS the struct
// stays alive as long as the CPU keeps the hook.
func (h *HLEBIOS) dispatch(cpu *sh2.CPU) func(pc uint32) {
	return func(pc uint32) {
		if pc>>12 != 0xA0000 {
			return
		}
		if fn, ok := h.hooks[pc]; ok {
			fn(cpu, h.bus)
		}
	}
}

// copyIP places the 32 KB IP image at Work-RAM-H offset $2000.
func (h *HLEBIOS) copyIP(ip []byte) {
	off := uint32(ipLoadAddr) - 0x06000000
	copy(h.bus.wramH[off:], ip)
}

// populateDataTables seeds every Work-RAM-H region the real BIOS
// would have written before handing off to the disc. See
// project_hle_bios_state_inventory.md for the inventory.
func (h *HLEBIOS) populateDataTables(ip []byte) {
	// IP entry / workspace pointers used by SDK code as known-good
	// constants. The IP entry pointer is what some SDK functions
	// jump through to re-enter SYS_SEC.
	h.bus.writeWramHU32(wramHIPEntryPtr, ipEntry)
	h.bus.writeWramHU32(wramHWorkspacePtr, 0x06000000+wramHWorkspace)

	// $060002B0 = "1st Read Address" from System ID +$F0. The BIOS
	// sets this before handoff so sub_1C90 / sub_2F48 know where to
	// land the application binary. Confirmed in the captured handoff
	// state (both NiGHTS and the other captured game have $06004000
	// here, matching their respective System ID +$F0).
	loadAddr := uint32(ip[0xF0])<<24 | uint32(ip[0xF1])<<16 |
		uint32(ip[0xF2])<<8 | uint32(ip[0xF3])
	h.bus.writeWramHU32(0x2B0, loadAddr)

	// "2RDY" warm-boot handshake magic at $06000240. Real BIOS Phase
	// 3 sets this before handoff (captured handoff dumps confirm
	// $32524459). The real-BIOS slave-init code at $00000200-$0000027F
	// reads mem.L[$06000240] and checks for this magic to know that
	// master-side warm-boot work is done; with the slot at zero the
	// slave's init takes a different path. Some games (Waku Waku 7's
	// wait→loading transition) won't proceed until this matches.
	h.bus.writeWramHU32(0x240, 0x32524459)

	// BIOS-internal function pointer slots in $06000234-$0600034C.
	// Each slot gets a magic address corresponding to either:
	//  - the Go implementation of the BIOS routine, or
	//  - a warning-printing handler (for routines we haven't
	//    implemented yet but want to know if the IP calls), or
	//  - hleSentinel (for routines that are confirmed no-op-safe
	//    or for which we don't yet have a specific handler).
	// See biosPointerSlotTargets for the per-slot mapping; this loop
	// only writes slots that are in our list, so non-pointer data
	// slots ($023C, $02B0) are left untouched.
	for _, slot := range biosPointerSlots {
		target, ok := biosPointerSlotTargets[slot]
		if !ok {
			target = hleSentinel
		}
		h.bus.writeWramHU32(slot, target)
	}

	// SYS_*/PER_*/BUP_* dispatch table at $06000300. Pattern verified
	// against the real-BIOS handoff WRAM-H dump:
	//   $308, $30C, $318, $31C, $338, $33C, $360+ = $0600083C (no-op)
	//   $324, $350, $354 = $00000000  (data zeros, NOT pointers)
	//   $348 = IMS shadow (set separately below to $0000BFFF)
	//   other slots = specific service entry points (overwritten
	//                 with HLE magic addresses immediately below)
	// Game code reads some of these as plain longwords (not just
	// as function pointers), so matching the real-BIOS bytes is
	// required - the sentinel can't be a generic magic address.
	for off := uint32(0); off < 0x60; off += 4 {
		h.bus.writeWramHU32(wramHSysTable+off, wramHNoopHandlerAddr)
	}
	// Explicit-zero slots (data, not pointers) per handoff dump.
	h.bus.writeWramHU32(wramHSysTable+0x24, 0)
	h.bus.writeWramHU32(wramHSysTable+0x50, 0)
	h.bus.writeWramHU32(wramHSysTable+0x54, 0)
	h.bus.writeWramHU32(wramHSysTable+0x00, hleSysSetUint)
	h.bus.writeWramHU32(wramHSysTable+0x04, hleSysGetUint)
	h.bus.writeWramHU32(wramHSysTable+0x10, hleSysSetSint)
	h.bus.writeWramHU32(wramHSysTable+0x14, hleSysGetSint)
	h.bus.writeWramHU32(wramHSysTable+0x30, hleSysTassem)
	h.bus.writeWramHU32(wramHSysTable+0x34, hleSysClrsem)
	h.bus.writeWramHU32(wramHSysTable+0x40, hleSysSetScuim)
	h.bus.writeWramHU32(wramHSysTable+0x44, hleSysChgScuim)
	h.bus.writeWramHU32(wramHSysTable+0x20, hleSysChgSysCk)
	// PER_Init at $06000358: magic address dispatched in Go. The
	// service writes RTS+NOP at the caller's R4 buffer (so any
	// later JSR to "the driver entry" returns cleanly) AND
	// populates the caller's R6 peripheral output buffer with
	// current pad records - replicating the side effect of real
	// BIOS PER_Init calling the decompressed driver.
	h.bus.writeWramHU32(wramHSysTable+0x58, hleBiosPERInit)

	// SCU IMS shadow at $06000348. Initialized to the SCU's reset
	// value ($0000BFFF) so SYS_GETSCUIM and CHGSCUIM's RMW step see
	// a sane starting mask. The slot sits inside the dispatch-table
	// region but is not a function pointer; SDK code reads it as data.
	h.bus.writeWramHU32(wramHIMSShadow, 0x0000BFFF)

	// SCU user-handler table sentinels for vectors $40-$5F. Real
	// BIOS dispatcher does mem[$06000900 + vec*4]; for those
	// vectors the effective slots are $06000A00-$06000A7F. Filling
	// with the $0600083C no-op handler - same bytes the real BIOS
	// uses post-Phase 4 (verified against captured handoff dump).
	// SETUINT overwrites at runtime as games install handlers.
	for vec := uint32(0x40); vec <= 0x5F; vec++ {
		h.bus.writeWramHU32(wramHUIntTable+vec*4, wramHNoopHandlerAddr)
	}

	// SCU priority/mask table for vectors $40-$5F. Same base+vec*4
	// indexing; effective slots at $06000A80-$06000AFF. Real BIOS
	// fills these with (SR_I << 16) | IMS_mask precomputed pairs;
	// the exact derivation isn't reversed yet. $00F0FFFF =
	// "highest priority / mask all" is a safe default - HLE doesn't
	// replicate the dispatcher's priority-lowering logic so this
	// table isn't actually read in HLE today; populated only so
	// any future code that does read it gets a defined value.
	for vec := uint32(0x40); vec <= 0x5F; vec++ {
		h.bus.writeWramHU32(wramHPriMaskTable+vec*4, 0x00F0FFFF)
	}

	// Semaphore array is zero-filled (which the bus already
	// allocates as), but be explicit.
	for i := uint32(0); i < 256; i++ {
		h.bus.wramH[wramHSemArray+i] = 0
	}

	// Per-disc System ID cache: first 256 B of the IP.
	copy(h.bus.wramH[wramHSysIDCache:wramHSysIDCache+0x100], ip[:0x100])

	// Workspace.
	for i := uint32(0); i < 16; i++ {
		h.bus.wramH[wramHWorkspace+i] = 0
	}
}

// installIntStubs writes the SH-2 dispatch code used to handle SCU
// interrupts plus the BIOS-compatible default exception handler and
// slave VBR table.
//
// Layout:
//
//	$0600094A  RTE+NOP                  default exception handler
//	$06000400-$060005FF  128-entry slave VBR table, all pointing
//	                     at $0600094A (matches real-BIOS handoff;
//	                     SDK convention puts slave VBR at $06000400)
//	$06000600+  32 SCU vector trampolines, 6 B each:
//	    +00  MOV.L R0, @-R15            ; push R0
//	    +02  BRA   wramHIntDispatcher   ; branch to common dispatcher
//	    +04  MOV   #vec, R0             ; vector number (delay slot)
//	$060006C0  common dispatcher.
//
// The dispatcher saves the full caller-save register set (R0 from
// the stub plus R1-R7, GBR, PR) around the handler call, then
// restores them and RTEs. The interrupted code expects R0-R7 + GBR
// + PR to be unchanged after the IRQ - the user handler is free to
// clobber any of them, so the dispatcher must do the preservation.
//
//	STS.L PR, @-R15
//	STC.L GBR, @-R15
//	MOV.L R1-R7 individually @-R15
//	MOV.L user_table_const, R1   ; R1 = $06000900
//	SHLL2 R0                     ; R0 = vec*4
//	MOV.L @(R0,R1), R0           ; R0 = installed handler
//	JSR @R0
//	NOP
//	MOV.L @R15+, R7-R1
//	LDC.L @R15+, GBR
//	LDS.L @R15+, PR
//	MOV.L @R15+, R0
//	RTE
//	NOP
//	.long $06000900              ; user-handler table base
func (h *HLEBIOS) installIntStubs() {
	defaultRTEAddr := uint32(0x06000000) + wramHDefaultRTE

	writeWord(h.bus, wramHDefaultRTE+0, 0x002B) // RTE
	writeWord(h.bus, wramHDefaultRTE+2, 0x0009) // NOP

	for i := uint32(0); i < 128; i++ {
		h.bus.writeWramHU32(wramHSlaveVBR+i*4, defaultRTEAddr)
	}

	for vec := uint32(0x40); vec <= 0x5F; vec++ {
		base := wramHIntStubBase + (vec-0x40)*hleIntStubStride
		writeWord(h.bus, base+0, 0x2F06)
		delta := int32(wramHIntDispatcher) - int32(base+6)
		disp := uint16((delta / 2) & 0x0FFF)
		writeWord(h.bus, base+2, 0xA000|disp)
		writeWord(h.bus, base+4, 0xE000|uint16(vec&0xFF))
	}

	// Dispatcher layout (size fixed; precomputed so the MOV.L
	// @(disp,PC),R1 can be emitted with its final displacement
	// rather than as a placeholder patched up afterward).
	//
	//   +$00..+$11   9 register pushes (STS PR, STC GBR, MOV.L R1-R7)
	//   +$12         MOV.L @(disp,PC),R1   <- loads user-handler table
	//   +$14..+$1B   SHLL2 R0; MOV.L @(R0,R1),R0; JSR @R0; NOP
	//   +$1C..+$2B   7 pops R7-R1
	//   +$2C..+$33   LDC.L GBR; LDS.L PR; MOV.L R0; RTE; NOP
	//   +$34         .long $06000900 (wramHUIntTable base)
	//
	// 52 bytes of instructions; constant at offset 52, which is
	// 4-aligned given the dispatcher base is 4-aligned. The PC base
	// for MOV.L @(disp,PC) is ((+$12)+4) & ~3 = +$14; constant is
	// at +$34, so disp = ($34-$14)/4 = $20/4 = 8.
	d := uint32(wramHIntDispatcher)
	const constOffFromBase = 52
	const movLOffFromBase = 18
	pcBase := (movLOffFromBase + 4) &^ 3
	movLDisp := uint16((constOffFromBase - pcBase) / 4)

	off := d
	emit := func(op uint16) {
		writeWord(h.bus, off, op)
		off += 2
	}
	emit(0x4F22)                     // STS.L PR, @-R15
	emit(0x4F13)                     // STC.L GBR, @-R15
	emit(0x2F16)                     // MOV.L R1, @-R15
	emit(0x2F26)                     // MOV.L R2, @-R15
	emit(0x2F36)                     // MOV.L R3, @-R15
	emit(0x2F46)                     // MOV.L R4, @-R15
	emit(0x2F56)                     // MOV.L R5, @-R15
	emit(0x2F66)                     // MOV.L R6, @-R15
	emit(0x2F76)                     // MOV.L R7, @-R15
	emit(0xD100 | (movLDisp & 0xFF)) // MOV.L @(disp,PC),R1
	emit(0x4008)                     // SHLL2 R0
	emit(0x001E)                     // MOV.L @(R0,R1), R0
	emit(0x400B)                     // JSR @R0
	emit(0x0009)                     // NOP
	emit(0x67F6)                     // MOV.L @R15+, R7
	emit(0x66F6)                     // MOV.L @R15+, R6
	emit(0x65F6)                     // MOV.L @R15+, R5
	emit(0x64F6)                     // MOV.L @R15+, R4
	emit(0x63F6)                     // MOV.L @R15+, R3
	emit(0x62F6)                     // MOV.L @R15+, R2
	emit(0x61F6)                     // MOV.L @R15+, R1
	emit(0x4F17)                     // LDC.L @R15+, GBR
	emit(0x4F26)                     // LDS.L @R15+, PR
	emit(0x60F6)                     // MOV.L @R15+, R0
	emit(0x002B)                     // RTE
	emit(0x0009)                     // NOP
	h.bus.writeWramHU32(d+constOffFromBase, uint32(0x06000000)+wramHUIntTable)
}

// populateVBRTable fills the 128-entry VBR exception/interrupt
// vector table at $06000000-$060001FF. Default everywhere is the
// 4-byte RTE+NOP stub at $06000400 (any exception just returns).
// SCU interrupt vectors $40-$5F are overwritten with pointers to
// their per-vector dispatch stubs in $06000410-$0600068F.
//
// The table is 128 entries, not 256: SH-2 reserves vectors 0-127.
// $06000200+ is BIOS data (per-disc System ID partial cache, etc.)
// - leaving that region alone so populateDataTables' writes to
// $0254, $0260, $0300+ etc. aren't clobbered.
func (h *HLEBIOS) populateVBRTable() {
	defaultAddr := uint32(0x06000000) + wramHDefaultRTE
	for i := uint32(0); i < 128; i++ {
		h.bus.writeWramHU32(i*4, defaultAddr)
	}
	for vec := uint32(0x40); vec <= 0x5F; vec++ {
		stubAddr := uint32(0x06000000) + wramHIntStubBase + (vec-0x40)*hleIntStubStride
		h.bus.writeWramHU32(vec*4, stubAddr)
	}
}

// populateIntDefaultTable fills the per-vector interrupt default-handler
// table at BIOS-ROM $0600, read by SYS_SETSINT when the caller passes
// handler=0 (see docs/bios/system_services.md "$06000784 - SYS_SETSINT").
// SCU vectors $40-$5F default to their dispatcher trampoline; $4E/$4F and
// all other entries default to the RTE;NOP no-op.
func (h *HLEBIOS) populateIntDefaultTable() {
	noop := uint32(0x06000000) + wramHDefaultRTE
	for idx := uint32(0); idx < 128; idx++ {
		def := noop
		if idx >= 0x40 && idx <= 0x5F && idx != 0x4E && idx != 0x4F {
			def = uint32(0x06000000) + wramHIntStubBase + (idx-0x40)*hleIntStubStride
		}
		off := wramHIntDefaultTable + idx*4
		h.bus.bios[off+0] = byte(def >> 24)
		h.bus.bios[off+1] = byte(def >> 16)
		h.bus.bios[off+2] = byte(def >> 8)
		h.bus.bios[off+3] = byte(def)
	}
}

// writeWord stores a big-endian 16-bit value at the given Work-RAM-H
// offset. Used by installIntStubs to lay down SH-2 instruction
// encodings.
func writeWord(b *Bus, off uint32, v uint16) {
	b.wramH[off] = uint8(v >> 8)
	b.wramH[off+1] = uint8(v)
}

// installNoopHandler writes the 2-instruction RTS+NOP stub at
// WRAM-H offset $083C. The address $0600083C is the real-BIOS
// "default no-op handler" used as the value for every unused SYS
// dispatch slot and every SCU vector that doesn't have an explicit
// handler. Slot values get read back as 16-bit halves by game
// code; the $0600 high half is what the game treats as a valid
// upper-pointer byte. A magic-address sentinel ($A0000020) puts
// $A000 in the high half - a different (negative) sign-extension
// that breaks pointer arithmetic in code that reinterprets slots
// as data.
func (h *HLEBIOS) installNoopHandler() {
	writeWord(h.bus, wramHNoopHandler+0, 0x000B) // RTS
	writeWord(h.bus, wramHNoopHandler+2, 0x0009) // NOP (delay slot)
}

// installVirtualBIOS allocates a 512 KB byte array and wires it to
// the bus's BIOS slot, then populates the BIOS ROM public routine
// table at $0005D8-$0005FC with magic-address pointers. This lets
// disc IP code use the legacy
//
//	`R2 = mem.W[$05E4]; R2 = mem.L[R2]; JSR @R2`
//
// pattern (sub_50EA family) without any real BIOS loaded: the
// pointer at $05E4 reads as a magic address, the SH-2 fetches at
// that magic address, and CPU.HLEHook dispatches to the Go
// implementation.
//
// Slots that don't have a Go implementation yet are filled with
// hleSentinel so calls through them are a clean no-op RTS.
func (h *HLEBIOS) installVirtualBIOS() {
	h.bus.bios = make([]byte, biosSize)
	writeU32BE := func(off uint32, v uint32) {
		h.bus.bios[off] = uint8(v >> 24)
		h.bus.bios[off+1] = uint8(v >> 16)
		h.bus.bios[off+2] = uint8(v >> 8)
		h.bus.bios[off+3] = uint8(v)
	}
	// SH-2 reset vector longwords at BIOS-ROM offsets $00 and $04.
	//
	// Reset-PC vector -> hleSlaveInit magic address ($A00000F0).
	// Reset-SP vector -> $06002000 (matches captured real-BIOS
	// handoff slave R15).
	//
	// slave.Reset (called from smpc.cmdSSHON via the slaveReset
	// callback wired in emulator.go) runs LoadResetVectors, which
	// reads PC from mem.L[$00000000] and R15 from mem.L[$00000004].
	// With PC set directly to the magic address, the slave's first
	// instruction fetch lands in the HLE trap range; CPU.HLEHook
	// fires the slave-init Go service, and PC := PR jumps the
	// slave to wherever the service set PR (game's slave entry
	// from mem.L[$06000250], or the halt loop at $0000020C).
	//
	// Real BIOS holds $20000200 here (its own reset code) - games
	// that read mem.L[$0] as data would see a different value
	// under HLE. No known game depends on the BIOS reset vector
	// value, so the divergence is harmless.
	writeU32BE(0x0000, hleSlaveInit)
	writeU32BE(0x0004, 0x06002000)

	// Slave halt loop in BIOS ROM at $0000020C. The slave-init service
	// SetPRs the slave to $2000020C (cache-through P0 mirror) when the
	// game hasn't installed an entry at $06000250. Slave fetches BRA -2
	// here and halts cleanly - quiescent state for FRT input-capture /
	// other IRQs to fire and run their game-installed handlers, with
	// the RTE landing the slave back at the halt. Real BIOS does the
	// same job at WRAM-H $06000646 (Phase 4 copy of BIOS $00000C46);
	// HLE puts it directly in BIOS ROM since we don't do that copy
	// and any WRAM-H slot in the $06000220-$06000AFF copy range would
	// collide with data the game reads.
	h.bus.bios[0x20C] = 0xAF
	h.bus.bios[0x20D] = 0xFE // BRA -2 (BRA self)
	h.bus.bios[0x20E] = 0x00
	h.bus.bios[0x20F] = 0x09 // NOP (delay slot, unused)
	// BIOS-ROM public routine table at $0005D8-$0005FC. All entries
	// are sentinels: the IP is the only known caller of these slots and
	// HLE skips the IP-side security screen, so the routines do not
	// need real implementations. See docs/bios/system_services.md and docs/bios/rom_layout.md
	// "BIOS ROM Public Routine Table" for per-slot purpose.
	writeU32BE(0x05D8, hleSentinel) // sub_1D7C - hw register init
	writeU32BE(0x05E4, hleSentinel) // sub_50EA - security-screen font upload
	writeU32BE(0x05E8, hleSentinel) // sub_511C - security-screen text drawer
	writeU32BE(0x05EC, hleSentinel) // sub_5208 - soft reset entry
	writeU32BE(0x05F8, hleSentinel) // sub_1F04 - LZSS decompressor
}

// biosPointerSlotTargets maps each WRAM-H BIOS-pointer slot to the
// magic address its corresponding Go implementation (or warning
// handler) lives at. Slots not in this map fall back to hleSentinel.
// See docs/bios/system_services.md "BIOS-Published WRAM-H
// Function-Pointer Slots" for the source-of-truth audit.
var biosPointerSlotTargets = map[uint32]uint32{
	0x234: hleBiosFill,           // sub_02AC fill
	0x238: hleBiosCopy,           // sub_02BC copy
	0x268: hleBiosWarnRegionInit, // sub_1A18 (warn)
	0x26C: hleBiosWarnCDBlock,    // CD-block helper
	0x270: hleBiosWarnCDBlock,
	0x274: hleBiosWarnCDBlock,
	0x280: hleBiosByteExpand,  // $06000810 byte-expand fill
	0x284: hleBiosSub1C90Load, // sub_1C90 (Go shortcut: load game from disc)
	0x288: hleBiosWarnCDBlock,
	0x28C: hleBiosWarnCDBlock,
	0x298: hleBiosWarnCDBlock,
	0x29C: hleBiosWarnCDBlock,
	0x2A0: hleBiosWarnGBRZero, // sub_1800 (warn)
	0x2C4: hleSentinel,        // empty WRAM-H code area
	0x2C8: hleBiosWarnCDBlock,
	0x2CC: hleBiosWarnCDBlock,
	0x2D0: hleBiosWarnCDBlock,
	0x2D4: hleBiosWarnCDBlock,
	0x2D8: hleBiosWarnCDBlock,
	0x2DC: hleBiosWarnCDBlock,
	0x328: hleBiosWarnSMPCInit, // sub_04C8 (warn)
	0x32C: hleBiosWarnGBRZero,  // sub_1800 (second ref)
	0x34C: hleBiosScuISTClear,  // $06000664
}
