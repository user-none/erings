# Saturn BIOS Reference

This directory documents the Sega Saturn BIOS ROM image and the
runtime state it produces: the code, data, function-pointer tables,
and Work-RAM regions the BIOS exposes to disc-supplied code.

## File index

### Boot and handoff

| File | Scope |
|---|---|
| [identification.md](identification.md) | BIOS version strings, region differences in the public image. |
| [rom_layout.md](rom_layout.md) | ROM-side address map: vector table, public routine table, compressed bodies, BIOS-resident routines. |
| [boot_sequence.md](boot_sequence.md) | Power-on through handoff: Phases 1-6, system-code path at `$001120`, boot state machine, NMI handlers. |
| [handoff_state.md](handoff_state.md) | Per-subsystem state at the moment the BIOS hands off to the disc's IP. Captured-dump observations. |
| [ip_bin.md](ip_bin.md) | IP.BIN structure, System ID layout, BIOS->disc handoff convention. |
| [slave_sh2_init.md](slave_sh2_init.md) | Slave SH-2 bring-up via SMPC SSH_ON; observed patterns. |
| [security_check.md](security_check.md) | Disc security check: CD-block disc authentication (`$E0`/`$E1`), the boot disc-validation state machine, and IP.BIN header / region validation. |

### Subsystem interfaces

| File | Scope |
|---|---|
| [scu_interrupt_handling.md](scu_interrupt_handling.md) | SCU interrupt VBR table layout in WRAM-H, trampoline mechanism, dispatcher. |
| [cd_block_interface.md](cd_block_interface.md) | CD-block register surface, command-code reference, BIOS boot command sequence, sub_2F48 chain. |
| [bios_decompression.md](bios_decompression.md) | sub_1F04 LZSS format reference, compressed bodies in the ROM, load sites. |

### Services exposed to disc/game code

| File | Scope |
|---|---|
| [system_services.md](system_services.md) | SYS_* dispatch table at `$06000300`, BIOS-published WRAM-H pointer slots, BIOS-ROM public routine table at `$0005D8`, per-routine bodies. |
| [peripheral_driver.md](peripheral_driver.md) | Decompressed peripheral / Backup-Memory driver disassembly: per-slot function bodies plus shared sub-routines. |
| [backup_library.md](backup_library.md) | BUP_* function set + return codes (SDK reference layer that runs on top of the driver). |

### Boot-screen content

| File | Scope |
|---|---|
| [bios_font.md](bios_font.md) | 8x16 ISO-8859-1 bitmap font sub_50EA uploads, plus font / graphics data references. |
| [text_drawers.md](text_drawers.md) | sub_511C and sub_50A0 - the security-screen text painting routines. |
| [boot_library.md](boot_library.md) | Boot library at `$007000`: Saturn logo / disc-check animation only (NOT the apps). |
| [system_applications.md](system_applications.md) | SEGA PLAYER multimedia shell at `$040000`: CD player, CD+G, Video-CD, System Settings / Memory Manager. The `$040400` module directory, the boot dispatch, and the load mechanism. |

### Companion docs

- [saturn_boot_process.md](saturn_boot_process.md) - high-level Saturn boot overview (not BIOS-image-specific).
