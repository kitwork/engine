package main

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// cmdChangeset measures SQLite's session extension as a format for the history (docs/KITWORK_KITDB.md mục 5 of the
// Kitwork repository, 04/11/2026): the changeset and the patchset of every commit of the writes `kitdbsqlite at`
// makes (atWorkload, the same seed), eight writes a commit as the engine's writer gathered them there, and what
// recording them costs the writes. It calls SQLite through modernc.org/sqlite/lib, the C translated to Go the engine
// already links, on a connection of its own: no cgo, no module the engine does not have.
//
// directory holds session.db and plain.db, two copies of .database/shopping/kitwork.db: the writes run on both, with
// a session recording them on session.db and none on plain.db.
func cmdChangeset(directory string) {
	recorded := filepath.Join(directory, "session.db")
	plain := filepath.Join(directory, "plain.db")
	checkpointFile(recorded)
	checkpointFile(plain)
	_, _, rowCount := fileGeometry(recorded)
	columns := make([]string, len(shoppingColumns))
	for position, column := range shoppingColumns {
		columns[position] = quoted(column.name)
	}
	writes := atWorkload(columns, rowCount)
	statements := make([]string, len(writes))
	for position, write := range writes {
		statements[position] = literalSQL(write.query, write.arguments)
	}
	tls := libc.NewTLS()
	defer tls.Close()

	bare := runRaw(tls, plain, statements, false)
	fmt.Printf("%d writes, eight a commit, no session:           %s (%.0f writes/s)\n", len(statements), duration(bare.elapsed),
		float64(len(statements))/bare.elapsed.Seconds())
	session := runRaw(tls, recorded, statements, true)
	fmt.Printf("the same with a session and its changeset:       %s (%.0f writes/s, %+.1f%%)\n", duration(session.elapsed),
		float64(len(statements))/session.elapsed.Seconds(), 100*(session.elapsed.Seconds()/bare.elapsed.Seconds()-1))
	var changesetBytes int64
	for _, changeset := range session.changesets {
		changesetBytes += int64(len(changeset))
	}
	fmt.Printf("\n%d commits: changesets %s, patchsets %s; kitwork_history held %s for the same writes (kitdbsqlite at)\n",
		len(session.changesets), gigabytes(changesetBytes), gigabytes(session.patchBytes), gigabytes(atHistoryBytes))
	fmt.Printf("bytes a write: changeset %.0f, patchset %.0f, kitwork_history %.0f\n", float64(changesetBytes)/float64(len(statements)),
		float64(session.patchBytes)/float64(len(statements)), float64(atHistoryBytes)/float64(len(statements)))

	start := time.Now()
	var inverted int64
	for _, changeset := range session.changesets {
		inverted += int64(len(invertChangeset(tls, changeset)))
	}
	fmt.Printf("inverting every changeset: %s (%s out)\n", duration(time.Since(start)), gigabytes(inverted))
}

// atHistoryBytes is what kitwork_history held after the writes of atWorkload through the engine's writer:
// sum(length(changes)) of RESULTS_2026-11-04.md, "at() Going Backward".
const atHistoryBytes = 1_015_989_396

// literalSQL writes the arguments of query into it: every argument of atWorkload is an integer.
func literalSQL(query string, arguments []any) string {
	var text strings.Builder
	next := 0
	for _, character := range query {
		if character == '?' {
			text.WriteString(strconv.FormatInt(arguments[next].(int64), 10))
			next++
			continue
		}
		text.WriteRune(character)
	}
	return text.String()
}

type rawRun struct {
	elapsed    time.Duration
	changesets [][]byte
	patchBytes int64
}

// runRaw runs statements on path, eight a transaction, each recorded by a session of its own when record is true:
// the changeset is taken inside the timing, the patchset beside it outside.
func runRaw(tls *libc.TLS, path string, statements []string, record bool) rawRun {
	raw := openRaw(tls, path)
	defer raw.close()
	raw.exec(`PRAGMA journal_mode=WAL`)
	raw.exec(`PRAGMA synchronous=FULL`)
	var run rawRun
	var aside time.Duration
	start := time.Now()
	for first := 0; first < len(statements); first += 8 {
		var session uintptr
		if record {
			session = raw.session("shopping")
		}
		raw.exec("BEGIN")
		for _, statement := range statements[first:min(first+8, len(statements))] {
			raw.exec(statement)
		}
		raw.exec("COMMIT")
		if record {
			run.changesets = append(run.changesets, raw.take(func(size, data uintptr) int32 {
				return sqlite3.Xsqlite3session_changeset(tls, session, size, data)
			}))
			patch := time.Now()
			run.patchBytes += int64(len(raw.take(func(size, data uintptr) int32 {
				return sqlite3.Xsqlite3session_patchset(tls, session, size, data)
			})))
			aside += time.Since(patch)
			sqlite3.Xsqlite3session_delete(tls, session)
		}
	}
	run.elapsed = time.Since(start) - aside
	return run
}

// wordAt reads the pointer-sized word at address, memory SQLite's C allocated; setWord writes one there. Read through
// libc.GoBytes, the package needs no unsafe of its own.
func wordAt(address uintptr) uintptr {
	return uintptr(binary.NativeEndian.Uint64(libc.GoBytes(address, 8)))
}

func setWord(address, word uintptr) {
	binary.NativeEndian.PutUint64(libc.GoBytes(address, 8), uint64(word))
}

// rawSQLite is one connection opened through SQLite's C interface, translated to Go.
type rawSQLite struct {
	tls *libc.TLS
	db  uintptr
}

func cString(text string) uintptr {
	pointer, err := libc.CString(text)
	must(err)
	return pointer
}

func openRaw(tls *libc.TLS, path string) *rawSQLite {
	name := cString(filepath.ToSlash(path))
	defer libc.Xfree(tls, name)
	handle := libc.Xmalloc(tls, 8)
	defer libc.Xfree(tls, handle)
	setWord(handle, 0)
	if code := sqlite3.Xsqlite3_open_v2(tls, name, handle, sqlite3.SQLITE_OPEN_READWRITE, 0); code != sqlite3.SQLITE_OK {
		panic(fmt.Sprintf("open %s: code %d", path, code))
	}
	return &rawSQLite{tls: tls, db: wordAt(handle)}
}

func (raw *rawSQLite) exec(query string) {
	text := cString(query)
	defer libc.Xfree(raw.tls, text)
	if code := sqlite3.Xsqlite3_exec(raw.tls, raw.db, text, 0, 0, 0); code != sqlite3.SQLITE_OK {
		panic(fmt.Sprintf("%s: %s", query, libc.GoString(sqlite3.Xsqlite3_errmsg(raw.tls, raw.db))))
	}
}

func (raw *rawSQLite) close() {
	sqlite3.Xsqlite3_close_v2(raw.tls, raw.db)
}

// session starts a session on main recording table.
func (raw *rawSQLite) session(table string) uintptr {
	schema := cString("main")
	defer libc.Xfree(raw.tls, schema)
	handle := libc.Xmalloc(raw.tls, 8)
	defer libc.Xfree(raw.tls, handle)
	if code := sqlite3.Xsqlite3session_create(raw.tls, raw.db, schema, handle); code != sqlite3.SQLITE_OK {
		panic(fmt.Sprintf("session: code %d", code))
	}
	session := wordAt(handle)
	name := cString(table)
	defer libc.Xfree(raw.tls, name)
	if code := sqlite3.Xsqlite3session_attach(raw.tls, session, name); code != sqlite3.SQLITE_OK {
		panic(fmt.Sprintf("attach %s: code %d", table, code))
	}
	return session
}

// take copies what produce writes, a size and a buffer SQLite allocated, into Go memory and frees SQLite's.
func (raw *rawSQLite) take(produce func(size, data uintptr) int32) []byte {
	return takeBuffer(raw.tls, produce)
}

func takeBuffer(tls *libc.TLS, produce func(size, data uintptr) int32) []byte {
	size := libc.Xmalloc(tls, 8)
	defer libc.Xfree(tls, size)
	data := libc.Xmalloc(tls, 8)
	defer libc.Xfree(tls, data)
	setWord(size, 0)
	setWord(data, 0)
	if code := produce(size, data); code != sqlite3.SQLITE_OK {
		panic(fmt.Sprintf("changeset: code %d", code))
	}
	length := int(int32(binary.NativeEndian.Uint32(libc.GoBytes(size, 4))))
	pointer := wordAt(data)
	out := make([]byte, length)
	if length > 0 {
		copy(out, libc.GoBytes(pointer, length))
	}
	sqlite3.Xsqlite3_free(tls, pointer)
	return out
}

// invertChangeset is changeset undone: every insert a delete, every delete an insert, every update its old values.
func invertChangeset(tls *libc.TLS, changeset []byte) []byte {
	if len(changeset) == 0 {
		return nil
	}
	input := libc.Xmalloc(tls, uint64(len(changeset)))
	defer libc.Xfree(tls, input)
	copy(libc.GoBytes(input, len(changeset)), changeset)
	return takeBuffer(tls, func(size, data uintptr) int32 {
		return sqlite3.Xsqlite3changeset_invert(tls, int32(len(changeset)), input, size, data)
	})
}
