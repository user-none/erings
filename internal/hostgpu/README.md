# hostgpu: the compile stage, the IR, and the backends

hostgpu is the host side of the GPU renderer. It takes the compute
packages' embedded source, compiles it to an intermediate
representation, and has a backend lower the IR to its compute
shaders and run them. The renderer in core uses package hostgpu and
opens one of the platform's backends, by name, through
hostgpu/backend. The
language-level contract, the Go
the compute functions are written in and its semantics, is
core/compute/README.md; this file specifies the IR those semantics
travel through so that every backend emits from one resolved form,
the conventions each emitter follows, and the device contract the
backends implement.

Pipeline:

    embedded .go source of core/compute tree
      -> parse (go/parser) + type-check (go/types) + subset validation
      -> lower to IR (compile)
      -> emit per backend: MSL source (compile/msl) / HLSL source (compile/hlsl) / SPIR-V (compile/spirv)
      -> backend compiles: the Metal runtime compiler / DXC ahead of time / the Vulkan driver

Everything runs at renderer initialization inside the emulator
process, except the DX12 backend's compile: the HLSL is emitted and
compiled to signed DXIL by a build step (make dxil, which runs
utils/dxil_gen), a Windows binary embeds the blobs in dx12/dxil/,
and the backend at initialization lowers the
same sources
for the entry layouts and takes each entry's blob by name. dx12/dxil/
README.md describes that step.

Packages:

- compile: the loader, the validator, the IR, and the lowering.
- compile/msl, compile/hlsl, and compile/spirv: the emitters.
- conformance: the conformance vectors and the Go reference runs the
  backend tests compare against.
- metal (macOS), dx12 (Windows), and vulkan (Linux and Windows): the
  backends.
- devicetest: the device and conformance tests every backend runs on
  its own opened device, written once over the hostgpu interfaces. A
  backend's test files open its device and call them.
- backend: lists the backends the build can open on the platform, the
  platform's default first (Names), and opens one by name and builds
  the sources on it (Open). macOS lists Metal and Linux Vulkan.
  Windows lists Direct3D 12, the default, and Vulkan; a Windows build
  without the embedded DXIL lists Vulkan alone. The renderer option
  offers these names and Software. A backend that fails to open or to
  run falls back to the software renderer, never to another backend.


## Design properties

- The IR is a typed, structured statement tree, a resolved AST rather
  than a compiler IR. No SSA, no basic blocks, no control-flow graph.
  The subset has only structured control flow and every target is
  structured, so nothing else is needed.
- No optimization passes. Emitters are printers; optimization belongs
  to the downstream compilers (the Metal compiler, DXC). The only
  whole-program analyses are the validations the subset assigns to
  the compile stage: acyclic call graph, import closure, read-only
  package tables, struct layout, the params row read-only, and the
  reference aliasing rule.
- Semantics are the subset's semantics section, by reference. The
  lowering resolves everything Go leaves implicit; the emitters never
  interpret Go, only the IR.
- All references are by index: functions, locals, params-struct
  fields, and buffers are numbered during lowering. Emitters
  synthesize names, so source identifiers never reach shader text
  and cannot collide with target keywords.
- Every expression node carries its resolved type. There are no
  implicit conversions anywhere in the IR.


## Type system

Scalars: Bool, I8, I16, I32, I64, U8, U16, U32, U64, F32.

Composites:

- Array: element type + constant length of one or more. Stride = element size
  rounded to element alignment.
- Struct: ordered fields, each with a computed byte offset (see
  Layout). No embedding, no reordering.

Buffers (opaque handles, never values):

- ByteBuffer and WordBuffer, each instance carrying its size constant
  (from the parent package's named buffer types) and a writable flag.
  Byte buffers are addressed by byte through the renderer operations,
  the Go source's accessors; word buffers are arrays of U32, which the Go
  source indexes and the lowering turns into the Load and Store
  helpers (w[i] = v, compound assignments and increments included,
  with the index evaluated once).
- A dynamic buffer is a named []uint32 (a word buffer) or []byte (a
  byte buffer) in the parent Go package, passed by value. Size is not
  a constant; the backend supplies the bound buffer's word count with
  the binding, the entry's shell scales it by four for a byte buffer,
  and every function below the shell receives the buffer's length in
  its own units: words for a word buffer, bytes for a byte buffer.
  len of the buffer, converted to int32 or uint32 in the Go source,
  lowers to the Length helper, which reads that length. The count is
  a power of two, rounded up by the host where it sizes the buffer and
  checked by the backend where it binds, so the byte length is one too, and the
  helpers mask with the length minus one exactly as a fixed buffer's
  helpers mask with its size constant, so a variable-length buffer
  has the same helpers as a fixed one: the byte accessors on a byte
  buffer, the shared-word operations on a word buffer. An index past
  a word buffer is an error Go stops on; the mask keeps the device's
  access inside the buffer. The Go slice header is never uploaded.
  Allocation and growth are host operations and do not appear in the
  IR.
- A buffer group is a named struct, in any compute package, whose
  every field holds buffers: a dynamic buffer by value, a fixed
  buffer or another group by pointer, or an array of either kind of
  buffer. It has no IR type: the lowering flattens it to the buffers
  it holds, depth first in field order, expands a group parameter
  into one BufferParam local per buffer and a call argument the same
  way, and lowers a path to a buffer (a field, a nested field, a
  constant array element) to that buffer's local, so entries,
  wrappers, bindings, and emitters see only buffers. A word read from
  an array of buffers at a variable index, allowed only as the whole
  value of an assignment, lowers to a switch over the array's
  buffers into a temporary. The validator admits a group only as a
  pointer parameter.

Buffer-visible structs, the params struct and any struct read from a
buffer, may contain only bools, 32-bit and 64-bit scalars (I32, U32,
I64, U64, F32), and arrays and structs of them. A bool is laid out as
Go lays it out, one byte, and a backend without a storage bool reads
the word holding the byte and tests it against zero, so the host
uploads the Go struct's bytes as they are. 8/16-bit storage would
require optional device capabilities, so narrow types are local-only
and byte-granular data rides in byte buffers accessed through the
helpers. The compile stage rejects violations.


## Struct layout

One rule, applied identically on both sides:

- Field alignment = field size for scalars (4 or 8; only 32/64-bit
  scalars appear in laid-out structs). Array alignment = element
  alignment. Nested struct alignment = its own alignment.
- Each field's offset = the next multiple of its alignment after the
  previous field's end. No reordering.
- Struct alignment = max field alignment. Struct size = end of last
  field rounded up to struct alignment.

This is natural alignment, which for 32/64-bit-only structs is
simultaneously Go's (gc) layout, MSL's default layout, and HLSL's
structured-buffer layout; the field-type restriction above is what
makes them agree. The compile stage:

- computes every offset by this rule and stores it in the IR,
- emits field-by-field declarations, and the HLSL params loaders at
  the computed offsets, and
- verifies at lowering that the rule's offsets and size equal the Go
  compiler's for the same struct, through the gc sizes go/types
  provides. A mismatch is a compile-stage diagnostic, on which the
  renderer falls back to software.


## Program structure

    Module
      Structs   []StructType          // laid-out types, indexed
      Tables    []Table               // package-level const arrays
      Funcs     []Func                // helpers and wrappers, indexed
      Entries   []Entry               // exported entry points

    Func
      Locals    []Local               // the parameters first, then locals as encountered
      NumParams int
      Results   []Type                // multiple results allowed
      Body      Block

    Entry
      Func, Wrapper int               // the entry and its synthesized wrapper
      Span, Group bool                // the entry's form; neither set is an index entry
      Buffers   []BufferBinding       // in parameter order
      Params    []int                 // the struct parameters, in order
      Coords    int                   // trailing coords: 1 (index), 2 (span), or 3 (group)

A Table is a package-level lookup array (read-only, verified never
assigned); emitters place tables in constant address space (MSL
constant, HLSL static const).


## Statements

Ten kinds:

- Block: statement list with its own local scope.
- VarDecl: declare local (index) with initializer.
- Assign: one or more targets (locals, struct fields, array elements,
  ref params) from one or more values. Go's parallel assignment
  lowers to temporaries holding every right-hand side before the
  stores, so the only multi-target Assign left is a multi-result call
  as the single value.
- ExprStmt: call evaluated for effect.
- If: condition, then-block, optional else-block or else-if chain.
- Switch: integer or bool scrutinee, constant cases, default; no
  fallthrough. Emitters print a native switch.
- For: optional condition expression, optional post statement, body.
  A Go loop's init statement lowers into a block wrapping the loop.
  Bounds are arbitrary expressions (dynamic). A Go
  range over an integer lowers to a For on a hidden counter against
  the bound evaluated once, with the key a fresh local set from the
  counter at the top of the body.
- Break, Continue: innermost loop only.
- Return: zero or more values matching Results.


## Expressions

Node kinds:

- Const: folded go/constant value, typed.
- LocalRef: local or parameter by index. A ref parameter denotes the
  value it refers to, so there is no address or dereference node:
  Go's *p is the parameter, &x as an argument is x's access path, and
  a pointer-receiver call passes its receiver the same way.
- FieldRef: struct field access. The entry's params struct is a ref
  parameter like any other, so its fields are FieldRef over LocalRef;
  no separate params-field node exists.
- IndexRef: array element access.
- TableRef: package table element access.
- Zero: the zero value of a composite type (an uninitialized
  declaration).
- Composite: a struct or array literal as positional elements, one
  per field or element, with zero fill for those the source omits.
- Call: module function by index.
- OpCall: an operation of compute.Operations by name (see below).
- Unary: Neg, Not (bool), BitComplement.
- Binary: see operators.
- Convert: explicit scalar conversion.
- Mask: re-canonicalization of a narrow value (see below).
- LoadStruct: the struct row at a byte address of a byte buffer,
  used only by the synthesized entry wrappers to fetch a row's params
  (see Entry points). It names the row, not a copy: whether the row
  is copied into a local or bound by reference and read in place is
  each emitter's choice. The address is a multiple of the struct's
  alignment by construction of the packet.

Operators (type-driven; the emitter selects integer/float and
signed/unsigned forms from the operand type):

    Add Sub Mul Div Rem
    BitAnd BitOr BitXor AndNot Shl Shr
    Eq Ne Lt Le Gt Ge
    LogicalAnd LogicalOr

Div and Rem truncate toward zero, as Go's do: MSL and HLSL integer
division and remainder are truncating, and SPIR-V's OpSDiv and
OpSRem are, OpSRem taking the dividend's sign. A 32-bit or 64-bit
signed minimum divided by minus one is each backend's native
behavior, undefined on all three targets, and is not guaranteed to
match Go, which wraps the quotient to the minimum and the remainder
to zero. The lowering emits no guard for it (core/compute/README.md,
the division rule).

LogicalAnd/LogicalOr are short-circuit. When the right operand
contains a call, the lowering rewrites the expression into an if
statement so short-circuit is structural; a pure right operand is
emitted as the target's native && / ||. HLSL and MSL short-circuit
them. SPIR-V's logical operators take both operands evaluated, so its
emitter makes each a selection construct that evaluates the right
operand only in the branch that needs it, as a right operand the left
one guards may divide by zero or index out of range.
The if statement is emitted ahead of the statement being lowered, so
an earlier operand of the same expression that holds a call (an
earlier call argument, the left operand of a binary operator, an
earlier composite element or return value, a store's index) is
evaluated into a temporary declared ahead of it, keeping Go's
left-to-right order.


## Narrow-width normalization

The subset allows 8/16-bit locals with exact Go wrap semantics, while
targets compute in 32-bit registers. The lowering, not the emitters,
decides where masking happens:

- Narrow values are represented in 32-bit registers, sign- or
  zero-extended canonical form.
- A Mask node re-canonicalizes a value whose operation can exceed the
  declared width (add, subtract, multiply, shift left, negate,
  complement, and division, whose one overflowing case is the signed
  minimum divided by minus one, which Go wraps back to the minimum)
  at every point it is consumed: stores to narrow locals or fields,
  conversions, call arguments, results, indices, and the operands of
  comparisons, divisions, remainders, and shifts.
- Operations that cannot overflow the canonical form (BitAnd, BitOr,
  BitXor on canonical inputs) insert nothing.

Emitters therefore never reason about widths; they print what the
lowering produced. The conformance functions for wrap semantics
exercise every width at every masking point.


## Renderer operations

The operations of compute.Operations, enumerated, with fixed lowerings:

    helper                Go implementation        MSL                                    HLSL
    U8(b,a)               b[a&mask]                byte pointer                           word load + lane extract
    U16BE(b,a)            two U8, hi<<8|lo         byte pointer                           two U8, hi<<8|lo
    U32BE(b,a)            four U8 composed         byte pointer                           word extract, byte swap; four U8 off a word boundary
    PutU8/16BE/32BE       dual of the reads        byte pointer                           lane-masked atomic AND then OR on the word, a byte at a time; one store of the swapped word for an aligned U32
    Load(w,i)             w[i] (an index)          index                                  index
    Store(w,i,v)          w[i]=v                   index                                  index
    Length(b)             len(b) (a builtin)       the view's length                      the hidden length
    min/max (builtins)    the Go builtins          native                                 native at 32 bits, prelude functions at 64
    NextPhase()           nothing                  threadgroup_barrier(mem_device)        DeviceMemoryBarrierWithGroupSync
    Raise(w,i,v)          w[i&mask]=max(w,v)       atomic_fetch_max_explicit (relaxed)    InterlockedMax
    Add(w,i,v)            w[i&mask]+=v, prior      atomic_fetch_add_explicit (relaxed)    InterlockedAdd
    Swap(w,i,v)           w[i&mask]=v, prior       atomic_exchange_explicit (relaxed)     InterlockedExchange

The SPIR-V forms are the same operations: byte access chains into
an 8-bit block with non-private loads and stores, GLSL.std.450 min
and max, OpControlBarrier with workgroup execution and memory scopes
under acquire and release semantics over uniform memory with the
availability and visibility operations, and OpAtomicUMax,
OpAtomicIAdd, and OpAtomicExchange at device scope, relaxed.

For the byte accessors, the shared-word operations, and NextPhase,
the Go implementation column is the parent package's software form,
in core/compute/software.go, which the software renderer runs and
which is the single-thread reference; the lowering resolves each by
name and never compiles the body. The lowering holds each such call
to the operation as the interface takes it, the operand count and
the result, since Invoke passes the operands to a backend's method
by position; the software forms themselves are the software
renderer's, held to the interface by a test in core/compute. Load,
Store, Length, min, and max name no parent function: they are the
lowering of Go's own index, len, min, and max, looked up by name.

The operations are the methods of compute.Operations, a generic
interface over the backend's buffer and value operand types, which
each emitter implements: over a buffer's local and source text for
MSL and HLSL, over a buffer's local and an instruction id for
SPIR-V. The compile stage states nothing about an operation itself:
it reads the set from the interface by reflection at initialization
(compile.Operation: the name, whether the first parameter is the
buffer, the operands after it, and whether there is a result), and
compile.Invoke calls the method named for an IR OpCall node on the
operands the emitter evaluated. An emitter lacking an operation does
not build. An operation with no result is a statement: a source
emitter prints it as a line where it stands, and the lowering keeps
such an operation out of a loop's post, which source emitters print
inline.

Notes:

- NextPhase is a statement, allowed only directly in the body of a
  group entry, outside any if, switch, loop, or nested block
  (validated), so every thread of the workgroup reaches each one: a
  barrier in control flow some threads skip is undefined on every
  backend. It is not pure: the lowering keeps its order among the
  buffer stores around it. A group entry is never called
  (validated), so its NextPhase runs only in its own group dispatch.
- Raise, Add, and Swap are word buffer helpers, masked like Store,
  lowered on fixed or variable-length buffers alike (the parent
  declares them on the round state's variable-length regions, one
  operation each), and are not pure: each
  writes its word, so the lowering keeps its order among the buffer
  accesses around it. Raise has no result. Add and Swap return
  the prior word, which a compute function uses for addressing or
  stores as a link and never feeds to another Add: the Metal
  compiler coalesces a SIMD group's atomic adds and reads the
  coalesced result as uniform across the lanes when it is atomically
  added again. The MSL lowering reinterprets the buffer's word
  pointer as an atomic pointer at the masked index.
- The address mask is the buffer's size constant, emitted inline so
  downstream compilers see a constant, or for a variable-length
  buffer its length minus one, in bytes for a byte buffer and in
  words for a word buffer.
- Byte writes on HLSL touch a 32-bit word, since a raw buffer stores
  words. The lowering masks the address, then clears the byte's lanes
  with an atomic AND and sets them with an atomic OR, so threads
  writing distinct bytes of one word keep each other's bytes. A
  16-bit read or write is two byte operations, not one word
  operation with the two bytes swapped by shifts and masks: that
  form loads and stores wrong values on NVIDIA's DXIL compiler
  inside the VDP1 draw kernels, the stored value replaced by another
  live value, while it passes in the conformance kernels, and the
  byte forms carry no swap. A 32-bit write on a word boundary is one
  store of the swapped word, since it writes every lane, and one off
  a word boundary is two 16-bit writes. Two threads
  writing the same byte in one dispatch is the collision the compute
  functions exclude by construction (one writer per pixel in the
  write pass, rows and words taken by stride, one thread per pixel in
  the resolve), and a read of a byte another thread is writing in the
  same dispatch is excluded the same way, so the transient between
  the AND and the OR is never observed.
- The word extract for U16BE/U32BE assumes the byte buffer's logical
  content is big-endian bytes packed little-endian into words by the
  upload path; the upload convention and the extract are defined
  together and locked by a conformance function.


## Lowering from Go (normalization)

- go/types resolves every identifier and expression type; untyped
  constants are folded and must fit their context type (subset rule).
- Methods become functions with the receiver as an explicit first
  parameter; pointer receivers and pointer parameters become ref
  parameters.
- Named results become ordinary locals; bare returns become explicit.
- Multiple results lower to multiple IR results; emitters realize
  them as out parameters (MSL thread references, HLSL out
  parameters), invisible at the IR level.
- Ref parameters (from Go pointers) lower to MSL thread-address
  references and HLSL inout parameters. Refs never escape (subset: no
  storing, no returning), so no other storage class is ever needed.
- Evaluation-order temporaries are introduced wherever the target's
  order is unspecified and the Go order is observable: when one
  statement holds more than one module call or buffer helper, every
  such call but the last is evaluated into a temporary in Go's
  left-to-right order. An assignment's target path counts with its
  value and is evaluated first, as Go evaluates index operands, so the
  calls in the path are spilled ahead of the value's. An assignment
  with several targets follows Go's two phases: every target's index
  operands are evaluated into temporaries, then the values, then the
  stores in order, so a store changes no later target's place
  (`i, x[i] = 1, 2` sets x[0]). An operand keeps
  its shape around the temporaries, so a reference argument's path to
  its target stays a path with the calls in its indexes spilled. The
  pure integer operations (the min and max builtins) have no
  effects and are left in place. Plain reads are not ordered against
  the calls: the validator rejects a statement that reads, outside a
  call, storage the call may write (checkCallOrder), so where a plain
  read falls among the calls is not observable.
- A loop condition that needs temporaries (a short-circuit rewrite or
  hoisted calls) moves into the body as a conditional break; a post
  statement that lowers to more than one statement is inlined at the
  end of the body and before each continue of that loop.
- Structs are numbered in declaration order per package, a struct
  another struct holds taking the next number when the holder is laid
  out if it has none yet. Tables and functions are numbered in
  declaration order. The IR of a package does not depend on the order
  functions are lowered in.
- len/cap on fixed arrays fold to constants. min and max lower to the
  runtime ops of their operand type, folded pairwise from the left.


## Entry points, wrappers, and dispatch

- The entry convention is the subset's: buffers, then at most one
  params struct and one row-state struct by pointer, then the
  coordinates, each an int32 or a uint32, which the wrapper converts
  to. Three coordinate forms exist. A span entry ends in two
  coordinates (x0, x1): it renders the dots [x0, x1) of the row
  its params describe, which is how the software schedule calls it
  for the beam's chunk. An index entry ends in one coordinate and
  handles one element. A group entry ends in three coordinates:
  the thread's index in its group, the group's size, and the group's
  index. A group is a row of the grid, its threads the workgroups
  the backend dispatches across the row, and the group's params row
  is the group's index. The barrier orders one workgroup, so an
  entry dispatched one workgroup across, a grid one thread across on
  every backend, may order its phases with it.
- The lowering synthesizes a wrapper function per entry point in the
  IR, so the wrapper's logic exists once for every backend. The
  wrapper takes the entry's buffers, the params buffer, the dispatch
  record fields as scalars, and the thread coordinates (gx, gy, and a
  third word: for a group entry the shell passes the thread's index
  across the row as gx, the row as gy, and the row's thread count,
  the workgroup size by the workgroups across, as the third), and:
  1. returns when gy >= boundY, and when gx >= boundX for an index
     entry or gx * segment >= boundX for a span entry;
  2. loads the params struct from the params buffer at paramsOffset
     + gy * paramsStride (a LoadStruct node, an IR-only expression
     with a per-backend lowering, never written by authors), so a
     per-row params array such as the frame packet's records is
     addressed by row with the struct's offset within the record and
     the record stride; a single params struct uses stride 0;
  3. zero-initializes the row-state struct when the entry takes one;
  4. calls the entry with, for a span entry, x0 = gx * segment and
     x1 = min(x0 + segment, boundX), for an index entry gx, and for a
     group entry the thread's index, the group's size, and the
     group's index.
- Emitters print only the entry point shell a language forces: the
  shell's declaration, the buffer binding attributes, the thread id
  intrinsics the language provides, the record binding, and a call
  into the wrapper. The shell carries no logic.
- The dispatch record is a fixed 20-byte block of five U32 {boundX,
  boundY, paramsOffset, paramsStride, segment}, delivered through each
  API's direct-constant path: MSL setBytes, DX12 root constants,
  Vulkan push constants. A backend that needs words of its own for a
  dispatch declares them after the record in its own shell and fills
  them in its own device, and no other backend sees them (see the
  binding model). The renderer covers an index or span grid of any
  width with one Dispatch call and never sees how a backend issues
  it. A group entry's grid is never split across dispatches, and a
  group grid wider than one dispatch is a host error (ErrGroupWide).
  Rows are never split either, so on a backend whose API bounds the
  rows of one dispatch a grid with more is a host error (ErrGridRows).
- A span entry's segment is the unit one thread takes whole, set by
  the renderer per entry: a cell at the scale for the cell spans, so
  the pattern name cache serves a whole cell, the whole row for the
  gradation, whose blur carries across the row, and one dot for the
  other spans, which share nothing between dots. The renderer's grid
  across is the units over boundX.
- The dots a thread takes past the unit are the backend's. A backend
  may give each thread a whole number k of units: it dispatches the
  record with segment times k and the grid across as boundX over the
  new segment, rounded up. The span contract makes the result the
  same for any segment, and k trades repeated per-thread prologue
  work against occupancy, which is the device's. k is chosen from the
  entry's form and the unit alone, never from the entry's name. Metal,
  DX12, and Vulkan each dispatch at k = 1.
- A conformance entry exercises the wrapper contract itself, per-row
  params addressing and the segment range and guards, so each backend
  proves it against the Go execution like any other rule. A second
  entry runs the same rule at twice the segment, the record a backend
  at k = 2 sends.
- Workgroup size is each backend's choice, made by its emitter from
  the entry's form and carried by the backend's pipeline: the MSL
  EntryPoint for Metal's dispatch geometry, the HLSL Layout for the
  numthreads the blob holds and the DX12 device's rounding, and the
  SPIR-V EntryPoint for the LocalSize. The IR names no workgroup. A
  backend dispatches a grid of threads across in whole workgroups of
  its own size, so the renderer dispatches a grid of threads across
  by rows and never names a workgroup. A group entry's grid is a row
  per group by the threads across the device sizes for the group's
  work (Device.GroupThreads), rounded up to whole workgroups, and the
  group's size the entry receives is the threads the backend ran. A
  group entry is written so its result is the same for any size, so
  how wide a group runs is each backend's choice for its device. The
  renderer states the dispatch's load (GroupLoad): its op count, and
  the chunks of its largest op and of all its ops together, a chunk
  being the piece of an op's work one thread takes (an erase's line, a
  drawn op's run of GenChunk steps of a line). Every backend sizes the
  row with GroupWidth from the load, its group workgroup, the widest
  row one of its dispatches issues, and the threads its device runs
  at once where the device reports them: the largest op's chunks when
  every row at that width fits the device at once, and otherwise the
  geometric mean of the average and the largest op's chunks, which
  holds the largest op's loop count and the threads past the chunks to
  the same factor for any mix of op sizes. DX12 reports its lanes
  (D3D12_FEATURE_DATA_D3D12_OPTIONS1 TotalLaneCount); Metal and core
  Vulkan report none.
- The renderer names the host side through hostgpu and
  hostgpu/backend: Source, the Program built from the sources with its
  pipelines by entry point name, Device, Buffer, Commands, the
  Dispatch record, GroupLoad, and the backend package's Names and
  Open. The compile stage and its emitters are behind hostgpu.Build,
  which the backend package calls after opening the named backend.


## Buffer binding model

Binding index = entry-point parameter position, 0-based, in
declaration order, a buffer group parameter counting one position
per buffer it holds, with the params struct's buffer taking the index after
the last data buffer:

- MSL: [[buffer(i)]] on each; the dispatch record via setBytes on the
  next free index. When any data buffer is Dynamic, one additional
  setBytes binding after the dispatch record carries uint32 word
  counts by data-buffer index. The backend fills this from the bound
  buffers; the entry shell constructs a WordView for a word buffer and
  a ByteView for a byte buffer, each a device pointer and a length,
  the ByteView's the word count by four. Ordinary functions pass
  these views unchanged. The word and byte helpers go through prelude
  view helpers that mask with the view's length minus one, so only
  those helpers index a view, and len reads the length. Fixed-buffer
  dispatches have no extra binding. Metal issues a grid of any width
  in one dispatch, and a group entry's row thread count and index come
  from the threadgroup built-ins, so the MSL shell adds no words to
  the record.
- HLSL: all buffers as UAVs u0..uN in the same order, the params
  buffer after the last data buffer (uniform UAV binding keeps one
  code path), RWByteAddressBuffer for byte buffers and
  RWStructuredBuffer<uint> for word buffers, each a root descriptor;
  the root constants in b0 after them: the five dispatch record
  words, then two words the DX12 device fills for itself, then the
  module's length words, one per data buffer index up to the most
  data buffers any entry with a variable-length buffer binds, so
  every entry of a module declares one constant count and the device
  pushes one block. The first of the device's words is a group
  entry's row thread count, which no built-in supplies, and the
  second is the first thread of a piece of a split index or span
  dispatch: DX12 bounds the thread groups one dispatch may issue per
  dimension at 65535, and the device issues a wider grid in pieces of
  at most that, each with its base, which the shell adds to the
  thread's x. The positions are a contract between the HLSL shell and
  the DX12 device alone (compile/hlsl/layout.go). The params buffer is
  also declared at the UAV register after the entry's buffers ahead
  of the common source, whose functions read the params row in place
  (see the HLSL emitter); an entry without params never reaches those
  reads, so its declaration is unused and outside the root signature.
  The root signature is declared
  on each entry's shell (compile/hlsl/layout.go is the one rule), the
  compiler embeds it in the blob, and the device creates it from the
  blob.
- SPIR-V: every buffer a storage buffer block at descriptor set 0,
  binding i in the same order, the params buffer after the last
  data buffer, pushed per dispatch through VK_KHR_push_descriptor;
  the push constant block after them: the five dispatch record words
  at offsets 0 to 16, then one word the Vulkan device fills for
  itself at offset 20, then, for an entry with a variable-length
  buffer, an array of word counts by data buffer binding at offset
  24. The device's word is the first thread of a piece of a split
  index or span dispatch: the device issues a grid wider than its
  maxComputeWorkGroupCount in pieces, each with its base, which the
  shell adds to the thread's x. A group entry's row thread count
  comes from the NumWorkgroups built-in. The positions are a contract
  between the SPIR-V shell and the Vulkan device alone
  (compile/spirv/emit.go).

The renderer binds by the same table it lowered from, so bindings
cannot drift from signatures.

Copies between buffers and fills of a range of words are the
device's Copy and Fill (see the device contract), not entries.


## Backend emitters

- MSL (compile/msl): a source printer. Byte buffers are device uchar
  pointers, word buffers device uint pointers. Names are synthesized
  from IR indices (S0, fn0, k0, tb0, l0, f0) with the source name in a
  trailing comment on structs, functions, and entry point shells.
  Narrow scalars print as int or uint, and Mask prints a char or
  short cast for signed types and an AND for unsigned. A signed add,
  subtract, multiply, negate, or shift left is printed on the
  unsigned type of its register width and converted back, since
  MSL's C++ base leaves signed overflow undefined while the subset
  wraps; the HLSL printer does the same, and SPIR-V defines integer
  overflow as wrapping. Arrays are
  metal::array so they copy by value and initialize by brace list.
  Structs print in dependency order with fields in declaration order
  and prototypes precede every body. The renderer operations are a
  static inline prelude taking the buffer's address mask as an argument, so
  one definition serves every buffer size; the integer helpers are
  the Metal library's min and max. Multiple results are
  trailing thread-reference out parameters assigned from temporaries
  before the return. Every operator expression is parenthesized. The entry
  point shell binds buffers by position, the params buffer at the
  next binding, the dispatch record after it, reads the grid id, and
  calls the IR wrapper with them. The MSL emitter's choice for
  LoadStruct is a copy: a typed load through a device pointer into
  the wrapper's local. Compiled by the Metal runtime compiler
  with fast-math disabled.
- HLSL (compile/hlsl): a source printer targeting Shader Model 6.0
  (int64_t) under HLSL 2021, one source per entry point holding every
  function and the entry's shell, whose kernel is named main in every
  source, compiled ahead of time by DXC with strict float math (-Gis).
  Names, narrow scalars, and Mask follow the MSL printer; Mask
  sign-extends through shifts. A signed add, subtract, multiply,
  negate, or shift left is printed on the unsigned type of its
  register width and converted back, as the MSL printer does: DXC
  marks signed arithmetic as never overflowing and folds a
  comparison such as a+b > a on that basis, while the subset wraps.
  An aggregate literal is legal only as
  an initializer, so every Composite or Zero outside a VarDecl
  initializer or an out-parameter return is hoisted into a temporary
  ahead of its statement, and so is a call with out results outside
  its statement form. A loop whose condition or post needs such a
  temporary is printed as for (bool f = false; ; f = true): the post
  at the top of every pass but the first, so a continue reaches it,
  then the condition's temporaries and a break when it is false.
  A function cannot return an array, so an array
  result is an out parameter like a multiple result. A
  variable-length buffer parameter is followed by its hidden length,
  threaded through every call. A signed 32-bit constant carries the
  L suffix, which HLSL 2021 types as int for a decimal literal; an
  unsuffixed integer is a 64-bit literal, and one left of a shift by
  a variable count takes the count's type. The compiler reads a
  literal starting with 0 as octal and types a suffixed octal or hex
  literal as uint, so zero is printed unsuffixed, which keeps a
  comparison of a signed value against it signed. A 64-bit constant
  is composed from its halves through casts rather than a literal
  suffix. HLSL has no pointers: a reference parameter is an inout
  parameter, the value copied in at the call and back at the return,
  so two overlapping references are two private copies, which the
  compile stage's aliasing rule makes exact, and a local aliasing
  part of a struct cannot be written, which the subset excludes.
  The HLSL emitter's choice for LoadStruct is a copy: a per-struct
  loader function gathering the fields at their laid-out offsets from
  the raw buffer (one word per 32-bit scalar, two per 64-bit) into
  the wrapper's local, since a raw buffer loads words. The wrapper
  also keeps the row's address in a static, and a read at a run-time
  index through a reference parameter that is always the row itself
  is a load from the params buffer at that address instead, so the
  copy is read only at constant paths and an array read by index is
  not held per thread. An access path rooted in a local through
  arrays of at most eight elements at side-effect-free run-time
  indexes is printed over the constant indexes, a scalar read as a
  select chain, an aggregate read and a write as an if chain, so the
  compiler keeps the aggregate in registers, the expansion bounded at
  64 element paths and a path rooted in a hoisted temporary chained
  too; a reference argument the callee writes through and a loop
  post's target keep the indexed form.
- SPIR-V (compile/spirv): a binary emitter, one module per entry
  point at version 1.5 under the Vulkan memory model, holding the
  functions the entry reaches and the entry's shell. The module
  declares the Shader, Int64, StorageBuffer8BitAccess,
  VariablePointersStorageBuffer, VulkanMemoryModel, and
  VulkanMemoryModelDeviceScope capabilities: byte buffers are
  blocks of 8-bit integers loaded and stored a byte at a time, word
  buffers blocks of 32-bit words, and a buffer's StorageBuffer
  pointer is passed to every helper as a function argument. Every
  storage buffer load and store carries the NonPrivatePointer
  operand, the form the memory model's barrier operations order
  between invocations; accesses of every other storage class are
  private to their invocation. Every
  local, including a value parameter, is a Function variable loaded
  at each use. A reference parameter is a Function pointer; a
  reference argument that is a bare local passes the local's own
  variable, and one that is a field or element path is copied into a
  temporary before the call and copied back after it when the callee
  writes that parameter, which the compile stage's aliasing rule
  makes exact. The written parameters are computed once per module
  over the acyclic call graph. Multiple results are a result struct
  extracted at the call. A variable-length buffer's length is a
  hidden parameter after the buffer, threaded through every call.
  Narrow integers ride in 32-bit registers of their signedness and
  Mask is a shift up and arithmetic shift down for a signed type,
  an AND for an unsigned one; a bitcast reconciles signedness where
  an instruction requires the register type of its result. Integer
  negation is OpISub from zero, whose result the specification
  defines as the low-order bits of the exact result, so the minimum
  negates to itself; OpSNegate's description does not define its
  overflow. Control flow is structured: a selection merge per if and switch, a loop
  merge with a continue target per for, break and continue as
  branches to them, a fresh block after any terminator, and
  OpUnreachable in a value function's last block when the body
  leaves it open. A bool switch tag selects one or zero for the
  integer selector. The renderer operations are inline sequences:
  masked byte and word access chains, the mask a constant for a
  fixed buffer and the hidden length minus one for a
  variable-length view, atomics at device scope with relaxed
  semantics, GLSL.std.450 min and max, and a control barrier with
  workgroup execution and memory scopes under acquire and release
  semantics over uniform memory with the availability and visibility
  operations, the memory scope the workgroup because only the group's
  own threads read what a phase wrote. The SPIR-V emitter's choice for
  LoadStruct is a copy: a word gather from the params buffer, bound
  as a word block, at the row's word index into the wrapper's local,
  the row's byte address kept in a Private variable. A read at a
  run-time index through a reference parameter that is always the
  row itself is a load through the params buffer's variable at that
  address instead, so the copy is read only at constant paths. An
  access path rooted in a local through arrays of at most eight
  elements at side-effect-free run-time indexes is emitted over the
  constant indexes, a scalar read as an OpSelect chain, an aggregate
  read and a write as an OpSwitch, so no Function pointer is indexed
  at run time and the driver keeps the aggregate in registers; a
  reference argument the callee writes through keeps the indexed
  form.
  Types given a layout decoration, the buffer blocks and the push
  block, never reach the Function or Private storage class; the
  builder refuses a pointer that would. Compiled by the driver
  through the shader module call.

Emitter output is deterministic: identical IR produces identical
shader text or words, making emitted output diffable across runs.


## The device contract

A backend implements Device, Buffer, and Commands (hostgpu.go):

- Build makes one pipeline per entry point, in the module's entry
  order, from the emitted shaders.
- GroupThreads sizes a group entry's row for its load on the device
  (GroupLoad, GroupWidth; see Entry points).
- NewBuffer allocates a buffer of a kind, which names the caller's
  access to it and no memory; the backend chooses where each kind
  lives, and may choose differently for one device than another. An
  upload buffer (BufferUpload) is written by the CPU and read by the
  GPU, bound to a dispatch or as a Copy source. A readback buffer
  (BufferReadback) is written by the GPU only as a Copy destination
  and read by the CPU after the Wait. A working buffer
  (BufferWorking) is the render's working data, which only
  dispatches, Copy, and Fill touch, and its Bytes is nil. An output
  buffer (BufferOutput) is written by the GPU through dispatches,
  Copy, and Fill and read by the CPU after the Wait on the
  submission that wrote it, never written by the CPU: the renderer's
  frame buffer, which the composite writes and the frontend reads.
  Where it lives is the backend's: a device sharing the CPU's memory
  maps the dispatch target itself, so no copy exists, and a discrete
  device places it in host memory the dispatch stores into across
  the bus. Bytes reach
  a working buffer from an upload buffer and leave it for a readback
  buffer through the device, by Copy or by a dispatch, and Fill sets
  a range of a working or an output buffer to a word. The shared
  checks report the rest as host errors: a readback buffer bound to
  a dispatch (CheckBindings), a copy into an upload buffer or from a
  readback buffer (CheckCopy), and a fill of an upload or a readback
  buffer (CheckFill). Size is a buffer's byte size for every kind,
  and a backend takes a variable-length binding's word count from
  it.
- Begin opens the one encoder; Dispatch binds the buffers at indices
  0 to n-1 with the params buffer last and the record after them, and
  runs the pipeline over a grid covered by whole workgroups; Copy
  copies a range of bytes between two distinct buffers of any kinds,
  the offsets and the length multiples of four and the ranges inside
  the buffers (CheckCopy, which every backend calls first); Fill
  writes a 32-bit value to every word of a range of a buffer, the
  offset and the length multiples of four and the range inside the
  buffer (CheckFill); Barrier orders the dispatches, copies, and
  fills before it ahead of those after; Commit submits and returns
  the submission's sequence number. A request the contract refuses
  is a host error (hostgpu.HostError, a constant): the backend checks
  it before recording anything, latches it on the encoder, encodes
  none of the submission's later commands, and reports it from the
  Wait on that submission with any device error. A command after
  Commit is reported by the next Begin. No backend panics. Each
  backend runs a copy with its API's own command: a blit encoder's
  copyFromBuffer on Metal, which ends the compute encoder and orders
  the passes by Metal's hazard tracking of the buffers;
  CopyBufferRegion on DX12, with explicit transitions of the two
  buffers into the copy states and back to unordered access at the
  next dispatch that binds them, the device tracking each buffer's
  state in the list it records, since a buffer promoted to unordered
  access stays there until the list completes; vkCmdCopyBuffer on
  Vulkan, whose barrier covers the compute and transfer stages, with
  a barrier from those stages to the host at the end of every
  submission so the host reads the device's writes once the
  semaphore is waited on. A fill is vkCmdFillBuffer on Vulkan, in the transfer
  stage as a copy is. Metal's buffer fill takes one byte and a DX12
  compute list has no buffer fill, so on those two a fill is a
  dispatch of a kernel the backend owns, written in its own language
  rather than as a compute package entry: on Metal an MSL kernel
  compiled at open, on DX12 an HLSL kernel (compile/hlsl/fill.go)
  that the generator compiles into the backend shader set, which the
  device binds with the buffer at the fill's first byte and issues in
  pieces past the thread group limit as it does a wide grid. Every
  Vulkan buffer is created with the transfer source and destination
  usages besides the storage usage, which a copy and a fill need.
- Wait blocks in the driver's own wait until a submission has
  completed, and releases it and every earlier submission, returning
  their errors joined with any device failure. The wait is the
  driver's because the device schedules the work of a blocked waiter
  at full speed: with the same kernels and a thread that only reads
  completion state and yields, device time measured three times
  longer on Metal. A hung kernel or a removed device ends the wait
  through the OS watchdog with the failure reported.
- The host bookkeeping every backend shares is written once in
  hostgpu: the sequence numbers and the in-flight list (Ledger, which
  each backend holds with its own value per submission, and whose
  Wait is the ledger's pending submissions, one driver wait covering
  them, and the release), the dispatch, copy, and fill checks
  (CheckBindings, CheckCopy, CheckFill), the length words of an entry
  with a variable-length buffer (Lengths), and the rounding of a grid
  to workgroups (Groups). A split of a wide grid stays in the backend
  whose API bounds the dispatch.
- Close waits for what is in flight and releases the pipelines and
  the device. Buffers are released by their owner before it. A
  submission still being encoded is abandoned, not submitted.
- An error in submitted work, a failed queue and a removed device
  included, reaches the renderer through Wait on a device that is
  idle, as does a host error. Open, Build, NewBuffer, and Begin
  return their errors directly. Either way the core switches the
  session to the software renderer.
- No error or device name carries a package or backend
  prefix. Device.Name is the device's own name, and the backend in
  use is the backend package's Name ("Vulkan", "Direct3D 12",
  "Metal"). A caller that displays a message adds the context: the
  core reports a GPU failure as "GPU renderer (Vulkan): ...".
- The frame path allocates nothing on the Go side once the first
  frame has run; the backend tests hold this. A host error is a
  constant, so reporting one allocates nothing either.

The metal backend calls Metal through Objective-C messaging without
cgo and compiles the emitted MSL at open; it allocates upload,
readback, and output buffers in shared storage and working buffers in
private storage. It dispatches each pipeline in threadgroups of the
entry's workgroup, lowered to the largest power of two at or below
the pipeline's threadgroup limit when that limit, which Metal lowers
for a kernel's register use, is smaller. The dx12 backend calls
Direct3D 12 and DXGI through their COM vtables over syscall without
cgo, opens the first hardware adapter DXGI lists, the platform's
choice, and requires feature level 11_0, Shader Model 6.0, and 64-bit
integer operations (the Int64ShaderOps capability) of it, never
passing over it for a later adapter: a requirement it fails is
ErrUnavailable naming the adapter and the requirement, allocates
upload, readback, and output buffers on a custom heap with write-back
CPU pages in system memory, so a dispatch writing an output buffer
stores across the bus as it runs, the heap properties derived from
the adapter's architecture feature (a write-combined upload heap is
the fallback for an upload buffer), and working buffers on the default
heap, all created in the
common state and promoted to unordered access at each use,
binds root descriptors and root constants per dispatch, and reads a
fence whose values are the submission sequence numbers. The vulkan backend calls
the system loader through its procedure address functions without
cgo, opens a Vulkan 1.3 instance and the first hardware physical
device with a compute queue, the push descriptor extension, and the
shaderInt64 feature for the 64-bit integers, storageBuffer8BitAccess
for the byte buffers, and variablePointersStorageBuffer for buffers
passed to helpers, the three features a 1.3 device may still lack,
never a CPU device, and enables with them the features 1.3 requires
that the backend uses: the timeline semaphore, the Vulkan memory
model at device scope, and synchronization2. It allocates upload,
readback, and output buffers in host-visible coherent memory, host
cached when a type offers it, and working buffers in the first
device-local type, records a memory barrier over the compute and
transfer stages at the start of every submission so its accesses
follow the earlier submissions' writes, pushes descriptors and the
push block per dispatch, and signals one timeline semaphore to the
submission's sequence number through the queue, as the DX12 backend
does its fence. A missing loader is ErrUnavailable. Its tests skip
without a usable device.


## Conformance map

One conformance function per row, executed per backend and compared
bit-exactly against its Go execution:

    integer wrap        each width x add/sub/mul at masking points
    32-bit wrap         signed add/sub/mul/neg/shl past the int32 and
                        int64 range, a narrow chain past the register,
                        and the comparisons a compiler folds under
                        undefined overflow
    shifts              arithmetic vs logical, each width
    div/rem             signed truncation, negative operands
    conversions         every narrowing/widening pair
    int64               arithmetic, shifts, comparisons at 64-bit
    struct layout       a probe struct writing field markers by offset,
                        bools between words included
    byte helpers        U8/U16BE/U32BE and Puts across word bounds
    word indexing       reads, writes, a compound assignment, and an
                        increment on buffer words, an int32 index
    short-circuit       call-counting right operands
    tables              package table indexing
    control flow        loop forms, range over an integer, switch,
                        break/continue, multi-return
    entry forms         the span segment range and per-row params
                        (Segments), the same at twice the segment
                        (DoubleSegments), the group form with the
                        barrier (Group), and an index grid wider than one
                        dispatch may issue, so a backend with that
                        limit issues it in pieces and every thread
                        must still see its index across the whole
                        grid (Wide)
    variable buffers    two independent views, their lengths, and a
                        read at an index masked with the input's
                        length (VariableWords); two independent byte
                        buffers, U8 at an address masked with the
                        input's length and PutU8 at an address masked
                        with the output's, neighboring threads
                        writing neighboring bytes of one word
                        (VariableBytes); U16BE and PutU16BE at odd
                        addresses straddling halfwords, words, and
                        the ends of both buffers, no two threads
                        writing one byte (VariableHalfwords); a word
                        read from an array of
                        two variable buffers of different lengths
                        and data chosen by a variable index
                        (VariableArray)
    shared words        on the round state group's three
                        variable-length regions, one operation each,
                        which also holds the group's binding order.
                        raise: every word at the peak of the offers
                        (RaiseWords); add: a range reserved per
                        thread and every word of the ranges marked,
                        the counter at the total (AddWords); swap:
                        every thread on the chain from the head once
                        (SwapChain). The add and the
                        chain are held to the property any thread
                        order gives, not to the Go execution's order,
                        and to the group size each group records, the
                        threads the backend ran after rounding to its
                        workgroups. All three run over several
                        workgroups per group, the group form spanning
                        workgroups.

The conformance functions live in core/compute/conformance under the
subset's rules and double as the compile stage's regression suite:
they are the first thing a backend runs and the gate every emitter
change must pass on every backend.
