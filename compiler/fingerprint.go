package compiler

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"

	"github.com/kitwork/engine/runtime"
)

// CompilerSchemaVersion is the explicit compiler-cache contract. Increment it
// whenever lowering or constant semantics change without an incompatible
// opcode encoding change.
//
// 2 added switch/case/default and scoped break; 3 added the protected .safe()
// evaluator; 4 (13/10/2026) made reserved words property names, so source that
// already compiled can mean something else: o.undefined and { undefined: x }
// name the key "undefined" (was "null"), { true: x } / { false: x } / { null: x }
// emit their key as a string constant, and template debug positions after an
// escape moved. No opcode, operand or keyword changed; the bump only retires
// cached artifacts.
//
// 5 (14/10/2026): && and || run their right side only when the left does not
// decide (lowered with DUP/TRUE/FALSE/JUMP; AND/OR stay for a constant or a
// name on the right), ?? and ?. and the wider destructuring lower to existing
// opcodes, and CALL/INVOKE record where the call is (the callee's start, the
// dot) instead of its last argument, so the debug table of most programs moved.
// No opcode or operand changed.
//
// 6 (14/10/2026): const and let live in their block, as in JavaScript. The compiler resolves every
// name and gives a variable of a block the slot name#N, and a declaration of a function's own scope
// whose name a function around may hold the slot name@N (scope.go), so a program that compiled
// before can read or write another variable now: an inner const no longer overwrites the outer one
// or a parent function's. A closure made in a loop that reaches a variable of the iteration gets that
// iteration's (an object per iteration, handed in by a call). Declaring a name twice in one block is
// a compile error. No opcode, operand or instruction meaning changed.
//
// 7 (14/10/2026): x.safe() is gone. Its lowering (the receiver compiled into a hidden evaluator lambda
// and INVOKE "safe" on it) is no more: x.safe() is a compile error that says to write safe(() => x), a
// global function the VM calls like any other, and safe(<not a function>) is a compile error when the
// bundle does not declare safe itself. Columns count characters, not bytes, so the debug table of a
// line with Vietnamese text before a place moved. A cached artifact of schema 6 still holds the
// evaluator lambda, which nothing runs any more: the bump retires it. No opcode or operand changed.
//
// 8 (14/10/2026, the review of 5 to 7): a template that starts with two values, or is one value, starts
// from "" (`${a}${b}` of 1 and 2 is "12", `${5}` is "5"); a return inside for…of drops the loop's
// iterable and index first; an operator, a member read and an array literal record their own place,
// so the debug table moved. A write to a const, continue, typeof, instanceof, delete, in, for…in, the
// comma operator, { a = 1 } outside a pattern and code nested past 1000 levels are compile errors;
// let y; and a block standing alone compile. A cached artifact of schema 7 holds a template compiled
// as a sum: the bump retires it. No opcode or operand changed.
//
// 9 (14/10/2026, "đồng ý hết"): x++ used as a value is the old x (read, update, POP) and ++o.x the new
// o.x (update, POP, read); as a statement or a for's update both compile as before, byte for byte.
// continue compiles to a JUMP to the counting for's update or for…of's next item; typeof x to x; PUSH
// "#typeof"; INVOKE 0, which the VM answers (runtime.TypeofMethod). A SET of o.x = v and o[k] = v
// records the dot or bracket, so the debug table of a program with such a write moved. The VM's
// truthiness (a negative number is true) and a[i] = v past the end (nulls between) changed with it,
// and need no recompile. A cached artifact of schema 8 gives x++ the new value: the bump retires it.
// No opcode or operand changed.
//
// 10 (15/10/2026, the review of 9): an update or a compound assignment of a member or an item whose object
// or key does more than read (a[i++]++, get().n += 1) evaluates them once, into hidden slots (#N), as
// JavaScript does; one whose object and key only read compiles as before, byte for byte. A line break
// before ++ or -- ends the expression (x\n++y is x; ++y, as JavaScript reads it; it was x++; y). A cached
// artifact of schema 9 reads one item of a[i++]++ and writes another: the bump retires it. No opcode or
// operand changed.
//
// 11 (15/10/2026, the owner's L1 to L3, on bytecode 4): x++, x--, ++x and --x lower to STEP, which makes
// a number of x as JavaScript's ToNumber does, then steps it (x; STEP 1 where x; PUSH 1; ADD was), and
// x++ used as a value makes the old x a number (x; STEP 0). The opcode is new, so bytecode 4 moved the
// fingerprint already; the schema moves with it because the lowering changed too, so an artifact of
// schema 10 is retired by both. Division by zero (Infinity, NaN) and a write through null or a string
// (the runtime error TYPE) are the VM's and need no recompile.
const CompilerSchemaVersion uint16 = 11

var currentCompilerFingerprint = compilerFingerprintFor(CompilerSchemaVersion)

// Fingerprint identifies the compiler and runtime contract that produced an
// artifact. It is stable across processes running the same engine build.
func Fingerprint() string {
	return hex.EncodeToString(currentCompilerFingerprint[:])
}

// CompilerFingerprint reports the producer contract recorded on this
// Bytecode value.
func (b *Bytecode) CompilerFingerprint() string {
	if b == nil {
		return ""
	}
	return b.compilerFingerprint
}

// SourceFingerprint reports a deterministic hash of every bundled source name
// and byte used by the compiler.
func (b *Bytecode) SourceFingerprint() string {
	if b == nil {
		return ""
	}
	return b.sourceFingerprint
}

// CacheKey combines source and compiler identity. A bytecode version,
// instruction contract, compiler schema, or source change produces a new key.
func (b *Bytecode) CacheKey() string {
	if b == nil || b.sourceFingerprint == "" {
		return ""
	}
	return cacheKeyForSource(b.sourceFingerprint)
}

func cacheKeyForSource(sourceFingerprint string) string {
	return cacheKeyFor(Fingerprint(), sourceFingerprint)
}

// cacheKeyFor is the cache file name a compiler with this fingerprint gives a
// source: an engine on another schema looks under another name.
func cacheKeyFor(compilerFingerprint, sourceFingerprint string) string {
	hash := sha256.New()
	hash.Write([]byte("kitwork-bytecode-cache"))
	hash.Write([]byte{0})
	hash.Write([]byte(compilerFingerprint))
	hash.Write([]byte{0})
	hash.Write([]byte(sourceFingerprint))
	return hex.EncodeToString(hash.Sum(nil))
}

// compilerFingerprintFor is the fingerprint a compiler on this schema stamps
// into its artifacts, with the current runtime contract.
func compilerFingerprintFor(schema uint16) [sha256.Size]byte {
	return compilerFingerprintWith(schema, runtime.BytecodeVersion, runtime.ProgramEncodingVersion, runtime.InstructionSetChecksum())
}

// compilerFingerprintWith is the fingerprint of a compiler schema on a runtime contract: what an
// engine of an earlier bytecode version stamped is computed with that version's values.
func compilerFingerprintWith(schema, bytecodeVersion, encodingVersion uint16, instructionSetChecksum string) [sha256.Size]byte {
	hash := sha256.New()
	hash.Write([]byte("kitwork-compiler"))
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], schema)
	hash.Write(version[:])
	binary.BigEndian.PutUint16(version[:], bytecodeVersion)
	hash.Write(version[:])
	binary.BigEndian.PutUint16(version[:], encodingVersion)
	hash.Write(version[:])
	hash.Write([]byte(instructionSetChecksum))

	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

func fingerprintSources(sources map[string]string) string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)

	hash := sha256.New()
	var length [8]byte
	for _, name := range names {
		binary.BigEndian.PutUint64(length[:], uint64(len(name)))
		hash.Write(length[:])
		hash.Write([]byte(name))
		source := sources[name]
		binary.BigEndian.PutUint64(length[:], uint64(len(source)))
		hash.Write(length[:])
		hash.Write([]byte(source))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
