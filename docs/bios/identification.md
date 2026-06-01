# BIOS Identification

The Saturn BIOS ROM is a 512 KB image. The version of the image
in use can be identified by a 16-byte ASCII string at BIOS offset
`$000800`, which the BIOS copies to Work-RAM-H at `$06000200`
during Phase 4 init.

The string is fixed-width and prefixed with `BTR_`. The remaining
bytes encode the firmware version and build date (`YYMMDD`), plus
a region indicator in the USA image. The leading `BTR_` is shared
by all observed images.

## Known variants

| Region | Version string (offset `$000800`) | Build date | Image size |
|---|---|---|---|
| USA | `BTR_1.000U941115` | 1994-11-15 | 524 288 bytes (512 KB) |
| JP  | `BTR_1.0191941228` | 1994-12-28 | 524 288 bytes (512 KB) |

The USA string ends with the region letter `U` and the 6-digit build
date `941115`. The JP string carries no explicit region letter; the
digits after `BTR_1.01` are interpretable as a revision suffix
followed by the same `YYMMDD` build date (`941228`).

The font, peripheral driver, and BIOS-ROM public routine table
addresses are identical between the two variants (see
[bios_font.md](bios_font.md), [peripheral_driver.md](peripheral_driver.md),
[rom_layout.md](rom_layout.md)). The boot library, logo / menu
graphics, CD-block firmware, and security-check code regions differ
between regions; only the version string at `$000800` is required
to distinguish images at runtime.

## SHA-256 of the published images

| Image | SHA-256 |
|---|---|
| USA `BTR_1.000U941115` | `96e106f740ab448cf89f0dd49dfbac7fe5391cb6bd6e14ad5e3061c13330266f` |
| JP  `BTR_1.0191941228` | `dcfef4b99605f872b6c3b6d05c045385cdea3d1b702906a0ed930df7bcb7deac` |
