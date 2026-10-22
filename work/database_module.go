package work

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/value"
)

// kitwork/database (KITWORK_DATABASE.md mục 1): connection() and connect(), which an app's
// .shared/schema/ declares its databases with, and the words that mean something only inside a
// statement's lambda (desc, asc, one, like, search).
//
//	import { connection } from "kitwork/database";
//	export const db = connection().schema({ orders });
//
// The older spellings of the module (database.kitdb(…), database.text(), …) stay on it until the slice
// that removes them: DatabaseModule holds the older *Database, whose methods are its own unless it
// writes one of the same name (connection and connect). Each word is a getter giving a function, as the
// column builders are, so a destructured import is the function itself. count stays the older module's
// (database.count(…)) until then too.
func (w *KitWork) Database() *DatabaseModule {
	return &DatabaseModule{Database: w.legacyDatabase()}
}

// DatabaseModule is kitwork/database.
type DatabaseModule struct {
	*Database
}

// Connection is connection(): the app's database, declared with .schema({ bảng }).
func (m *DatabaseModule) Connection() value.Value {
	tenant := m.tenant
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) != 0 {
			return value.InvalidFailure(value.UnsupportedCode, "connection(\"tên\") chưa có trong engine hôm nay (lát 8, cùng app.databases của host); database của app là connection() không tên")
		}
		return value.Value{K: value.Proxy, V: &ConnectionDeclaration{tenant: tenant}}
	})
}

// Connect is connect(…), a database outside the app, which the engine does not have yet.
func (m *DatabaseModule) Connect() value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.InvalidFailure(value.UnsupportedCode, "connect(…) chưa có trong engine hôm nay (lát 8); database của app là connection().schema({ … })")
	})
}

func (m *DatabaseModule) Desc() value.Value   { return lambdaWord("desc") }
func (m *DatabaseModule) Asc() value.Value    { return lambdaWord("asc") }
func (m *DatabaseModule) One() value.Value    { return lambdaWord("one") }
func (m *DatabaseModule) Like() value.Value   { return lambdaWord("like") }
func (m *DatabaseModule) Search() value.Value { return lambdaWord("search") }

// lambdaWord is a word that means something only inside a statement's lambda, which the compiler reads
// and never runs: called anywhere else, it says so.
func lambdaWord(word string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("%s chỉ có nghĩa trong lambda của câu database: db.list((bảng) => …), viết thẳng trên tên import từ app/.shared/schema/", word))
	})
}

// ConnectionDeclaration is connection() before its action: .schema({ bảng }) declares the app's database.
type ConnectionDeclaration struct {
	tenant *Tenant
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

// schema declares the app's database: the tables by their names, each a struct of kitwork/schema. Nothing
// opens: the KitDB file of the app opens when the first statement runs.
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
		case strings.HasPrefix(name, "kitwork_"):
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s: tiền tố kitwork_ là của bảng engine", name))
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
	proxy := declareDbProxy(c.tenant, nil, "kitdb", value.New(kitDBApplicationName), args[0],
		value.New(map[string]value.Value{"migrate": value.New(true)}))
	if proxy.K != value.Proxy {
		return proxy
	}
	return value.Value{K: value.Proxy, V: newManagedDatabase(proxy.V.(*dbProxy))}
}
