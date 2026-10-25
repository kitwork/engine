package compiler

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"path/filepath"
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
//
// 12 (22/10/2026, KITWORK_DATABASE.md slice 1, "dịch lúc biên dịch"): a bundle that imports a database
// from an app's .shared/schema/ lowers every statement on it (db.where(…).list(…)) to one call of the
// handle, LOAD db; PUSH "<template>"; holes…; PUSH "#statement"; INVOKE 1+n (database_translate.go), and
// its query lambdas are no longer compiled. A cached artifact of schema 11 holds the lambda form, which
// the handle refuses: the bump retires it. A program that imports no database compiles as before, byte for
// byte. The template's format (database/statement.Version 1) pairs with this schema: a change of the
// format bumps both (TestStatementVersionPairsWithSchema). No opcode or operand changed.
//
// 13 (22/10/2026, KITWORK_DATABASE.md, the older spellings removed): an import of an older word of the
// database module (import { kitdb, text } from "kitwork/database", import { sqlite } from "kitwork") and a
// read of one off the module's name (database.where) are compile errors; connection("tên") and
// connect({ … }) of .shared/schema/ compile, and a handle of connect() answers query() as written. A
// cached artifact of schema 12 that uses an older word would still run until the word fails where it is
// read, instead of the file failing where it is written: the bump retires it. A program that uses none of
// them compiles as before, byte for byte; the template's format is unchanged. No opcode or operand changed.
//
// 14 (22/10/2026, KITWORK_QUEUE.md, a job named by its export): the export object of a file of an app's
// .shared/queue/ goes out through kitwork().queue.#exports({ … }, "<file>"), which names each job after its
// key (queue.go), and export { b as c } gives c the value of b (it gave keys "b", "as" and "c", the last two
// of names nothing declared). A cached artifact of schema 13 that imports a job file holds handles with no
// name, which send() refuses: the bump retires it. A program that imports no job file and aliases no export
// compiles as before, byte for byte. No opcode or operand changed.
//
// 15 (22/10/2026, KITWORK_SCHEDULE.md, a schedule named by its export): the export object of a file of an
// app's .schedule/ goes out through kitwork().schedule.#exports({ … }, "<file>", [names]), as a job file's
// does (schedule.go); cron (import { cron } from "kitwork", kitwork/cron), a rule of kitwork/schedule outside
// .schedule/, and a chain written with literals that breaks a rule of mục 11 are compile errors. A cached
// artifact of schema 14 that imports a .schedule/ file holds handles with no name, and one that writes
// cron would run until the word fails where it is read: the bump retires both. A program that writes
// neither compiles as before, byte for byte. No opcode or operand changed.
//
// 16 (22/10/2026, KITWORK_ROUTINE.md, go moves into kitwork/routine): go(fn) written bare, go read off
// "kitwork" (import { go } from "kitwork", kitwork().go), a word kitwork/routine has not, a function of
// routine.go that uses context, request or response from outside it, and a routine.wait that mixes functions
// with handles or puts retry or timeout on a handle are compile errors (routine.go). A cached artifact of
// schema 15 that writes one of them would run until the call fails where it runs, instead of the file
// failing where it is written: the bump retires it. A program that writes none of them compiles as before,
// byte for byte. No opcode or operand changed.
//
// 17 (22/10/2026, KITWORK_STORAGE.md and KITWORK_SSE.md, cache and sse replaced): cache and sse read off
// "kitwork" (import { cache } from "kitwork", kitwork().sse, kitwork/cache, kitwork/sse) and a word
// kitwork/storage has not (storage.get, import { get } from "kitwork/storage") are compile errors
// (storage.go). A cached artifact of schema 16 that writes one of them would run until the word fails where
// it is read, instead of the file failing where it is written: the bump retires it. A program that writes
// none of them compiles as before, byte for byte. No opcode or operand changed.
//
// 18 (22/10/2026, KITWORK_FILE.md, file in the app's data): the words of kitwork/file of before
// (file.write, file.save, file.base64), the changes at the module's level the spec never has (file.create,
// file.copy, file.move, file.delete, file.remove), file.directory, file.folder, file.path, any other word it
// has not, and a word imported by name off it are compile errors (file.go). A cached artifact of schema 17
// that writes one would run until the word fails where it is read, instead of the file failing where it
// is written: the bump retires it. A program that writes none of them compiles as before, byte for byte.
// No opcode or operand changed.
//
// 19 (22/10/2026, KITWORK_ENVIRONMENT.md, environment read by name): env read off "kitwork" in a file of an
// app (import { env } from "kitwork", kitwork().env), any use of environment but environment.NAME and
// environment.require("NAME") (a write, another name for the whole, an index, going through it), a require
// of a name not written as a string, and a key not named PUBLIC_… handed straight to a page by .view(…) are
// compile errors (environment.go). A cached artifact of schema 18 that writes one of them would run until
// the word fails where it is read, or hand a page a secret: the bump retires it. A program that writes none
// of them compiles as before, byte for byte. No opcode or operand changed.
//
// 20 (23/10/2026, KITWORK_ROUTER.md, router.view removed): router.view read off the router in a router.work
// (router.view(…), const { view } = router, import { view } from "kitwork/router") is a compile error
// (router.go), and a .view(…) handed a function is no longer read for the page, nor are the file's consts
// listed for it (EnvironmentServerOnly is gone with router.view). A cached artifact of schema 19 that writes
// router.view would run until the router refused it where it runs, instead of the file failing where it is
// written: the bump retires it. A program that writes none of them compiles as before, byte for byte. No
// opcode or operand changed.
//
// 21 (24/10/2026, KITWORK_QUEUE.md and KITWORK_SCHEDULE.md mục 2, the folder's config.work by calls): a
// config.work of .shared/queue/ or of .schedule/ no longer returns an object of its top-level names; it runs
// its calls, queue.parallel(4), schedule.keep("7d"), each a line of its own (folder_config.go). The const of
// 17/10 there (const parallel = 4), a chain, a word called twice, and a word of a folder's config.work written
// anywhere else are compile errors; queue.parallel and queue.keep outside it no longer say to write a const.
// A cached artifact of schema 20 of a config.work returns names the loader no longer reads: the bump retires
// it. A program that writes none of them compiles as before, byte for byte. No opcode or operand changed.
//
// 22 (24/10/2026, KITWORK_QUEUE.md and KITWORK_SCHEDULE.md mục 2, queue.database(db) and schedule.database(db)
// in the engine): what a config.work hands queue.database(…) or schedule.database(…) is checked where it is
// written (folder_config.go): a name that is not a database of .shared/schema/, a database of connect(…), a
// string, any other value, or not one argument, are compile errors. A file of .shared/schema/ no longer
// declares a table named _kitwork_… (the ledger of a SQLite file's migrations) or kitwork_… in any case, nor
// two tables whose names differ only in case (SQLite reads them as one): a cached artifact of a route that
// imports such a schema compiled under 21, and the bump retires it. A config.work is compiled at each boot,
// never cached. A program that writes none of these compiles as before, byte for byte. No opcode or operand
// changed.
//
// 23 (24/10/2026, KITWORK_ROUTER.md mục 3, 4, 11, the router's one import and closed words): a router.work and
// a config.work of the app's folder, a site or a branch are read by the router's rules before anything is
// bundled into them (router_rules.go): an import of the router other than import * as router from
// "kitwork/router", the router read as a value or called anywhere but as a line of its own at the top level,
// a word the router does not have or no longer has, a folder call chained or (nearest wins) called twice, a
// door in a config.work, router.timezone outside the app's config.work, const get/post/put/patch at the top
// level, and context.view handed anything but an object written in place (or view taken apart) are compile
// errors; any other .work file that takes kitwork/router is refused. A cached artifact of schema 22 of a
// router.work written in an old way would run until the router met it, or run it: the bump retires it. A
// program that writes none of them compiles as before, byte for byte. No opcode or operand changed.
//
// 24 (25/10/2026, KITWORK_ROUTER.md mục 4, a guard draws no page): context.view in what a guard of a router.work
// or a router config.work runs is a compile error, followed into the files the guard is imported from
// (router_guard.go); the view-taken-apart rule no longer follows a call through a name declared where it is
// written, nor a name the file assigns again. A cached artifact of schema 23 of a router.work whose guard draws a
// page would run until the engine refused it: the bump retires it. A program that writes none of them compiles
// as before, byte for byte. No opcode or operand changed.
const CompilerSchemaVersion uint16 = 24

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
// It names the program whatever app compiled it (a cron's or a queue's stored
// content_hash); the artifact a FileCache keeps is named CacheKeyInApp.
func (b *Bytecode) CacheKey() string {
	if b == nil || b.sourceFingerprint == "" {
		return ""
	}
	return cacheKeyForSource(b.sourceFingerprint)
}

// CacheKeyInApp is the name (without .kwbc) of this program's artifact in a
// FileCache when it was compiled for the app whose folder is appRoot
// (FileCache.CompileFileInApp): CacheKey for a file of no app (""), the key with
// the app's folder otherwise. It is the one formula the cache writes under.
func (b *Bytecode) CacheKeyInApp(appRoot string) string {
	if b == nil || b.sourceFingerprint == "" {
		return ""
	}
	return cacheKeyForApp(appRoot, b.sourceFingerprint)
}

func cacheKeyForSource(sourceFingerprint string) string {
	return cacheKeyFor(Fingerprint(), sourceFingerprint)
}

// cacheKeyForApp is the cache file name of a source compiled for the app whose
// folder is appRoot: the source's own key for a file of no app, and for a file of
// an app that key with the app's absolute folder, so an artifact is only ever
// read back for the app that compiled it.
func cacheKeyForApp(appRoot, sourceFingerprint string) string {
	key := cacheKeyForSource(sourceFingerprint)
	if appRoot == "" {
		return key
	}
	if absolute, err := filepath.Abs(appRoot); err == nil {
		appRoot = absolute
	}
	hash := sha256.New()
	hash.Write([]byte("kitwork-bytecode-cache-app"))
	hash.Write([]byte{0})
	hash.Write([]byte(key))
	hash.Write([]byte{0})
	hash.Write([]byte(filepath.Clean(appRoot)))
	return hex.EncodeToString(hash.Sum(nil))
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
