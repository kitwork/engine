package work

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/database"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

// Database chooses the database this folder's routes read as ctx.database, by the name the host
// declared for it in the manifest (app.database's managed root publishes each database under its
// folder's name; app.postgresql/.connect declare an external one under its alias).
//
//	router.database("shop");
//
// The choice is inherited down the folders like router.colors: the nearest folder that chose wins,
// so the site root chooses for the site and a branch (quan-ly/router.kitwork.js) for itself and the
// folders under it. One folder makes one choice: a second call is an error, since a default has no
// plural — every other declared database is reached by name, ctx.database("ledger").
//
// The name is checked here against what the host declared: a name nothing declared is this folder's
// error at boot, not a connection failure at two in the morning.
func (f *FolderRouter) Database(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 || !args[0].IsString() || strings.TrimSpace(args[0].String()) == "" {
		return f, fmt.Errorf(`router.database nhận tên một database đã khai trong manifest: router.database("shop"); database khác gọi bằng ctx.database("tên")`)
	}
	name := strings.TrimSpace(args[0].String())
	if f == nil {
		return f, nil
	}
	if err := databaseDeclared(name); err != nil {
		return f, err
	}
	if f.database != "" {
		return f, fmt.Errorf(`router.database("%s"): thư mục này đã chọn "%s"; một thư mục một mặc định, database khác gọi bằng ctx.database("%s")`, name, f.database, name)
	}
	f.database = name
	return f, nil
}

// databaseDeclared checks that the host declared exactly one database under alias: an external
// connection of the manifest, or a database a managed KitDB root publishes. A name both declare —
// .postgresql("system") beside a .database/system/ folder — is refused here, at boot, naming both,
// rather than at the first request (resolveAlias refuses it too, so nothing is ever chosen by luck).
// Resolving an owned alias may open that database, which the declaration would do anyway.
func databaseDeclared(alias string) error {
	_, external := database.Configs[alias]
	owned, err := database.ResolveOwned(alias)
	if err != nil {
		return fmt.Errorf(`router.database("%s"): %w`, alias, err)
	}
	if external && owned != nil {
		return fmt.Errorf(`router.database("%s"): tên này có ở hai chỗ, một database ngoài trong manifest (app.postgresql/app.connect) và một thư mục trong managed root của app.database; đổi tên một bên`, alias)
	}
	if !external && owned == nil {
		return fmt.Errorf(`router.database: không có database tên "%s"; khai nó trong manifest (app.database cho KitDB, app.postgresql hay app.connect cho database ngoài) rồi mới chọn ở đây`, alias)
	}
	return nil
}

// routeDatabase is one route's default database: the router.database() of the nearest folder from
// the route up to the site root that chose one, "" when none did.
func routeDatabase(node *RouteNode) string {
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && n.folder.database != "" {
			return n.folder.database
		}
	}
	return ""
}

// RequestDatabase is ctx.database: a database handle made for one request, over the connection the
// host owns, bound to the request's scope. It is the tenant Database with its alias fixed by the
// route's folder (routeDatabase). It answers as an object — ctx.database.name says which database it
// is, ctx.database.table(…) and the rest are the tenant Database's — and as a function:
// ctx.database("ledger") is the same kind of handle on another declared database.
type RequestDatabase struct {
	inner *Database
}

// Name is the alias of the database this handle reads: ctx.database.name.
func (d *RequestDatabase) Name() string {
	if d == nil || d.inner == nil {
		return ""
	}
	return d.inner.alias
}

func (d *RequestDatabase) OnGet(key string) value.Value {
	if key == "name" {
		return value.New(d.Name())
	}
	return value.New(d.inner).Get(key)
}

func (d *RequestDatabase) OnCompare(_ string, _ value.Value) value.Value {
	return value.Value{K: value.Nil}
}

func (d *RequestDatabase) OnInvoke(method string, args ...value.Value) value.Value {
	if method != "" {
		return value.New(d.inner).Invoke(method, args...)
	}
	// ctx.database("ledger"): another declared database, by name
	if len(args) == 0 {
		return value.Value{K: value.Proxy, V: d}
	}
	if len(args) != 1 || !args[0].IsString() || strings.TrimSpace(args[0].String()) == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, `ctx.database nhận tên một database đã khai trong manifest: ctx.database("ledger")`)
	}
	alias := strings.TrimSpace(args[0].String())
	if alias == d.Name() {
		return value.Value{K: value.Proxy, V: d}
	}
	return requestDatabaseValue(d.inner.tenant, d.inner.requestScope, alias)
}

// requestDatabaseValue makes the request handle for alias, or the failure that explains why the
// host could not open it.
func requestDatabaseValue(tenant *Tenant, scope *requestscope.Scope, alias string) value.Value {
	handle := &Database{tenant: tenant, requestScope: scope, config: &database.Config{Alias: alias}, alias: alias}
	connection, err := handle.resolveAlias(alias)
	if err != nil {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`ctx.database("%s"): không có database tên "%s" trong manifest (%v)`, alias, alias, err))
	}
	handle.sqlDB = connection
	return value.Value{K: value.Proxy, V: &RequestDatabase{inner: handle}}
}

// Database is ctx.database: the handle on the database the route's folder chose (router.database),
// made for this request. A route whose folders chose none is told to choose, by name.
func (c *Context) Database() value.Value {
	if c == nil || c.request == nil || c.request.router == nil {
		return value.InvalidFailure(value.UnavailableCode, "ctx.database: request này không có router")
	}
	router := c.request.router
	if router.node == nil {
		return value.InvalidFailure(value.UnavailableCode, "ctx.database: request này không đi qua cây thư mục nên không có database của thư mục")
	}
	name := routeDatabase(router.node)
	if name == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, `ctx.database: thư mục này chưa chọn database; viết router.database("tên") trong router của thư mục hay một thư mục cha (tên khai trong manifest)`)
	}
	return requestDatabaseValue(router.tenant, router.requestScope, name)
}
