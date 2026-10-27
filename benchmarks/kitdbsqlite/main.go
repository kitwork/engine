// kitdbsqlite copies the retained 13,773,074-row KitDB `shopping` table into one SQLite file and compares the
// two engines on that data. It is the evidence behind docs/KITWORK_KITDB.md of the Kitwork repository (KitDB on
// SQLite): SQLite keeps the rows, KitDB keeps the layer above them.
//
// Open only a COPY of the KitDB file: opening KitDB takes the writer lock and recovers, there is no read-only open.
//
//	kitdbsqlite copy <copy of data.kitdb> <new main.db>     copy every row, chain a checksum over the stream
//	kitdbsqlite verify <main.db>                            quick_check, then the same checksum read back from SQLite
//	kitdbsqlite bench <copy of data.kitdb> <main.db> <label> point, page and analytic queries on both engines
//	kitdbsqlite refresh <copy of data.kitdb>                build the KCOL sidecar (.analytics)
//	kitdbsqlite execute <copy of data.kitdb> <statement>    one KitDB statement, e.g. ALTER … SET ANALYTICS
//	kitdbsqlite measure <copy of data.kitdb> <query>        one KitDB query: path, first and best time
//	kitdbsqlite query <main.db> <query>                     one read-only SQLite query
//	kitdbsqlite definition <copy of data.kitdb>             the Schema IR of every table
//	kitdbsqlite decimal                                     what a decimal kept as TEXT does in SQLite
//	kitdbsqlite idlayout <directory> <rows>                 what id() and uuid() cost as a table's key
//
// KITDBSQLITE_PAGE sets the SQLite page size of copy (default 16384); KITDBSQLITE_LIMIT stops copy after that
// many rows for a quick trial.
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
	arguments := map[string]int{"copy": 2, "verify": 1, "bench": 3, "refresh": 1, "execute": 2, "measure": 2,
		"query": 2, "definition": 1, "decimal": 0, "idlayout": 2}
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "")
	}
	if count, known := arguments[os.Args[1]]; !known || len(os.Args) != count+2 {
		fmt.Fprintln(os.Stderr, "usage: kitdbsqlite copy|verify|bench|refresh|execute|measure|query|definition|decimal|idlayout …")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "copy":
		cmdCopy(os.Args[2], os.Args[3])
	case "verify":
		cmdVerify(os.Args[2])
	case "bench":
		cmdBench(os.Args[2], os.Args[3], os.Args[4])
	case "refresh":
		cmdRefresh(os.Args[2])
	case "execute":
		cmdExecute(os.Args[2], os.Args[3])
	case "measure":
		cmdMeasure(os.Args[2], os.Args[3])
	case "query":
		cmdQuery(os.Args[2], os.Args[3])
	case "definition":
		cmdDefinition(os.Args[2])
	case "decimal":
		cmdDecimal()
	case "idlayout":
		cmdIDLayout(os.Args[2], os.Args[3])
	}
}
