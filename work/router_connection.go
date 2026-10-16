package work

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/database"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/utilities/safepath"
	"github.com/kitwork/engine/value"
)

// applicationConnection is the one reserved connection name: the application's own database, the
// host's KitDB root (application.database("./.database/")) in the folder named after the site. It is
// what ctx.database is when a folder plugs nothing, and what router.connection("application") says
// out loud. The manifest's own object is called application, so no declared alias can take the name.
const applicationConnection = "application"

// folderConnection is what a folder plugged ctx.database into: a connection the host declared, by
// its alias (or "application"), or a connection the tenant wrote itself (router.connect).
type folderConnection struct {
	alias  string
	config *database.Config
}

func (c *folderConnection) describe() string {
	if c == nil {
		return `"` + applicationConnection + `"`
	}
	if c.config != nil {
		return "kết nối tenant tự nối (" + c.config.Type + ")"
	}
	return `"` + c.alias + `"`
}

// Connection plugs this folder's ctx.database into a connection the host declared in app.work:
//
//	router.connection("system");        // application.connect("system", …) or a folder of the host's KitDB root
//	router.connection("application");   // the application's own database, which plugging nothing also means
//
// Inherited down the folders like router.colors: the nearest folder that plugged wins. One folder
// plugs once; a second router.connection or router.connect is an error, since a default has no
// plural. Every other declared connection stays reachable by name, ctx.database("system"). The name
// is checked here against what the host declared: a name nothing declared is this folder's error at
// boot, listing the names there are.
func (f *FolderRouter) Connection(args ...value.Value) (*FolderRouter, error) {
	if len(args) == 1 && args[0].IsMap() {
		return f, fmt.Errorf(`router.connection nhận tên một kết nối host đã khai trong app.work: router.connection("system"); tenant tự nối bằng cấu hình thì viết router.connect({ postgresql: environment.require("DATABASE_URL") })`)
	}
	if len(args) != 1 || !args[0].IsString() || strings.TrimSpace(args[0].String()) == "" {
		return f, fmt.Errorf(`router.connection nhận tên một kết nối host đã khai trong app.work: router.connection("system"), hay router.connection("application") cho database của chính app`)
	}
	name := strings.TrimSpace(args[0].String())
	if f == nil {
		return f, nil
	}
	if f.connection != nil {
		return f, fmt.Errorf(`router.connection("%s"): thư mục này đã cắm ctx.database vào %s; một thư mục một mặc định, kết nối khác gọi bằng ctx.database("tên")`, name, f.connection.describe())
	}
	if name != applicationConnection {
		if err := hostConnectionDeclared("router.connection", name); err != nil {
			return f, err
		}
	}
	f.connection = &folderConnection{alias: name}
	return f, nil
}

// Connect plugs this folder's ctx.database into a connection the tenant writes itself, with its
// own credentials from its own environment, no host declaration needed:
//
//	router.connect({ postgresql: environment.require("DATABASE_URL") });   // this folder's ctx.database
//	router.connect("crm", { postgresql: environment.require("CRM_URL") }); // reachable as ctx.database("crm")
//
// A connection is a provider keyed by a URL or a path (postgresql, mysql, kitsql, redis, sqlite —
// a sqlite path stays inside the tenant's folder), or the manifest's option object (type/url/host/
// port/user/password/name…). The connection opens at the first request, into the app's pool,
// never at boot. A named connection may not take a name the host declared, nor "application".
func (f *FolderRouter) Connect(args ...value.Value) (*FolderRouter, error) {
	name := ""
	var source value.Value
	switch {
	case len(args) == 1 && args[0].IsMap():
		source = args[0]
	case len(args) == 2 && args[0].IsString() && args[1].IsMap():
		name = strings.TrimSpace(args[0].String())
		source = args[1]
		if name == "" {
			return f, fmt.Errorf(`router.connect("", { … }): tên kết nối không được rỗng; không đặt tên thì viết router.connect({ … })`)
		}
	case len(args) >= 1 && args[0].IsString():
		return f, fmt.Errorf(`router.connect nhận cấu hình nối do tenant viết: router.connect({ postgresql: environment.require("DATABASE_URL") }); chọn kết nối host đã khai thì viết router.connection("%s")`, strings.TrimSpace(args[0].String()))
	default:
		return f, fmt.Errorf(`router.connect nhận cấu hình nối: router.connect({ postgresql: url }) hay router.connect("tên", { … })`)
	}
	if f == nil {
		return f, nil
	}
	config, err := tenantConnectionConfig(f.tenant, name, source)
	if err != nil {
		return f, err
	}
	if name == "" {
		if f.connection != nil {
			return f, fmt.Errorf(`router.connect({ … }): thư mục này đã cắm ctx.database vào %s; một thư mục một mặc định, kết nối thêm thì đặt tên: router.connect("tên", { … })`, f.connection.describe())
		}
		f.connection = &folderConnection{config: config}
		return f, nil
	}
	if name == applicationConnection {
		return f, fmt.Errorf(`router.connect("%s", …): "%s" là tên dành cho database của chính app; đặt tên khác`, name, name)
	}
	if hostConnectionDeclared("router.connect", name) == nil {
		return f, fmt.Errorf(`router.connect("%s", …): tên này đã là một kết nối host khai trong app.work; đặt tên khác, hay cắm vào nó bằng router.connection("%s")`, name, name)
	}
	if _, taken := f.connections[name]; taken {
		return f, fmt.Errorf(`router.connect("%s", …): thư mục này đã có kết nối tên "%s"`, name, name)
	}
	if f.connections == nil {
		f.connections = map[string]*database.Config{}
	}
	f.connections[name] = config
	return f, nil
}

// tenantConnectionConfig reads one router.connect source into a connection config. A provider key
// with a URL or path is the short form; anything else is the manifest's option object.
func tenantConnectionConfig(t *Tenant, name string, source value.Value) (*database.Config, error) {
	fields := source.Map()
	if len(fields) == 0 {
		return nil, fmt.Errorf(`router.connect({ }): cấu hình rỗng; viết router.connect({ postgresql: environment.require("DATABASE_URL") })`)
	}
	for key, item := range fields {
		provider := canonicalConnector(key)
		if provider == "" {
			continue
		}
		if len(fields) != 1 {
			return nil, fmt.Errorf(`router.connect({ %s: … }): dạng ngắn chỉ có một khoá là nhà cung cấp; thêm tuỳ chọn thì viết dạng đầy đủ { type: "%s", url: …, max_open: … }`, key, provider)
		}
		if !item.IsString() || strings.TrimSpace(item.Text()) == "" {
			return nil, fmt.Errorf(`router.connect({ %s: … }): giá trị là một URL hay đường dẫn trong dấu nháy, thường từ environment.require(…)`, key)
		}
		text := strings.TrimSpace(item.Text())
		config := &database.Config{Alias: name, Type: provider}
		if provider == "sqlite" {
			if t == nil {
				return nil, fmt.Errorf("router.connect: tenant is unavailable")
			}
			full, err := safepath.Resolve(t.resolve(), text)
			if err != nil {
				return nil, fmt.Errorf(`router.connect({ sqlite: "%s" }): đường dẫn phải nằm trong thư mục của app: %v`, text, err)
			}
			config.Name = full
			return config, nil
		}
		if scheme := connectorFromURL(text); scheme != "" && scheme != provider {
			return nil, fmt.Errorf(`router.connect({ %s: … }): URL có scheme của %s, không phải %s`, key, scheme, provider)
		}
		config.URL = text
		return config, nil
	}
	raw, err := json.Marshal(source.Interface())
	if err != nil {
		return nil, fmt.Errorf("router.connect: %v", err)
	}
	config := &database.Config{}
	if err := json.Unmarshal(raw, config); err != nil {
		return nil, fmt.Errorf(`router.connect({ … }): cấu hình không đọc được: %v`, err)
	}
	config.Alias = name
	config.Type = canonicalConnector(config.Type)
	if config.Type == "" && config.URL != "" {
		config.Type = connectorFromURL(config.URL)
	}
	if config.Type == "" {
		return nil, fmt.Errorf(`router.connect({ … }): cần biết nhà cung cấp: { postgresql: url }, hay { type: "postgres", host: …, user: … }`)
	}
	if config.Type == "sqlite" && config.Name != "" && t != nil {
		full, err := safepath.Resolve(t.resolve(), config.Name)
		if err != nil {
			return nil, fmt.Errorf(`router.connect({ type: "sqlite", name: "%s" }): đường dẫn phải nằm trong thư mục của app: %v`, config.Name, err)
		}
		config.Name = full
	}
	return config, nil
}

// canonicalConnector is the connector a provider key or type names, "" for an unknown word.
func canonicalConnector(word string) string {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "postgresql", "postgres":
		return "postgres"
	case "mysql":
		return "mysql"
	case "sqlite", "sqlite3":
		return "sqlite"
	case "kitsql":
		return "kitsql"
	case "redis", "rediss":
		return "redis"
	}
	return ""
}

func connectorFromURL(source string) string {
	separator := strings.Index(source, "://")
	if separator < 1 {
		return ""
	}
	return canonicalConnector(source[:separator])
}

// hostConnectionDeclared checks that the host declared exactly one connection under name: an
// external connection of the manifest, or a database a managed KitDB root publishes. A name both
// declare is refused, naming both; a name neither declares is refused listing the names there are.
func hostConnectionDeclared(method, name string) error {
	_, external := database.Configs[name]
	owned, err := database.ResolveOwned(name)
	if err != nil {
		return fmt.Errorf(`%s("%s"): %w`, method, name, err)
	}
	if external && owned != nil {
		return fmt.Errorf(`%s("%s"): tên này có ở hai chỗ, một kết nối ngoài trong app.work (application.connect/postgresql) và một thư mục trong root KitDB của application.database; đổi tên một bên`, method, name)
	}
	if !external && owned == nil {
		return fmt.Errorf(`%s: không có kết nối host tên "%s"; có: %s`, method, name, strings.Join(declaredConnectionNames(nil), ", "))
	}
	return nil
}

// declaredConnectionNames lists the names a route can plug or call: "application", the manifest's
// aliases, and the tenant's own named connections; the host root's folders are named on disk.
func declaredConnectionNames(named map[string]*database.Config) []string {
	names := []string{applicationConnection}
	seen := map[string]bool{}
	aliases := make([]string, 0, len(database.Configs))
	for alias := range database.Configs {
		aliases = append(aliases, alias)
		seen[alias] = true
	}
	for _, alias := range database.OwnedNames() {
		if !seen[alias] {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	names = append(names, aliases...)
	own := make([]string, 0, len(named))
	for name := range named {
		own = append(own, name)
	}
	sort.Strings(own)
	names = append(names, own...)
	names = append(names, "các thư mục trong root KitDB của application.database")
	return names
}

// routeConnection is one route's plug and name table: the router.connection/connect of the nearest
// folder up the chain that plugged (nil: the application's own), and the named router.connect of
// every folder down to the route, the nearest winning per name.
func routeConnection(node *RouteNode) (*folderConnection, map[string]*database.Config) {
	var plug *folderConnection
	named := map[string]*database.Config{}
	for n := node; n != nil; n = n.parent {
		if n.folder == nil {
			continue
		}
		if plug == nil && n.folder.connection != nil {
			plug = n.folder.connection
		}
		for name, config := range n.folder.connections {
			if _, nearer := named[name]; !nearer {
				named[name] = config
			}
		}
	}
	return plug, named
}

// RequestDatabase is ctx.database: a database handle made for one request, over the pool the app
// owns, bound to the request's scope. It answers as an object — ctx.database.name says which
// connection it is, ctx.database.table(…) and the rest are the tenant Database's — and as a
// function: ctx.database("system") is the same kind of handle on another connection, a host alias,
// a connection the tenant named, or "application".
type RequestDatabase struct {
	inner *Database
	named map[string]*database.Config
}

// Name is what this handle is plugged into: ctx.database.name.
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
	if len(args) == 0 {
		return value.Value{K: value.Proxy, V: d}
	}
	if len(args) != 1 || !args[0].IsString() || strings.TrimSpace(args[0].String()) == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, `ctx.database nhận tên một kết nối: ctx.database("system")`)
	}
	name := strings.TrimSpace(args[0].String())
	if name == d.Name() {
		return value.Value{K: value.Proxy, V: d}
	}
	return requestDatabaseByName(d.inner.tenant, d.inner.requestScope, d.named, name)
}

// requestDatabaseByName is ctx.database(name): the tenant's own named connection, "application",
// or a host alias; anything else is a failure listing the names there are.
func requestDatabaseByName(t *Tenant, scope *requestscope.Scope, named map[string]*database.Config, name string) value.Value {
	if config, ok := named[name]; ok {
		return requestDatabaseValue(t, scope, &folderConnection{config: config}, named)
	}
	if name == applicationConnection {
		return requestDatabaseValue(t, scope, nil, named)
	}
	if _, external := database.Configs[name]; !external {
		if owned, err := database.ResolveOwned(name); err != nil || owned == nil {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`ctx.database("%s"): không có kết nối tên này; có: %s`, name, strings.Join(declaredConnectionNames(named), ", ")))
		}
	}
	return requestDatabaseValue(t, scope, &folderConnection{alias: name}, named)
}

// requestDatabaseValue makes the request handle for one plug, or the failure that explains why the
// host could not open it. nil plugs the application's own database: the host root's database named
// after the site.
func requestDatabaseValue(t *Tenant, scope *requestscope.Scope, plug *folderConnection, named map[string]*database.Config) value.Value {
	handle := &Database{tenant: t, requestScope: scope, config: &database.Config{}}
	switch {
	case plug != nil && plug.config != nil:
		if t == nil {
			return value.InvalidFailure(value.UnavailableCode, "ctx.database: tenant is unavailable")
		}
		connection, err := t.openDatabase(plug.config)
		if err != nil {
			return value.FailureOfError("ctx.database", fmt.Errorf("kết nối tenant tự nối (%s): %w", plug.config.Type, err))
		}
		handle.sqlDB = connection
		handle.config = plug.config
		handle.alias = plug.config.Alias
		if handle.alias == "" {
			handle.alias = "connect"
		}
	case plug != nil && plug.alias != applicationConnection:
		handle.config = &database.Config{Alias: plug.alias}
		handle.alias = plug.alias
		connection, err := handle.resolveAlias(plug.alias)
		if err != nil {
			return value.FailureOfError("ctx.database", fmt.Errorf(`kết nối "%s": %w`, plug.alias, err))
		}
		handle.sqlDB = connection
	default:
		name := applicationDatabaseName(t)
		connection, err := database.ResolveOwned(name)
		if err != nil {
			return value.FailureOfError("ctx.database", err)
		}
		if connection == nil {
			return value.InvalidFailure(value.UnavailableCode, fmt.Sprintf(`ctx.database: database của app chưa có trong root KitDB của host (.database/%s/); tạo thư mục rồi attach, hoặc cắm bằng router.connection("tên") hay router.connect({ … })`, name))
		}
		handle.sqlDB = connection
		handle.config = &database.Config{Alias: name}
		handle.alias = name
	}
	return value.Value{K: value.Proxy, V: &RequestDatabase{inner: handle, named: named}}
}

// applicationDatabaseName is the folder of the host's KitDB root that is this site's own database:
// the site's domain.
func applicationDatabaseName(t *Tenant) string {
	if t != nil && t.entity != nil && t.entity.Domain != "" {
		return t.entity.Domain
	}
	return "default"
}

// Database is ctx.database: the handle on what the route's folder plugged (router.connection or
// router.connect), or the application's own database when nothing was plugged.
func (c *Context) Database() value.Value {
	if c == nil || c.request == nil || c.request.router == nil {
		return value.InvalidFailure(value.UnavailableCode, "ctx.database: request này không có router")
	}
	router := c.request.router
	if router.node == nil {
		return value.InvalidFailure(value.UnavailableCode, "ctx.database: request này không đi qua cây thư mục nên không có kết nối của thư mục")
	}
	plug, named := routeConnection(router.node)
	return requestDatabaseValue(router.tenant, router.requestScope, plug, named)
}
