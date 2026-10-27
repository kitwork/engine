package main

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// cmdIDLayout measures what a table's key costs on SQLite: id() as INTEGER PRIMARY KEY (the rowid) against uuid()
// as a 16-byte UUIDv7, in a rowid table (the key is a second b-tree) and WITHOUT ROWID (every secondary index
// carries the 16-byte key), with a random UUIDv4 beside them to show why the key is ordered by time. Every table
// has an index on userId, as every foreign key does. Evidence for KITWORK_DATABASE.md mục 8.3 (27/10/2026).
func cmdIDLayout(directory, rowsText string) {
	count, err := strconv.Atoi(rowsText)
	must(err)
	must(os.MkdirAll(directory, 0o755))
	const columns = `userId INTEGER NOT NULL, day TEXT NOT NULL, amount INTEGER NOT NULL, item TEXT NOT NULL`
	clock := &uuidV7{}
	layouts := []struct {
		name, create string
		key          func() []byte // nil: SQLite gives the rowid
	}{
		{"id()   INTEGER PRIMARY KEY (rowid)", `CREATE TABLE t (id INTEGER PRIMARY KEY, ` + columns + `) STRICT`, nil},
		{"uuid() v7, rowid table", `CREATE TABLE t (id BLOB NOT NULL PRIMARY KEY, ` + columns + `) STRICT`, clock.next},
		{"uuid() v7, WITHOUT ROWID", `CREATE TABLE t (id BLOB NOT NULL PRIMARY KEY, ` + columns + `) STRICT, WITHOUT ROWID`, clock.next},
		{"uuid  v4 random, WITHOUT ROWID", `CREATE TABLE t (id BLOB NOT NULL PRIMARY KEY, ` + columns + `) STRICT, WITHOUT ROWID`, uuidV4},
	}
	users := max(count/10, 1)
	fmt.Printf("%d rows, %d users, page %s, WAL, synchronous NORMAL, 10,000 rows a transaction\n\n", count, users, pageSize())
	fmt.Printf("%-34s %12s %10s %14s %16s\n", "layout", "insert rows/s", "file MB", "by key µs", "by userId µs")
	for index, layout := range layouts {
		path := filepath.Join(directory, fmt.Sprintf("idlayout-%d.db", index))
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
		db, err := sql.Open("sqlite", "file:"+toSlash(path)+"?_pragma=page_size("+pageSize()+")&_pragma=journal_mode(WAL)"+
			"&_pragma=synchronous(NORMAL)&_pragma=cache_size(-262144)")
		must(err)
		db.SetMaxOpenConns(1)
		_, err = db.Exec(layout.create)
		must(err)
		_, err = db.Exec(`CREATE INDEX t_userId ON t (userId)`)
		must(err)

		insert := `INSERT INTO t (id, userId, day, amount, item) VALUES (?, ?, ?, ?, ?)`
		if layout.key == nil {
			insert = `INSERT INTO t (userId, day, amount, item) VALUES (?, ?, ?, ?)`
		}
		random := rand.New(rand.NewPCG(1, 2))
		var keys [][]byte
		start := time.Now()
		for done := 0; done < count; {
			transaction, err := db.Begin()
			must(err)
			statement, err := transaction.Prepare(insert)
			must(err)
			for batch := 0; batch < 10000 && done < count; batch++ {
				user, amount := random.IntN(users)+1, random.IntN(5_000_000)
				item := "Cà phê sữa đá " + strconv.Itoa(random.IntN(1000))
				if layout.key == nil {
					_, err = statement.Exec(user, "2026-10-27", amount, item)
				} else {
					key := layout.key()
					keys = append(keys, key)
					_, err = statement.Exec(key, user, "2026-10-27", amount, item)
				}
				must(err)
				done++
			}
			must(statement.Close())
			must(transaction.Commit())
		}
		rate := float64(count) / time.Since(start).Seconds()
		_, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		must(err)
		stat, err := os.Stat(path)
		must(err)

		const lookups = 200_000
		byKey, err := db.Prepare(`SELECT amount FROM t WHERE id = ?`)
		must(err)
		start = time.Now()
		for range lookups {
			var key any = int64(random.IntN(count) + 1)
			if layout.key != nil {
				key = keys[random.IntN(count)]
			}
			var amount int64
			must(byKey.QueryRow(key).Scan(&amount))
		}
		keyTime := time.Since(start).Seconds() * 1e6 / lookups
		byUser, err := db.Prepare(`SELECT COUNT(*), SUM(amount) FROM t WHERE userId = ?`)
		must(err)
		start = time.Now()
		for range lookups {
			var rows, sum sql.NullInt64
			must(byUser.QueryRow(random.IntN(users)+1).Scan(&rows, &sum))
		}
		userTime := time.Since(start).Seconds() * 1e6 / lookups
		must(db.Close())
		fmt.Printf("%-34s %12.0f %10.1f %14.2f %16.2f\n", layout.name, rate, float64(stat.Size())/1e6, keyTime, userTime)
	}
}

// uuidV7 gives time-ordered keys as the engine would: 48 bits of Unix milliseconds, the version, a 12-bit counter
// that keeps keys of one millisecond in order, the variant and 62 random bits.
type uuidV7 struct {
	last    int64
	counter uint64
}

func (u *uuidV7) next() []byte {
	now := time.Now().UnixMilli()
	if now > u.last {
		u.last, u.counter = now, 0
	} else if u.counter++; u.counter > 0x0fff {
		u.last, u.counter = u.last+1, 0
	}
	key := make([]byte, 16)
	binary.BigEndian.PutUint64(key[:8], uint64(u.last)<<16|0x7000|u.counter)
	crand.Read(key[8:])
	key[8] = key[8]&0x3f | 0x80
	return key
}

func uuidV4() []byte {
	key := make([]byte, 16)
	crand.Read(key)
	key[6] = key[6]&0x0f | 0x40
	key[8] = key[8]&0x3f | 0x80
	return key
}
