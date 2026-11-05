package work

import "os"

// The KitDB files an app had before 29–30/10/2026, application.kitdb (its database) and storage.kitdb (its
// datastore()), are no longer read. `kitwork migrate kitdb` copied them into kitwork.db beside each, leaving a mark
// in kitwork_copies; the command and the KROW reader it used left the engine on 05/11/2026 (KITWORK_KITDB.md câu
// 13.4), once every file of this workspace was copied, so a file still there is copied with an engine from before
// that day, whose history holds the command: the boot only says so, and stays quiet once the mark is there.

// kitDBCopySchema is the table a SQLite file keeps the copies made into it in: one row for each KitDB file
// copied, named by its path in the app (source), with the digest of what was copied, how many rows, and when
// (milliseconds since the epoch).
const kitDBCopySchema = `CREATE TABLE IF NOT EXISTS kitwork_copies (source TEXT NOT NULL PRIMARY KEY, digest TEXT NOT NULL, rows INTEGER NOT NULL, at INTEGER NOT NULL)`

// kitDBCopyMarked reports whether the SQLite file at target holds the mark of a copy from source. A file that
// does not exist is not made, and one without kitwork_copies holds no mark.
func kitDBCopyMarked(t *Tenant, name, target, source string) bool {
	if !t.insideAppRoot(target) {
		return false
	}
	if _, err := os.Stat(target); err != nil {
		return false
	}
	config := hostSQLiteConfig(name, target)
	pool := t.lookupDatabase(config)
	if pool == nil {
		opened, err := t.openDatabase(config)
		if err != nil {
			return false
		}
		pool = opened
	}
	var held int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'kitwork_copies'`).Scan(&held); err != nil || held == 0 {
		return false
	}
	var marked int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM kitwork_copies WHERE source = ?`, source).Scan(&marked); err != nil {
		return false
	}
	return marked > 0
}

// kitDBApplicationFile is the file of the app's database inside .database/application/ before 29/10/2026.
const kitDBApplicationFile = "application.kitdb"

// kitDBApplicationSource is the KitDB file of the app's database as the mark of a copy names it.
const kitDBApplicationSource = ".database/application/" + kitDBApplicationFile

// applicationCopied reports whether the app's kitwork.db holds the mark of a copy from application.kitdb: what
// that file held, the runs among it, is in kitwork.db, and boot no longer opens it (appKitDBNotCopied).
func applicationCopied(t *Tenant) bool {
	return kitDBCopyMarked(t, appDatabaseName(""), appDatabasePath(t, ""), kitDBApplicationSource)
}
