package work

import (
	"fmt"
	"sort"
	"strings"

	hostdatabase "github.com/kitwork/engine/database"
	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// kitwork/database (KITWORK_DATABASE.md mục 1): connection() and connect(), which an app's .shared/schema/
// declares its databases with, and the words that mean something only inside a statement's lambda (desc,
// asc, one, like, search, sum, count, avg, min, max).
//
//	import { connection, connect } from "kitwork/database";
//	export const db = connection().tables({ orders });                    // the app's own: .database/application/kitwork.db
//	export const billing = connection().schema("billing").tables({ invoices });  // a schema of it: billing.db beside it
//	export const warehouse = connection("warehouse").tables({ stocks });  // a name of the host's app.databases
//	export const crm = connect({ postgresql: environment.CRM_URL });      // a database outside the app: query()
//
// The older spellings of the module (database.kitdb(…), database.text(), database.entity(), database.where(…),
// database.atomic(…), …) were removed on 22/10/2026. The compiler refuses them where it sees them; reading
// one by a way it did not see is the same error here (MissingMember), saying what the spec writes instead
// (database/statement.RemovedWord). Each word is a getter giving a function, as the column builders are, so
// a destructured import is the function itself.
func (w *KitWork) Database() *DatabaseModule {
	return &DatabaseModule{tenant: w.tenant}
}

// DatabaseModule is kitwork/database.
type DatabaseModule struct {
	tenant *Tenant
}

// Connection is connection(): the app's database, or with a name one the host declared in app.databases;
// both are declared with .tables({ bảng }), after .schema("tên") for a schema other than public.
func (m *DatabaseModule) Connection() value.Value {
	tenant := m.tenant
	return value.NewFunc(func(args ...value.Value) value.Value {
		switch {
		case len(args) == 0:
			return value.Value{K: value.Proxy, V: &ConnectionDeclaration{tenant: tenant}}
		case len(args) == 1 && args[0].K == value.String && strings.TrimSpace(args[0].String()) != "":
			name := args[0].String()
			if reserved := statement.ReservedConnection(name); reserved != "" {
				return value.InvalidFailure(value.InvalidArgumentCode, reserved)
			}
			return value.Value{K: value.Proxy, V: &ConnectionDeclaration{tenant: tenant, name: name}}
		}
		return value.InvalidFailure(value.InvalidArgumentCode, `connection nhận tên database viết bằng chuỗi: connection("warehouse"), tên có trong app.databases({ … }) của application.work`)
	})
}

// Connect is connect({ postgresql: url }): a database outside the app, which answers only query().
func (m *DatabaseModule) Connect() value.Value {
	tenant := m.tenant
	return value.NewFunc(func(args ...value.Value) value.Value {
		return connectExternal(tenant, args)
	})
}

func (m *DatabaseModule) Desc() value.Value   { return lambdaWord("desc") }
func (m *DatabaseModule) Asc() value.Value    { return lambdaWord("asc") }
func (m *DatabaseModule) One() value.Value    { return lambdaWord("one") }
func (m *DatabaseModule) Like() value.Value   { return lambdaWord("like") }
func (m *DatabaseModule) Search() value.Value { return lambdaWord("search") }
func (m *DatabaseModule) Sum() value.Value    { return lambdaWord("sum") }
func (m *DatabaseModule) Count() value.Value  { return lambdaWord("count") }
func (m *DatabaseModule) Avg() value.Value    { return lambdaWord("avg") }
func (m *DatabaseModule) Min() value.Value    { return lambdaWord("min") }
func (m *DatabaseModule) Max() value.Value    { return lambdaWord("max") }

// MissingMember is a word the module has not: an older word is the error that says what the spec writes
// instead; any other is nothing, as a missing member of any module is.
func (m *DatabaseModule) MissingMember(name string) value.Value {
	if spelling, removed := statement.RemovedWord(name); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("database.%s đã bỏ: %s", name, spelling))
	}
	return value.Value{K: value.Nil}
}

// lambdaWord is a word that means something only inside a statement's lambda, which the compiler reads
// and never runs: called anywhere else, it says so.
func lambdaWord(word string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("%s chỉ có nghĩa trong lambda của câu database: db.list((bảng) => …), viết thẳng trên tên import từ app/.shared/schema/", word))
	})
}

// ConnectionDeclaration is connection() before its action: .tables({ bảng }) declares the database. name
// is the host's name of it (connection("warehouse")); "" is the app's own. schema is what .schema("billing")
// picked (KITWORK_DATABASE.md mục 1, "Schema"); "" is public.
type ConnectionDeclaration struct {
	tenant *Tenant
	name   string
	schema string
}

func (c *ConnectionDeclaration) OnGet(key string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`connection().%s không có: khai connection() [.schema("tên")] [.policy]* [.before]* [.after]* .tables({ bảng })`, key))
}

func (c *ConnectionDeclaration) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (c *ConnectionDeclaration) OnInvoke(method string, args ...value.Value) value.Value {
	switch method {
	case "tables":
		return c.tables(args)
	case "schema":
		return c.pickSchema(args)
	case "policy", "before", "after":
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf(".%s(…) chưa có trong engine hôm nay (lát 6 của KITWORK_DATABASE.md)", method))
	}
	return c.OnGet(method)
}

// pickSchema is .schema("billing"): the schema the tables go in, a file of its own beside the public one.
// Written once, right after connection(…); .schema({ bảng }), the older spelling of .tables, says so.
func (c *ConnectionDeclaration) pickSchema(args []value.Value) value.Value {
	if len(args) == 1 && args[0].K == value.Map {
		return value.InvalidFailure(value.InvalidArgumentCode, statement.TablesRenamed)
	}
	if c.schema != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, `.schema("tên") viết một lần, ngay sau connection(…)`)
	}
	if len(args) != 1 || args[0].K != value.String {
		return value.InvalidFailure(value.InvalidArgumentCode, statement.SchemaName(""))
	}
	name := args[0].String()
	if refused := statement.SchemaName(name); refused != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, refused)
	}
	if c.name != "" {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf(`connection(%q).schema(…) chưa có trong engine hôm nay: schema chạy trên database của app, connection()`, c.name))
	}
	return value.Value{K: value.Proxy, V: &ConnectionDeclaration{tenant: c.tenant, name: c.name, schema: name}}
}

// tables declares the database: the tables by their names, each a table of kitwork/schema. Nothing opens:
// the app's SQLite file of the schema (appDatabasePath), or the host's SQLite file of a named connection,
// opens when the first statement runs.
func (c *ConnectionDeclaration) tables(args []value.Value) value.Value {
	if len(args) != 1 || args[0].K != value.Map {
		return value.InvalidFailure(value.InvalidArgumentCode, "tables nhận một object bảng: .tables({ orders })")
	}
	tables := args[0].Map()
	if len(tables) == 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, "tables cần ít nhất một bảng")
	}
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch {
		case name == "session":
			return value.InvalidFailure(value.InvalidArgumentCode, "session là tên dành riêng của phiên (tham số của policy), không làm tên bảng: bảng lưu phiên là sessions")
		case statement.EngineTable(name):
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s: tiền tố kitwork_ hay _kitwork_ (viết hoa hay thường) là của bảng engine", name))
		}
		definition, ok := tables[name].V.(*StructDef)
		if !ok || definition == nil {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("bảng %s là một table: const %s = table({ … }) của kitwork/schema", name, name))
		}
		primary := 0
		for column, spec := range definition.columns {
			if !spec.vocabulary {
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`bảng %s: cột %s viết bằng kiểu của kitwork/schema: import { table, text } from "kitwork/schema"`, name, column))
			}
			if spec.declareErr != "" {
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("bảng %s, cột %s: %s", name, column, spec.declareErr))
			}
			if spec.primary {
				primary++
			}
		}
		if primary != 1 {
			return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("bảng %s cần đúng một id() (bảng không có id() chưa có trong engine hôm nay)", name))
		}
	}
	if c.name != "" {
		return c.catalogSchema(args[0])
	}
	proxy := declareDbProxy(c.tenant, nil, "sqlite", value.New(appDatabaseName(c.schema)), args[0])
	if proxy.K != value.Proxy {
		return proxy
	}
	own := proxy.V.(*dbProxy)
	own.sqlitePath = appDatabasePath(c.tenant, c.schema)
	return value.Value{K: value.Proxy, V: newManagedDatabase(own)}
}

// applicationDatabaseName is the name of the app's database, connection().tables({ … }) of .shared/schema/
// (KITWORK_DATABASE.md mục 1). Its file is in the app's folder, app/.database/application/
// (apps/<identity>/.database/application/ for a host of many apps), never in a site's .data/. A proxy built by
// name cannot name it: newDbProxy refuses a name that starts with @ once databaseRel has read it ("./@application"
// is "@application").
const applicationDatabaseName = "@application"

// engineTable reports a table the engine keeps in the app's file for itself (kitwork_runs and
// kitwork_definitions, the runs of its background work: queue_runs.go).
func engineTable(name string) bool { return strings.HasPrefix(strings.ToLower(name), "kitwork_") }

// appDatabaseName is the name a schema of the app's database is registered under: @application for public,
// @application/billing for .schema("billing"). @ marks a name as the engine's, which no site file takes.
func appDatabaseName(schema string) string {
	if schema == "" {
		return applicationDatabaseName
	}
	return applicationDatabaseName + "/" + schema
}

// appDatabasePath is the SQLite file a schema of the app's database lives in (KITWORK_KITDB.md mục 3,
// 29/10/2026): .database/application/kitwork.db for public, billing.db beside it for .schema("billing"). The
// KitDB file the app's database was before, application.kitdb, is left as it is.
func appDatabasePath(t *Tenant, schema string) string {
	return t.resolveApp(".database", "application", statement.SchemaFileName(schema))
}

// catalogSchema declares the tables of connection("tên") on the database the host declared under that
// name in app.databases. The statements are the same as on the app's own database; a SQLite file of the
// host runs them, held against the declaration as the app's own database is (database_follows.go): what the file
// lacks or has otherwise fails the statements, kitwork migrate adds what it lacks, the file is the root. A name the
// host did not declare is refused listing the names there are. Every app on the host may name every entry: the
// catalogue is the host's.
func (c *ConnectionDeclaration) catalogSchema(tables value.Value) value.Value {
	entry, declared := hostdatabase.CatalogEntry(c.name)
	if !declared {
		names := hostdatabase.CatalogNames()
		there := "host chưa khai tên nào"
		if len(names) > 0 {
			there = "có: " + strings.Join(names, ", ")
		}
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`connection("%s"): host không khai database tên %s trong app.databases({ … }) của application.work; %s`, c.name, c.name, there))
	}
	if entry.Type != "sqlite" {
		// no other way reaches it either: the app never sees the host's URL, and connect() refuses an
		// address in private or local space
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf(`connection("%s") trên %s chưa có trong engine hôm nay: bảng, cột và migration của connection() hôm nay chỉ chạy trên SQLite, nên app chưa có đường nào tới database này; host khai tên %s bằng { sqlite: đường_dẫn } thì chạy`, c.name, connectorName(entry.Type), c.name))
	}
	proxy := declareDbProxy(c.tenant, nil, "sqlite", value.New(catalogDatabaseName(c.name)), tables)
	if proxy.K != value.Proxy {
		return proxy
	}
	host := proxy.V.(*dbProxy)
	host.sqlitePath = entry.Name
	return value.Value{K: value.Proxy, V: newManagedDatabase(host)}
}

// catalogDatabasePrefix starts the name a database of the host's catalogue is registered under: @ marks a
// name as the engine's, which a site's own files never take.
const catalogDatabasePrefix = "@databases/"

// catalogDatabaseName is the name the host's database called name is registered under.
func catalogDatabaseName(name string) string { return catalogDatabasePrefix + name }

// connectorName is a connector as the spec writes it: postgresql, mysql, sqlite.
func connectorName(connection string) string {
	if connection == "postgres" {
		return "postgresql"
	}
	return connection
}
