# Contributing

## Documentation

There is as much documentation about the Saturn as could be
found in the docs dir. There are a large number of official Sega PDF's present
which should be considered the source of truth.

The md files are either derived from the PDFs or contain additional information
that augments the PDFs. Such as correlating related information that's found
across several PDFs. While these should be correct they are not official and
could contain errors. Use them but always defer to the official PDFs.

### Notes and documentation deviation

The docs/emulation_notes.md is an important file because it contains
information about where hardware deviates from the official documentation.
It also contains game specific information that's not obvious. As well
as information about limits to the documentation were assumption or
guesses are needed and what was found from testing. For example
how games are dependent on accurate CD block timing.

### Guesses

The documentation doesn't detail ever single thing so sometimes
educated guesses need to be made. An example is VDP1 command cycles.
There isn't a command timing table like you see with a CPU. The
cycle count for VDP1 is an educated guess. This happens and should
be documented as a guess and not documentation grounded.

## AI usage

erings is primarily developed with the help of AI. However,
strict management of the AI is absolutely necessary. These
guidelines need to be followed for effective use with this
emulator (and others).

### Ground with documentation

There is extensive documentation in the docs dir which the
AI needs to be told to reference. Not once, but repeatedly
and before any code changes are made.

The AI will hallucinate how the hardware works and come up
with some pretty crazy things that will break a lot of things.
Before any code changes you must tell it to review what it's
proposing against the documentation and validate the changes
match.

### Managing the AI

An AI needs strict and concentrated management otherwise it
will go off the rail. It can't design properly and it will
arbitrarily try to change the design of the emulator in
very negative and broken ways if left to do whatever it wants.

### Code Review

Code needs to thoroughly reviewed. The AI will hack together
a change without regard for the rest of the system. Treat the
AI like a junior dev and scrutinize it's work. It can do
very good work but it can also make mistakes.

## Change Scope

Changes need to be scoped to what they're actually trying
to accomplish. For example, I'm looking at you AI, if it's
for a bug fix, functions prototypes, variable names shouldn't
change if it's not legitimately needed for the change.

Changes shouldn't come with additional refactoring if it's
not actually needed. Even if it's a refactoring change it
should still be limited and broken into phases.

### PRs

The author of the PR is responsible for understanding the changes.
If questioned the author needs to be able to defend the change
accurately.

## Code Comments

Comment the code and reference PDF documentation when possible.

## Testing

Testing is very difficult because it involves playing through
games in order to look for and find regressions. Especially
around timing bugs.

Use and create unit tests but be aware they're only useful
for testing changes for regressions in unrelated things.

### Test Game List

These are the games typically used for cursory testing.

- NiGHTS
- Waku Waku 7
- Burning Rangers
- Legend of Oasis
- Night Warriors'
- Bulk Slash

Each of these games helps to exercise a different parts
of the emulator. It's not exhaustive and it's not required
to test with these games.

## Game Bugs and Compatibility

There isn't a game compatibility database at this time.
It's fine to open tickets for game bugs but don't expect
them to be fixed anytime soon.


## Internal Development Tools

### `cmd/debug`

A command line launcher that should be used
for development. It is decoupled from the ui and outputs
additional information to the console. Such as frame rate
and gameplay frame rate.

It also includes a stall watchdog that will detect when the emulator
has entered a stalled state (such as an infinite loop). It will
dump every goroutine's stack to stderr and report says exactly what
caused the stall.

The `-cpuprofile` allows profiling the application using
the standard go profiling system.

Movement keys are:

- W (up)
- S (down)
- A (left)
- D (right)
- N (left shoulder)
- M (right shoulder)
- J (A)
- K (B)
- L (C)
- U (X)
- I (Y)
- O (Z)

Additional keys:

- Enter (start)
- 0 (pause emulation)
- 9 (dump top 20 PC histogram)
- 8 (dump current memory to `dump-YYYYMMDD-HHMMSS-mmm` directory)

### `sh2.TraceFunc`

The SH-2 support a hook that allows tracing all SH-2 execution.
This is used by the PC histogram feature of the `cmd/debug` launcher.
To use this function for other purposes with that launcher, the current
hooked in function needs to be changed or replaced for one off testing.
Changes to this histogram capture should not be committed.

### `utils/emudbg/disasm`

A simple SH-2 disassembler which can be used to investigate game
execution.

