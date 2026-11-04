// kitdbsqlite reads the SQLite copy of the retained 13,773,074-row `shopping` table and measures what SQLite does
// with the engine's data. It is the evidence behind docs/KITWORK_KITDB.md of the Kitwork repository (KitDB on
// SQLite): SQLite keeps the rows, KitDB keeps the layer above them.
//
//	kitdbsqlite verify <main.db>                    quick_check, then the checksum of every row read back from SQLite
//	kitdbsqlite query <main.db> <query>             one read-only SQLite query
//	kitdbsqlite decimal                             what a decimal kept as TEXT does in SQLite
//	kitdbsqlite idlayout <directory> <rows>         what id() and uuid() cost as a table's key
//
// The commands that read the KitDB file (copy, bench, refresh, execute, measure, definition) went with KROW on
// 03/11/2026 (docs/KITWORK_KITDB.md câu 13.4); the copy they made of shopping is .database/shopping/kitwork.db.
package main

import (
	"fmt"
	"os"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	arguments := map[string]int{"verify": 1, "query": 2, "decimal": 0, "idlayout": 2}
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "")
	}
	if count, known := arguments[os.Args[1]]; !known || len(os.Args) != count+2 {
		fmt.Fprintln(os.Stderr, "usage: kitdbsqlite verify|query|decimal|idlayout …")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "verify":
		cmdVerify(os.Args[2])
	case "query":
		cmdQuery(os.Args[2], os.Args[3])
	case "decimal":
		cmdDecimal()
	case "idlayout":
		cmdIDLayout(os.Args[2], os.Args[3])
	}
}
