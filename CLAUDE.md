This is the erings Sega Saturn emulator


# Reading Documentation

Use python calling pdftotext for parsing and filtering pdf documentation. Do
not put # comments in the one off python scripts used for processing data. Also
use python for filtering log entries and searching instead of compound bash
commands. The pdf documentation files are considered authoritative.


# Clean room

This is intended to be a clean room implementation and should be based on documentation
and our own tracing of data. We should not to look at other emulators code and copy what
they do. Again, we're striving for a clean room coded emulator.


# go

Use `go env GOMODCACHE` to determine the go cache location whenever it's required
to look at any modules.

If a go file fails to read run `gofmt` on the file and try reading again.

`gofmt` should be run regularly during or after finishing the change loop where it makes the most sense.
`go vet` should also be used regularly.

Compile-time interface check should not be added to code.


# Additional resources

If a Readme exists, it should be read when a new context is started for
additional information about the project


# Custom Scripts

Writing and then running scripts from disk should be avoided. For example but not limited to
only this list:

- Bash scripts
- Writing a bash script that uses `cat` and `EOF`
- `python`
- `python3`

If a script is needed it should be python3, and run with `-c` or using EOF type piping.
If a script needs to be reused you can ask and justify why it must be written to disk
and run multiple times. I will tell you where to place that single script.

Keep the scripts simple so they can be easily audited. Auditing takes time so it likely
shouldn't be the go to in all the time. Bash scripts should avoided because they are not
as powerful as Python and they are harder to audit.


# Temporary files

You are never allowed to write to /tmp or any other temporary location under any circumstances.
This includes changes to an application to have it write to a log file in /tmp.


# Non-ASCII Characters

Non-ASCII characters shouldn't be used in .md documentation or any source code
files. It is fine to use emoji for things like check marks or X's in tracking
and proposal documents. The only exception is in .md documentation files when
they are font mapping files. These will and should have unicode since it's a font
representation file.


# Temporary debug

Temporary debug added to code while researching an issue should use the following prefix
`[DEBUG][<COMPONET>]` Where `<COMPONET>` is the component being debugged. For example with
an emulation it should be the chip that's being investigated. Or if it's the emulation loop
it should state as such.

Using this prefix format we can easily identify debug that needs to be removed later and
we can trace components for a multi component system. Which will likely need to debug
multiple components to research and issue.

