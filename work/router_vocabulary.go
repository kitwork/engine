package work

// The router of a Kitwork UI folder at run time (KITWORK_ROUTER.md mục 3, 4, 11; 24/10/2026). The compiler
// reads router.work and config.work as their author wrote them (compiler/router_rules.go); the engine runs
// them, and checks again what a file cannot tell, or what the compiler could not follow, in the compiler's
// words from one table (compiler/router_words.go):
//
//   - the router answers only its words: a removed one fails naming the new spelling, any other naming the
//     nearest (uiRouter); a door chains only guard, limit, cache, persist (uiDoor); a folder call gives
//     nothing to chain (routerCallResult);
//   - each word at its level: router.timezone only in the app's config.work, router.reload only at a site's
//     root, router.meta, router.guard, router.limit, router.jsonld, router.cors, router.assets never in the
//     app's, a built-in door only in a site root's router.work, no door in any config.work;
//   - a call whose nearest folder wins once per folder, config.work and router.work together;
//   - what each word is handed, of the shape it takes: a guard a function, a door its handler, a folder
//     published never a dot file or a source (routerArgumentError);
//   - router.limit read strictly, router.timezone an IANA name, router.cors not yet.
//
// The config.work at the app's folder (app/config.work, apps/<identity>/config.work) is the parent of every
// site's root: a site's tenant hears its calls once into a collector (appRouterConfig) and makes them again on
// the site's root before the site's own files run (seed), so the site's calls add over the app's or win over
// them as a branch's do over its site's. router.timezone stays the app's: the zone the boundaries of a
// route's cache count in (routeNow), and a schedule's that writes no .timezone(…) (appTimezone). A template
// site (router.kitwork.js) never reads a config.work and keeps every old word (FolderRouter.MissingMember).

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	schedulerule "github.com/kitwork/engine/utilities/schedule"
	"github.com/kitwork/engine/value"
)

// routerCorsMissing is the boot error of router.cors(…): a word of the table whose rules are still open.
const routerCorsMissing = "router.cors(…) chưa có trong engine: luật nhận request từ trang của site khác còn mở (KITWORK_ROUTER.md mục 14); hôm nay route của router.work từ chối mọi request đổi dữ liệu đến từ trang của site khác, và chưa có cách mở"

// routerConfigWithoutRouter is the boot error of a config.work at a site's root that has no router file: the
// router file marks a site's root (and the host finds a site by it), so the config.work there, and the app's
// with it, would run for nothing.
const routerConfigWithoutRouter = `gốc site có config.work mà không có router.work: router.work đánh dấu gốc một site Kitwork UI, không có nó thì config.work ở đây, guard của nó và config.work của gốc app đều không chạy; thêm router.work cạnh nó (chỉ dòng import * as router from "kitwork/router" cũng được)`

// uiRouter is the router of a Kitwork UI folder as its files read it: the words of kitwork/router, nothing
// else.
type uiRouter struct {
	folder *FolderRouter
}

// Member answers every word, before anything of the Go type is read: a word of the router is the call
// that runs it, an old word the error naming the new spelling, any other the error naming the nearest
// word, as the compiler says each.
func (r *uiRouter) Member(name string) (value.Value, bool) {
	return r.folder.word(name), true
}

// routerFailure is the failure of a router word, its message as the author reads it.
func routerFailure(message string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, message)
}

// word is router.<name> of the folder.
func (f *FolderRouter) word(name string) value.Value {
	if removed, ok := compiler.RouterRemovedWord(name); ok {
		return routerFailure(removed)
	}
	known, ok := compiler.RouterWordOf(name)
	if !ok {
		return routerFailure(compiler.RouterUnknownWord(name, f.app || f.running == compiler.RouterConfigFile))
	}
	return value.NewFunc(func(args ...value.Value) value.Value { return f.call(known, args) })
}

// call runs router.<word>(args): a door gives the route to chain its rules on (uiDoor), a folder call
// nothing to chain (routerCallResult). The app's collector keeps each call for a site's root (seed),
// router.timezone aside, which is the app's own.
func (f *FolderRouter) call(word compiler.RouterWord, args []value.Value) value.Value {
	door := word.Kind == compiler.RouterDoor || word.Kind == compiler.RouterBuiltIn
	if door && (f.app || f.running == compiler.RouterConfigFile) {
		return routerFailure(compiler.RouterDoorInConfig(word.Word))
	}
	if !word.Takes(f.levels()) {
		return routerFailure(compiler.RouterWrongLevel(word.Word, f.where()))
	}
	if wrong := routerArgumentError(word.Word, args); wrong != "" {
		return routerFailure(wrong)
	}
	at := f.site()
	if word.Inheritance == compiler.RouterNearestWins {
		if twice := f.once(word.Word, at); twice != "" {
			return routerFailure(twice)
		}
	}
	if door {
		result := f.invoke(word.Word, args)
		if method, ok := result.V.(*FolderMethod); ok && result.K != value.Invalid {
			return value.New(&uiDoor{method: method, door: word.Word, once: map[string]runtime.SourceLocation{}})
		}
		return result
	}
	result := value.Value{K: value.Nil}
	if f.app && word.Word != "timezone" {
		f.seeds = append(f.seeds, routerSeed{word: word.Word, args: append([]value.Value(nil), args...), at: at, file: f.running})
	} else {
		result = f.apply(word.Word, args)
	}
	if result.K == value.Invalid {
		return result
	}
	return value.Value{K: value.Proxy, V: &routerCallResult{word: word.Word}}
}

// apply runs a folder call on the folder: the words the engine reads its own way, any other by the Go method
// of its name.
func (f *FolderRouter) apply(word string, args []value.Value) value.Value {
	switch word {
	case "timezone":
		return f.setTimezone(args)
	case "limit":
		return f.addLimit(args)
	case "cors":
		return routerFailure(routerCorsMissing)
	}
	return f.invoke(word, args)
}

// levels are where the file being run stands: the app's folder, a site's root (both, for the config.work
// of a site's root that is the app's folder too), a branch.
func (f *FolderRouter) levels() compiler.RouterLevel {
	switch {
	case f.app && f.appRoot:
		return compiler.RouterApp | compiler.RouterSite
	case f.app:
		return compiler.RouterApp
	case f.node != nil && f.node.parent == nil:
		if f.appRoot && f.running == compiler.RouterConfigFile {
			return compiler.RouterApp | compiler.RouterSite
		}
		return compiler.RouterSite
	}
	return compiler.RouterBranch
}

// where is the file being run in the author's words, for an error of a word at the wrong level.
func (f *FolderRouter) where() string {
	file := f.running
	if file == "" {
		file = UIRouterFileName
	}
	switch {
	case f.app:
		return compiler.RouterConfigFile + " của gốc app"
	case f.node != nil && f.node.parent == nil:
		return file + " của gốc site"
	}
	return file + " của một nhánh"
}

// site is where the call being made is written, as the VM running the folder's file gives it; nothing
// outside that run.
func (f *FolderRouter) site() runtime.SourceLocation {
	if f == nil || f.callSite == nil {
		return runtime.SourceLocation{}
	}
	return f.callSite()
}

// routerCallPlace is where a call of a folder is written: its file (config.work or router.work) and line.
type routerCallPlace struct {
	file string
	line int
}

// once records a call whose nearest folder wins (KITWORK_ROUTER.md mục 4): a second in the folder, in the same
// file or the other one, is the error naming both lines. A call of the app's config.work a site's root starts
// from (seed) is its parent's, never counted here.
func (f *FolderRouter) once(word string, at runtime.SourceLocation) string {
	place := routerCallPlace{file: f.running, line: int(at.Line)}
	if f.nearest == nil {
		f.nearest = map[string]routerCallPlace{}
	}
	first, twice := f.nearest[word]
	if !twice {
		f.nearest[word] = place
		return ""
	}
	if first.file == compiler.DesignFile || place.file == compiler.DesignFile {
		// design.<word> of the folder's design.work and router.<word> of its config.work or router.work are one
		// call (router_design.go)
		if first.file == place.file {
			return compiler.DesignCallTwice(word, fmt.Sprintf("dòng %d", first.line), fmt.Sprintf("dòng %d", place.line))
		}
		return compiler.DesignCallTwice(word, designCallPlace(word, first), designCallPlace(word, place))
	}
	if first.file == place.file {
		return compiler.RouterCallTwice(word, fmt.Sprintf("dòng %d", first.line), fmt.Sprintf("dòng %d", place.line))
	}
	return compiler.RouterCallTwice(word, fmt.Sprintf("%s:%d", first.file, first.line), fmt.Sprintf("%s:%d", place.file, place.line))
}

// folderRouterMethods are the Go methods of FolderRouter by their name in lower case, the way reflection
// reads a word: each word of kitwork/router that has one (get → Get, rss → RSS, llmsFull → LlmsFull).
var folderRouterMethods = sync.OnceValue(func() map[string]int {
	kind := reflect.TypeOf(&FolderRouter{})
	methods := make(map[string]int, kind.NumMethod())
	for index := 0; index < kind.NumMethod(); index++ {
		methods[strings.ToLower(kind.Method(index).Name)] = index
	}
	return methods
})

// invoke calls the Go method of a word of the router with args, as a call of the VM would.
func (f *FolderRouter) invoke(word string, args []value.Value) value.Value {
	index, ok := folderRouterMethods()[strings.ToLower(word)]
	if !ok {
		return routerFailure(fmt.Sprintf("router.%s chưa có trong engine này", word))
	}
	return value.Value{K: value.Func, V: reflect.ValueOf(f).Method(index)}.Call(word, args...)
}

// setTimezone is router.timezone(…): the app's zone, an IANA name (KITWORK_ROUTER.md mục 4), read with the
// time zone database the engine carries, so it is the same on Windows and in an empty container; "Local" and
// a name it does not know are errors.
func (f *FolderRouter) setTimezone(args []value.Value) value.Value {
	if len(args) != 1 || !args[0].IsString() {
		return routerFailure(`router.timezone nhận một tên múi giờ IANA: router.timezone("Asia/Ho_Chi_Minh")`)
	}
	zone, err := schedulerule.LoadZone(args[0].Text())
	if err != nil {
		return routerFailure(strings.ReplaceAll(err.Error(), ".timezone(", "router.timezone("))
	}
	f.timezone = zone
	return value.Value{K: value.Nil}
}

// addLimit is router.limit({ rate, per, by }): a ceiling for each client of the folder and every folder below
// it, added to the parents' (KITWORK_ROUTER.md mục 4).
func (f *FolderRouter) addLimit(args []value.Value) value.Value {
	rule, err := limitRule("router.limit", args)
	if err != nil {
		return routerFailure(err.Error())
	}
	f.checkLimitBy(rule.Dim)
	f.limits = append(f.limits, rule)
	return value.Value{K: value.Nil}
}

// limitOldKeys are the keys of the shapes before router.limit (router.ratelimit's units and dimensions,
// .limit's type), each with what it is now.
var limitOldKeys = map[string]string{
	"type": "ý là by?", "period": "ý là per?", "window": "ý là per?", "max": "ý là rate?",
	"ip": "đó là cách viết của router.ratelimit đã bỏ", "browser": "đó là cách viết của router.ratelimit đã bỏ",
	"user": "đó là cách viết của router.ratelimit đã bỏ", "global": "đó là cách viết của router.ratelimit đã bỏ",
	"second": "đó là cách viết của router.ratelimit đã bỏ", "seconds": "đó là cách viết của router.ratelimit đã bỏ",
	"minute": "đó là cách viết của router.ratelimit đã bỏ", "minutes": "đó là cách viết của router.ratelimit đã bỏ",
	"hour": "đó là cách viết của router.ratelimit đã bỏ", "hours": "đó là cách viết của router.ratelimit đã bỏ",
	"day": "đó là cách viết của router.ratelimit đã bỏ", "days": "đó là cách viết của router.ratelimit đã bỏ",
}

// limitRule reads one rate limit of a router.work or a config.work, { rate, per, by }, strictly
// (KITWORK_ROUTER.md mục 4): rate a whole number above 0, per a span written as a word ("30s", "1m",
// "1h"), by "ip" when left out. Any other key, a value of another kind or a span it cannot read is an
// error, never a rule read some other way (a template's limit falls back to one second, ratelimitRules).
// written is the call as the error names it: router.limit, router.post(…).limit.
func limitRule(written string, args []value.Value) (methodLimit, error) {
	example := written + `({ rate: 600, per: "1m", by: "ip" })`
	rule := methodLimit{Dim: "ip"}
	if len(args) != 1 || !args[0].IsMap() {
		return rule, fmt.Errorf("%s nhận một object { rate, per, by }: %s", written, example)
	}
	fields := args[0].Map()
	for _, key := range sortedNames(fields) {
		switch key {
		case "rate", "per", "by":
			continue
		}
		hint := ""
		if old, ok := limitOldKeys[key]; ok {
			hint = "; " + old
		}
		return rule, fmt.Errorf(`%s({ %s: … }): không có khoá "%s"%s; %s nhận rate (số lần), per (khoảng đếm) và by ("ip" hay "global"): %s`,
			written, key, key, hint, written, example)
	}
	rate, ok := fields["rate"]
	if !ok {
		return rule, fmt.Errorf("%s thiếu rate, số lần tối đa trong mỗi khoảng per: %s", written, example)
	}
	if rate.K != value.Number || rate.N < 1 || rate.N != math.Trunc(rate.N) || rate.N > math.MaxInt32 {
		return rule, fmt.Errorf("%s({ rate: %s }): rate là một số nguyên lớn hơn 0, số lần tối đa trong mỗi khoảng per: %s", written, writtenValue(rate), example)
	}
	per, ok := fields["per"]
	if !ok {
		return rule, fmt.Errorf(`%s thiếu per, khoảng đếm viết bằng chữ ("30s", "1m", "1h"): %s`, written, example)
	}
	span := time.Duration(0)
	if per.IsString() {
		span, _ = ParseDuration(per.Text())
	}
	if span <= 0 {
		return rule, fmt.Errorf(`%s({ per: %s }): per là một khoảng viết bằng chữ, "30s", "1m", "1h": %s`, written, writtenValue(per), example)
	}
	if by, ok := fields["by"]; ok {
		if !by.IsString() {
			return rule, fmt.Errorf(`%s({ by: %s }): by là một chữ, "ip" (mặc định) hay "global": %s`, written, writtenValue(by), example)
		}
		switch by.Text() {
		case "ip", "global", "user", "browser":
			rule.Dim = by.Text() // user and browser: checkLimitBy says why neither is counted
		default:
			hint := ""
			if near := strings.ToLower(strings.TrimSpace(by.Text())); near == "ip" || near == "global" {
				hint = fmt.Sprintf(`; ý là "%s"?`, near)
			}
			return rule, fmt.Errorf(`%s({ by: %s }): by là "ip" (mặc định) hay "global"%s: %s`, written, writtenValue(by), hint, example)
		}
	}
	rule.Rate, rule.Per = int(rate.N), span
	return rule, nil
}

// writtenValue is a value as a message writes it back: a text in quotes.
func writtenValue(v value.Value) string {
	if v.IsString() {
		return fmt.Sprintf("%q", v.Text())
	}
	return v.String()
}

// routerArgumentError is what is wrong with what a word of a Kitwork UI router is handed, "" when nothing
// is (KITWORK_ROUTER.md mục 3, 4, 11): a guard that is no function, a door without its handler, a branch's
// value of another kind, a folder published that is never served. A template's router passes such a value
// over, a guard or a handler of null among them, so a branch could lose its guard without a word. The words
// that read their own value (colors, fonts, gradients, appearance, contrast, transition, highlight, bundle,
// reload, timezone, limit) say it themselves; cors is not yet.
func routerArgumentError(word string, args []value.Value) string {
	written := "router." + word
	switch word {
	case "guard":
		return guardArgumentError(written, args)
	case "get", "post", "put", "patch", "delete":
		example := written + "((context) => …)"
		switch {
		case len(args) == 1 && lambdaOf(args[0]) != nil:
			return ""
		case len(args) == 0:
			return fmt.Sprintf("%s() thiếu handler, hàm trả lời request: %s", written, example)
		case len(args) > 1:
			return fmt.Sprintf("%s nhận một handler, không phải %d đối số: %s; luật của route nối sau nó, .guard(…), .limit(…), .cache(…), .persist(…)", written, len(args), example)
		}
		return fmt.Sprintf("%s: handler là một hàm: %s%s", called(written, args), example, nullHint(args[0]))
	case "proxy":
		if len(args) == 1 && (lambdaOf(args[0]) != nil || (args[0].IsString() && strings.TrimSpace(args[0].Text()) != "")) {
			return ""
		}
		return fmt.Sprintf(`%s: router.proxy nhận một URL hay một hàm trả về URL: router.proxy("https://cdn.example.com/logo.png")%s`, called(written, args), firstNullHint(args))
	case "sitemap", "rss", "robots", "llms", "llmsFull", "manifest":
		if len(args) == 0 {
			return "" // nothing is the default, as true is (router.robots(): allow all, the Sitemap line)
		}
		if len(args) == 1 {
			switch arg := args[0]; {
			case arg.K == value.Bool, arg.IsMap(), lambdaOf(arg) != nil:
				return ""
			case arg.IsString() && strings.TrimSpace(arg.Text()) != "":
				return assetDiskError(called(written, args), arg.Text())
			}
		}
		return fmt.Sprintf(`%s: %s nhận true (mặc định), đường dẫn một file, một object hay một hàm: %s(true)%s`, called(written, args), written, written, firstNullHint(args))
	case "meta":
		if len(args) == 1 && args[0].IsMap() {
			return ""
		}
		return fmt.Sprintf(`%s: router.meta nhận một object: router.meta({ title: "…", description: "…" })%s`, called(written, args), firstNullHint(args))
	case "jsonld":
		if len(args) == 1 && args[0].IsMap() {
			return ""
		}
		if len(args) == 1 && args[0].IsArray() {
			for index, node := range args[0].Array() {
				if !node.IsMap() {
					return fmt.Sprintf(`router.jsonld([…]): phần tử [%d] là %s, không phải object: mỗi phần tử là một object, router.jsonld([{ "@type": "Organization" }, { "@type": "WebSite" }])`, index, handed(node))
				}
			}
			return ""
		}
		return fmt.Sprintf(`%s: router.jsonld nhận một object hay một mảng object: router.jsonld({ "@type": "Organization", name: "…" })%s`, called(written, args), firstNullHint(args))
	case "language":
		if len(args) == 1 && args[0].IsString() && strings.TrimSpace(args[0].Text()) != "" {
			return ""
		}
		return fmt.Sprintf(`%s: router.language nhận một mã ngôn ngữ: router.language("vi")%s`, called(written, args), firstNullHint(args))
	case "favicon":
		if len(args) != 1 || !args[0].IsString() || strings.TrimSpace(args[0].Text()) == "" {
			return fmt.Sprintf(`%s: router.favicon nhận đường dẫn của icon: router.favicon("/assets/favicon.svg")%s`, called(written, args), firstNullHint(args))
		}
		if strings.Contains(args[0].Text(), "://") {
			return "" // an icon on another host is linked as written
		}
		return assetDiskError(called(written, args), args[0].Text())
	case "assets":
		switch {
		case len(args) == 1 && args[0].K == value.Bool:
			return "" // assets/ or _assets/ of the folder, or nothing
		case len(args) == 1 && args[0].IsString() && cleanAssetPrefix(args[0].Text()) != "":
			return assetDiskError(called(written, args), args[0].Text())
		case len(args) == 2 && args[0].IsString() && args[1].IsString() &&
			cleanAssetPrefix(args[0].Text()) != "" && cleanAssetPrefix(args[1].Text()) != "":
			return assetDiskError(called(written, args), args[1].Text())
		}
		return fmt.Sprintf(`%s: router.assets nhận một thư mục, hay đường dẫn công khai rồi thư mục: router.assets("_public"), router.assets("/tai-lieu/*", "_docs")%s`, called(written, args), firstNullHint(args))
	}
	return ""
}

// guardArgumentError is what is wrong with what a guard of a Kitwork UI router is handed (router.guard, the
// .guard of a route): one function, or one array of functions, each run in order; "" when it reads.
func guardArgumentError(written string, args []value.Value) string {
	example := written + "((context) => …)"
	switch {
	case len(args) == 0:
		return fmt.Sprintf("%s() thiếu guard: %s nhận một hàm hay một mảng hàm, %s", written, written, example)
	case len(args) > 1:
		return fmt.Sprintf("%s nhận một hàm hay một mảng hàm, không phải %d đối số: %s([authenticated, administrator])", written, len(args), written)
	case args[0].IsArray():
		for index, guard := range args[0].Array() {
			if lambdaOf(guard) == nil {
				return fmt.Sprintf("%s([…]): phần tử [%d] là %s, không phải hàm: mỗi guard là một hàm, %s([authenticated, administrator])%s", written, index, handed(guard), written, nullHint(guard))
			}
		}
		return ""
	case lambdaOf(args[0]) != nil:
		return ""
	}
	return fmt.Sprintf("%s: guard là một hàm hay một mảng hàm: %s%s", called(written, args), example, nullHint(args[0]))
}

// assetDiskError is what is wrong with a file or folder a Kitwork UI router publishes on disk (router.assets,
// a built-in door's file, router.favicon), "" when nothing is: a dot folder or file (.env, .database/,
// .persist/) and a source of the site (.work, .ui, *.kitwork.*) are never served (KITWORK_UI.md mục 5),
// whatever public path names them. serveTreeStatic reads the same rule where the file is read (uiDiskServed).
func assetDiskError(written, disk string) string {
	clean := cleanAssetPrefix(disk)
	for _, segment := range strings.Split(clean, "/") {
		if strings.HasPrefix(segment, ".") {
			return fmt.Sprintf(`%s: "%s" là file hay thư mục dấu chấm (.env, .database/, .persist/…), không bao giờ phục vụ: file công khai đặt trong assets/ hay một thư mục khác; .well-known/ ở gốc site phục vụ sẵn`, written, disk)
		}
	}
	if isUISource(clean) || strings.Contains(strings.ToLower(clean), extension+".") {
		return fmt.Sprintf(`%s: "%s" là mã nguồn của site (.work, .ui, .kitwork.*), không bao giờ phục vụ`, written, disk)
	}
	return ""
}

// called is a call as a message writes it back: router.meta("Quán"), router.guard(), router.get(…).
func called(written string, args []value.Value) string {
	switch len(args) {
	case 0:
		return written + "()"
	case 1:
		return written + "(" + handed(args[0]) + ")"
	}
	return written + "(…)"
}

// handed is a value a word was handed as a message names it: a text in quotes, a number or a truth as
// written, null, and an object, an array or a function by its kind.
func handed(v value.Value) string {
	switch {
	case v.K == value.Nil || v.K == value.Invalid:
		return "null"
	case v.IsString():
		return fmt.Sprintf("%q", v.Text())
	case v.IsMap():
		return "{…}"
	case v.IsArray():
		return "[…]"
	case v.K == value.Func:
		return "một hàm"
	}
	return v.Text()
}

// nullHint says what a null handed to the router most often is: a name with no value, written wrong or not
// exported by the file it is imported from.
func nullHint(v value.Value) string {
	if v.K != value.Nil && v.K != value.Invalid {
		return ""
	}
	return "; null thường là một tên không có giá trị: viết sai, hay file của nó không export"
}

// firstNullHint is nullHint of the one value a word was handed.
func firstNullHint(args []value.Value) string {
	if len(args) != 1 {
		return ""
	}
	return nullHint(args[0])
}

// routerCallResult is what a folder call gives: nothing to chain or to call, each call is a line of its own
// (KITWORK_ROUTER.md mục 4).
type routerCallResult struct {
	word string
	// module is "design" for a call of design.work (router_design.go), "" for router.<word>
	module string
}

// chained is the error of next chained after the call.
func (r *routerCallResult) chained(next string) value.Value {
	if r.module == "design" {
		return routerFailure(compiler.DesignCallChained(r.word, next))
	}
	return routerFailure(compiler.RouterCallChained(r.word, next))
}

func (r *routerCallResult) OnGet(key string) value.Value {
	return r.chained("." + key)
}

func (r *routerCallResult) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (r *routerCallResult) OnInvoke(method string, _ ...value.Value) value.Value {
	if method == "" {
		return r.chained("(…)")
	}
	return r.chained("." + method + "(…)")
}

// uiDoor is a door of a Kitwork UI folder as its router.work chains it: the four rules of a route and nothing
// else (KITWORK_ROUTER.md mục 3); guard and limit as often as written, cache and persist once.
type uiDoor struct {
	method *FolderMethod
	door   string
	once   map[string]runtime.SourceLocation
}

// Member answers every word chained after the door, as the compiler says it.
func (d *uiDoor) Member(name string) (value.Value, bool) {
	if removed, ok := compiler.RouteRuleRemoved(d.door, name); ok {
		return routerFailure(removed), true
	}
	rule, ok := compiler.RouteRuleOf(name)
	if !ok {
		return routerFailure(compiler.RouteRuleUnknown(d.door, name)), true
	}
	return value.NewFunc(func(args ...value.Value) value.Value { return d.chain(rule, args) }), true
}

// chain adds one rule to the route.
func (d *uiDoor) chain(rule compiler.RouterWord, args []value.Value) value.Value {
	folder := d.method.folder
	at := folder.site()
	if !rule.Repeats {
		if first, twice := d.once[rule.Word]; twice {
			return routerFailure(compiler.RouteRuleTwice(d.door, rule.Word, sourcePlace(first), sourcePlace(at)))
		}
		d.once[rule.Word] = at
	}
	switch rule.Word {
	case "guard":
		if wrong := guardArgumentError("router."+d.door+"(…).guard", args); wrong != "" {
			return routerFailure(wrong)
		}
		d.method.Guard(args...)
	case "limit":
		limit, err := limitRule("router."+d.door+"(…).limit", args)
		if err != nil {
			return routerFailure(err.Error())
		}
		folder.checkLimitBy(limit.Dim)
		d.method.limits = append(d.method.limits, limit)
	case "cache":
		d.method.Cache(args...)
	case "persist":
		d.method.Persist(args...)
	}
	return value.New(d)
}

// sourcePlace is a place the VM gives as an error writes it, file:line:column.
func sourcePlace(at runtime.SourceLocation) string {
	if at.File == "" {
		return fmt.Sprintf("%d:%d", at.Line, at.Column)
	}
	return fmt.Sprintf("%s:%d:%d", at.File, at.Line, at.Column)
}

// routerSeed is one call of the app's config.work as the app's collector heard it: the word, what it was
// handed, and where it is written.
type routerSeed struct {
	word string
	args []value.Value
	at   runtime.SourceLocation
	// file is the app's file the call is written in: design.work or config.work
	file string
}

// seed starts a site's root from the app's config.work (KITWORK_ROUTER.md mục 4, 24/10/2026): each call it
// made, in written order, is made again on the root before the root's own files run, so a site's call adds
// over the app's (colors per token, fonts per role, gradients per name, highlight per kind) or wins over it
// (language, appearance, contrast, transition, bundle, favicon), as a branch's does over its site's. A seed
// is the parent's, not the root's own call: a nearest-wins word the site writes once more is no call twice.
// A call the root cannot take stops the site's generation where the app's file writes it.
func (f *FolderRouter) seed(app *FolderRouter) error {
	defer func() { f.callSite = nil }()
	for _, call := range app.seeds {
		file := call.file
		if file == "" {
			file = compiler.RouterConfigFile
		}
		at := call.at
		at.File = "app/" + file // where router.highlight says a kind's colour is set (place)
		f.callSite = func() runtime.SourceLocation { return at }
		if result := f.apply(call.word, call.args); result.K == value.Invalid {
			message := result.Text()
			if file == compiler.DesignFile {
				message = designSpelling(message)
			}
			return fmt.Errorf("initialize %s app/%s: %s:%d:%d: %s", routerFileKind(file), file, file, call.at.Line, call.at.Column, message)
		}
	}
	return nil
}

// appRouterConfig hears the config.work at the app's folder, the parent of every site's root
// (KITWORK_ROUTER.md mục 4, 24/10/2026): compiled and run once for this tenant, into a collector of its own
// that keeps each call for a site's root to start from (seed) and router.timezone for the app. appRoot says
// the app's folder is a site's root too, so the file takes a site's words as well. The collector is nil when
// the app has no config.work; files are what the tenant watches for it, the file itself even when absent.
func (t *Tenant) appRouterConfig(appRoot bool) (*FolderRouter, []string, error) {
	return t.appRouterFiles(appRoot, compiler.RouterConfigFile)
}

// appRouterFiles hears the files of the app's folder named, in order, into one collector (appRouterConfig): a
// site's root hears the app's design.work, then its config.work (KITWORK_DESIGN.md mục 2: the app's look is the
// parent of every site's), so a word whose nearest folder wins written in both is the error naming both. The
// collector is nil when the app has none of them; files are what the tenant watches, each file even when absent.
func (t *Tenant) appRouterFiles(appRoot bool, names ...string) (*FolderRouter, []string, error) {
	var files []string
	var collector *FolderRouter
	for _, name := range names {
		file := t.resolveApp(name)
		files = append(files, file)
		if !fileExists(file) {
			continue
		}
		// app/ is the app's folder as an import writes it (KITWORK_UI.md mục 4), app/ for one app,
		// apps/<identity>/ for many
		kind, written := routerFileKind(name), "app/"+name
		bc, err := t.compileFile(file)
		if err != nil {
			return nil, files, fmt.Errorf("compile %s %s: %w", kind, written, err)
		}
		files = append(files, bc.Files...)
		if missing := t.missingEnvironment(bc); missing != nil {
			missing.placeEntry(name, written)
			return nil, files, missing
		}
		if collector == nil {
			node := &RouteNode{base: t.resolveApp()}
			collector = &FolderRouter{
				tenant: t, node: node,
				methods: map[string]*FolderMethod{}, outputs: map[string]*FolderMethod{}, meta: map[string]value.Value{},
				ui: true, app: true, appRoot: appRoot,
			}
			node.folder = collector
		}
		collector.running = name
		err = runFolderRouter(t, collector.node, bc)
		collector.running = compiler.RouterConfigFile
		if err != nil {
			return nil, files, fmt.Errorf("initialize %s %s: %w", kind, written, err)
		}
	}
	return collector, files, nil
}

// appTimezone is the app's zone for its schedules (KITWORK_SCHEDULE.md mục 5): router.timezone(…) of the
// config.work at the app's folder; declared is false when the app writes none, and the zone UTC. A file that
// does not compile or run is said in one line and failed is true: the zone it would declare is not known, so
// the schedules that count in it wait for the fix as those of a file that does not load (LoadScheduleFiles),
// never counted in UTC meanwhile, which would store another rule for each and choose a once() again. The app's
// tenant is built once per process and watches nothing (core's StartAppSchedulers), so the file is read here
// once, at start: a fix, or another router.timezone, reaches the clock when the app starts again, while the
// sites, which watch it, move their route caches at once.
func (t *Tenant) appTimezone() (zone *time.Location, declared bool, failed bool) {
	collector, _, err := t.appRouterConfig(t.appFolderIsASite())
	if err != nil {
		// one line: the error and its place, not the frames the VM adds under it
		said, _, _ := strings.Cut(err.Error(), "\n")
		fmt.Printf("[Schedule] %s; lịch không viết .timezone(…) đếm theo múi giờ của app, nên chờ, không tới giờ, tới khi file này chạy được và app khởi động lại (đồng hồ của app đọc file này một lần, lúc khởi động)\n", said)
		return scheduleDefaultLocation, false, true
	}
	if collector == nil || collector.timezone == nil {
		return scheduleDefaultLocation, false, false
	}
	return collector.timezone, true, false
}

// appFolderIsASite reports, in the tenant of an app's clock, whether the app's folder is a site's root too, as
// a site's tenant reads it (compileUIFolder, sameFolder): always in the layout of one app with one site
// (RootLayoutSingle, where the site's folder is the app's), elsewhere when the folder carries a router file.
// Its config.work then takes a site's words beside the app's.
func (t *Tenant) appFolderIsASite() bool {
	return t.config.layout == RootLayoutSingle || RouterMarker(t.resolveApp()) != ""
}

// routeCacheClock is the clock a route's cache reads its boundaries from ("hourly", "nextday 03:00"): a
// test's way to stand at an hour.
var routeCacheClock = time.Now

// routeNow is the time a route's cache counts its boundaries from (KITWORK_ROUTER.md mục 9): in a Kitwork
// UI site the app's zone, router.timezone(…) of the app's config.work, UTC when it writes none; a template
// site keeps the server's clock.
func (t *Tenant) routeNow() time.Time {
	now := routeCacheClock()
	tree := t.routeTree()
	if tree == nil || !tree.ui {
		return now
	}
	zone := time.UTC
	if root := tree.root; root != nil && root.folder != nil && root.folder.timezone != nil {
		zone = root.folder.timezone
	}
	return now.In(zone)
}
