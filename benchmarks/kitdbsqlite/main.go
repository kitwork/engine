// kitdbsqlite reads the SQLite copy of the retained 13,773,074-row `shopping` table and measures what SQLite does
// with the engine's data. It is the evidence behind docs/KITWORK_KITDB.md of the Kitwork repository (KitDB on
// SQLite): SQLite keeps the rows, KitDB keeps the layer above them.
//
//	kitdbsqlite verify <main.db>                    quick_check, then the checksum of every row read back from SQLite
//	kitdbsqlite query <main.db> <query>             one read-only SQLite query
//	kitdbsqlite decimal                             what a decimal kept as TEXT does in SQLite
//	kitdbsqlite idlayout <directory> <rows>         what id() and uuid() cost as a table's key
//	kitdbsqlite anchor <directory>                  the engine's anchors: a base, then the pages changed; restores, the fold
//	kitdbsqlite at <directory>                      at() going backward from the live file against the restore it runs today
//	kitdbsqlite changeset <directory>               SQLite's changesets of the same writes against kitwork_history
//	kitdbsqlite readtail <directory>                where a read right after a commit waits, with Windows' timer as it is and at 1 ms
//	kitdbsqlite analytics <kitwork.db> <copies>      KCOL on SQLite: the copy of shopping's analytic columns built, then read back
//	kitdbsqlite port <kitwork.db> <copies>           the same queries through the read-only port, the copy against SQLite
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
	arguments := map[string]int{"verify": 1, "query": 2, "decimal": 0, "idlayout": 2, "anchor": 1, "at": 1, "changeset": 1, "readtail": 1, "analytics": 2, "port": 2}
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "")
	}
	if count, known := arguments[os.Args[1]]; !known || len(os.Args) != count+2 {
		fmt.Fprintln(os.Stderr, "usage: kitdbsqlite verify|query|decimal|idlayout|anchor|at|changeset|readtail|analytics|port …")
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
	case "anchor":
		cmdAnchor(os.Args[2])
	case "at":
		cmdAt(os.Args[2])
	case "changeset":
		cmdChangeset(os.Args[2])
	case "readtail":
		cmdReadTail(os.Args[2])
	case "analytics":
		cmdAnalytics(os.Args[2], os.Args[3])
	case "port":
		cmdPort(os.Args[2], os.Args[3])
	}
}
