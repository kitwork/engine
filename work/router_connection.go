package work

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

// router.connection("tên"), router.connect({ … }) and context.database (15/10/2026) plugged a folder's
// routes into a database by name and handed each request a query-builder handle on it. KITWORK_DATABASE.md
// replaced them: an app's databases are declared in its .shared/schema/ and imported where they are used,
// a host's database by its name in app.databases, a database outside the app with connect(). They were
// removed on 22/10/2026; each is now the error that says what to write instead. The
// host's own connections (app.postgresql, app.connect: the queue's and the scheduler's system database)
// stay the host's, and no app code reaches them.

const (
	removedRouterConnection = `router.connection(…) đã bỏ: database có tên khai export const warehouse = connection("warehouse").schema({ bảng }) trong app/.shared/schema/<tên>.work, tên đó khai trong app.databases({ … }) của application.work; database của app là connection() không tên`
	removedRouterConnect    = `router.connect(…) đã bỏ: database ngoài khai export const crm = connect({ postgresql: environment.CRM_URL }) trong app/.shared/schema/<tên>.work, rồi crm.query(sql, [giá trị])`
	removedContextDatabase  = `context.database đã bỏ: database khai trong app/.shared/schema/ và dùng bằng import { db } from "app/.shared/schema/<tên>.work", rồi db.list((bảng) => …); database có tên là connection("tên") với app.databases của host, database ngoài là connect({ … })`
)

// Connection is router.connection(…), removed: the folder's boot fails saying what to write instead.
func (f *FolderRouter) Connection(_ ...value.Value) (*FolderRouter, error) {
	return f, fmt.Errorf("%s", removedRouterConnection)
}

// Connect is router.connect(…), removed: the folder's boot fails saying what to write instead.
func (f *FolderRouter) Connect(_ ...value.Value) (*FolderRouter, error) {
	return f, fmt.Errorf("%s", removedRouterConnect)
}

// Database is context.database, removed: reading it fails saying what to write instead.
func (c *Context) Database() value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, removedContextDatabase)
}
