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
//	export const db = connection().schema({ orders });                    // the app's own: KitDB in its folder
//	export const warehouse = connection("warehouse").schema({ stocks });  // a name of the host's app.databases
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
// both are declared with .schema({ bảng }).
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

// ConnectionDeclaration is connection() before its action: .schema({ bảng }) declares the database. name
// is the host's name of it (connection("warehouse")); "" is the app's own.
type ConnectionDeclaration struct {
	tenant *Tenant
	name   string
}

func (c *ConnectionDeclaration) OnGet(key string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("connection().%s không có: khai connection() [.policy]* [.before]* [.after]* .schema({ bảng })", key))
}

func (c *ConnectionDeclaration) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (c *ConnectionDeclaration) OnInvoke(method string, args ...value.Value) value.Value {
	switch method {
	case "schema":
		return c.schema(args)
	case "policy", "before", "after":
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf(".%s(…) chưa có trong engine hôm nay (lát 6 của KITWORK_DATABASE.md)", method))
	}
	return c.OnGet(method)
}

// schema declares the database: the tables by their names, each a struct of kitwork/schema. Nothing
// opens: the app's KitDB file, or the host's SQLite file of a named connection, opens when the first
// statement runs.
func (c *ConnectionDeclaration) schema(args []value.Value) value.Value {
	if len(args) != 1 || args[0].K != value.Map {
		return value.InvalidFailure(value.InvalidArgumentCode, "schema nhận một object bảng: .schema({ orders })")
	}
	tables := args[0].Map()
	if len(tables) == 0 {
		return value.InvalidFailure(value.InvalidArgumentCode, "schema cần ít nhất một bảng")
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
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("bảng %s là một struct: const %s = struct({ … }) của kitwork/schema", name, name))
		}
		primary := 0
		for column, spec := range definition.columns {
			if !spec.vocabulary {
				return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`bảng %s: cột %s viết bằng kiểu của kitwork/schema: import { struct, text } from "kitwork/schema"`, name, column))
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
	proxy := declareDbProxy(c.tenant, nil, "kitdb", value.New(kitDBApplicationName), args[0],
		value.New(map[string]value.Value{"migrate": value.New(true)}))
	if proxy.K != value.Proxy {
		return proxy
	}
	return value.Value{K: value.Proxy, V: newManagedDatabase(proxy.V.(*dbProxy))}
}

// catalogSchema declares the tables of connection("tên") on the database the host declared under that
// name in app.databases. The statements are the same as on the app's own database; a SQLite file of the
// host runs them, with additive migration as the app's KitDB has: at the first statement, a change that
// loses data (a column dropped or retyped) fails the statement and the file is left as it is
// (SchemaTable.ensureTable, refuseDestructiveMigration). A name the host did not declare is refused listing
// the names there are. Every app on the host may name every entry: the catalogue is the host's.
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
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf(`connection("%s") trên %s chưa có trong engine hôm nay: bảng, cột và migration của connection() hôm nay chỉ chạy trên KitDB và SQLite, nên app chưa có đường nào tới database này; host khai tên %s bằng { sqlite: đường_dẫn } thì chạy`, c.name, connectorName(entry.Type), c.name))
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
