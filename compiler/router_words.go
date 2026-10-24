package compiler

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The vocabulary of kitwork/router is closed (KITWORK_ROUTER.md mục 3, 4, 11; 24/10/2026): the doors a
// router.work declares, the four rules a door chains, and the calls of a branch in the table of mục 4. Any
// other word is an error with the nearest word; an old word is an error naming the new spelling. One table
// here serves the compiler, which reads the lines a file writes (router_rules.go), and the engine, which runs
// them and checks what a file cannot tell (the level of a folder, a call twice across config.work and
// router.work).

// RouterWordKind is what a word of the router is.
type RouterWordKind uint8

const (
	// RouterDoor is a method: router.get(…) declares a route of the folder.
	RouterDoor RouterWordKind = iota + 1
	// RouterBuiltIn is a route the engine writes at a fixed path of the site's root: router.sitemap(…).
	RouterBuiltIn
	// RouterFolderCall is a rule of the branch: router.guard(…), router.colors(…).
	RouterFolderCall
	// RouterRouteRule is a rule chained after a door: router.get(…).guard(…).
	RouterRouteRule
)

// RouterInheritance is how a folder call meets the same call of the folders above it.
type RouterInheritance uint8

const (
	// RouterAccumulates: each call is merged or added, parents first (guard, colors…); called as often as
	// needed.
	RouterAccumulates RouterInheritance = iota + 1
	// RouterNearestWins: the nearest folder's call is the one (language, reload…); once per folder.
	RouterNearestWins
)

// RouterLevel is a place a router word is written: the app's folder (its config.work), a site's root, a
// branch of a site. A word takes a set of them.
type RouterLevel uint8

const (
	RouterApp RouterLevel = 1 << iota
	RouterSite
	RouterBranch
)

// RouterWord is one word of the router.
type RouterWord struct {
	Word string
	Kind RouterWordKind
	// Inheritance is a folder call's; 0 for any other word.
	Inheritance RouterInheritance
	// Levels are where it may be written: a door in a router.work of a site or a branch, a built-in door at
	// the site's root only, a folder call as the table of mục 4 says. 0 for a route rule.
	Levels RouterLevel
	// Repeats: a route rule written more than once on one route runs each time, in written order (guard,
	// limit); cache and persist are once per route.
	Repeats bool
}

const everywhere = RouterApp | RouterSite | RouterBranch

// routerWords are the words a line begins with, router.<word>(…), in the order of the spec.
var routerWords = []RouterWord{
	{Word: "get", Kind: RouterDoor, Levels: RouterSite | RouterBranch},
	{Word: "post", Kind: RouterDoor, Levels: RouterSite | RouterBranch},
	{Word: "put", Kind: RouterDoor, Levels: RouterSite | RouterBranch},
	{Word: "patch", Kind: RouterDoor, Levels: RouterSite | RouterBranch},
	{Word: "delete", Kind: RouterDoor, Levels: RouterSite | RouterBranch},
	{Word: "proxy", Kind: RouterDoor, Levels: RouterSite | RouterBranch},
	{Word: "sitemap", Kind: RouterBuiltIn, Levels: RouterSite},
	{Word: "rss", Kind: RouterBuiltIn, Levels: RouterSite},
	{Word: "robots", Kind: RouterBuiltIn, Levels: RouterSite},
	{Word: "llms", Kind: RouterBuiltIn, Levels: RouterSite},
	{Word: "llmsFull", Kind: RouterBuiltIn, Levels: RouterSite},
	{Word: "manifest", Kind: RouterBuiltIn, Levels: RouterSite},
	{Word: "timezone", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: RouterApp},
	{Word: "language", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: everywhere},
	{Word: "colors", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: everywhere},
	{Word: "fonts", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: everywhere},
	{Word: "gradients", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: everywhere},
	{Word: "appearance", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: everywhere},
	{Word: "contrast", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: everywhere},
	{Word: "transition", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: everywhere},
	{Word: "highlight", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: everywhere},
	{Word: "bundle", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: everywhere},
	{Word: "favicon", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: everywhere},
	{Word: "meta", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: RouterSite | RouterBranch},
	{Word: "jsonld", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: RouterSite | RouterBranch},
	{Word: "guard", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: RouterSite | RouterBranch},
	{Word: "limit", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: RouterSite | RouterBranch},
	{Word: "cors", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: RouterSite | RouterBranch},
	{Word: "assets", Kind: RouterFolderCall, Inheritance: RouterAccumulates, Levels: RouterSite | RouterBranch},
	{Word: "reload", Kind: RouterFolderCall, Inheritance: RouterNearestWins, Levels: RouterSite},
}

// routeRules are the words a door chains, router.get(…).<word>(…).
var routeRules = []RouterWord{
	{Word: "guard", Kind: RouterRouteRule, Repeats: true},
	{Word: "limit", Kind: RouterRouteRule, Repeats: true},
	{Word: "cache", Kind: RouterRouteRule},
	{Word: "persist", Kind: RouterRouteRule},
}

// RouterWordOf is the word a line begins with, router.<word>(…): a door, a built-in door or a folder call.
// Words are compared as written: router.GET is no word (the engine's reflection would read it as get).
func RouterWordOf(word string) (RouterWord, bool) {
	for _, each := range routerWords {
		if each.Word == word {
			return each, true
		}
	}
	return RouterWord{}, false
}

// RouteRuleOf is a word a door chains: guard, limit, cache, persist.
func RouteRuleOf(word string) (RouterWord, bool) {
	for _, each := range routeRules {
		if each.Word == word {
			return each, true
		}
	}
	return RouterWord{}, false
}

// RouterWords are the words of kind, in the order of the spec.
func RouterWords(kind RouterWordKind) []string {
	var words []string
	if kind == RouterRouteRule {
		for _, each := range routeRules {
			words = append(words, each.Word)
		}
		return words
	}
	for _, each := range routerWords {
		if each.Kind == kind {
			words = append(words, each.Word)
		}
	}
	return words
}

// Takes reports whether the word may be written at level.
func (word RouterWord) Takes(level RouterLevel) bool {
	return word.Levels&level != 0
}

// RouterConnectionRemoved and RouterConnectRemoved are the errors of router.connection(…) and
// router.connect(…), removed on 22/10/2026 (work/router_connection.go says the same where a router runs).
const (
	RouterConnectionRemoved = `router.connection(…) đã bỏ: database có tên khai export const warehouse = connection("warehouse").schema({ bảng }) trong app/.shared/schema/<tên>.work, tên đó khai trong app.databases({ … }) của application.work; database của app là connection() không tên`
	RouterConnectRemoved    = `router.connect(…) đã bỏ: database ngoài khai export const crm = connect({ postgresql: environment.CRM_URL }) trong app/.shared/schema/<tên>.work, rồi crm.query(sql, [giá trị])`
)

// routerRemoved are the old words a line began with, router.<word>(…), and what to write instead.
var routerRemoved = map[string]string{
	"ratelimit":   "router.ratelimit đã đổi tên (24/10/2026): giới hạn tần suất của nhánh là router.limit({ rate, per, by })",
	"rateLimit":   "router.rateLimit đã đổi tên (24/10/2026): giới hạn tần suất của nhánh là router.limit({ rate, per, by })",
	"title":       "router.title không có: metadata mặc định của nhánh viết một chỗ, router.meta({ title, description, image, … })",
	"description": "router.description không có: metadata mặc định của nhánh viết một chỗ, router.meta({ title, description, image, … })",
	"image":       "router.image không có: metadata mặc định của nhánh viết một chỗ, router.meta({ title, description, image, … })",
	"url":         "router.url không có: metadata mặc định của nhánh viết một chỗ, router.meta({ url, … })",
	"type":        `router.type không có: metadata mặc định của nhánh viết một chỗ, router.meta({ type: "article", … })`,
	"theme":       "router.theme đã bỏ: bảng màu là router.colors({ … }), phông chữ là router.fonts({ … })",
	"themes":      "router.themes đã bỏ: bảng màu là router.colors({ … }), phông chữ là router.fonts({ … })",
	"jittheme":    "router.jittheme đã bỏ: bảng màu là router.colors({ … }), phông chữ là router.fonts({ … })",
	"jitTheme":    "router.jitTheme đã bỏ: bảng màu là router.colors({ … }), phông chữ là router.fonts({ … })",
	"css":         "router.css đã bỏ trong router.work: bảng màu là router.colors({ … }), phông chữ là router.fonts({ … })",
	"jitcss":      "router.jitcss đã bỏ trong router.work: bảng màu là router.colors({ … }), phông chữ là router.fonts({ … })",
	"middleware":  "router.middleware đã bỏ: guard của nhánh là router.guard(…)",
	"notfound":    "router.notfound đã bỏ: trang 404 là notfound.ui gần nhất, route trả context.notFound()",
	"notFound":    "router.notFound không có: trang 404 là notfound.ui gần nhất, route trả context.notFound()",
	"error":       "router.error đã bỏ (24/10/2026): lỗi của route vẽ error.ui gần nhất; ghi log hay dọn dẹp viết trong handler",
	"cache":       `router.cache đã bỏ (24/10/2026): cache viết trên từng route, router.get(…).cache("5m")`,
	"headers":     "router.headers đã bỏ (24/10/2026): header viết trên câu trả lời, context.header(…)",
	"redirect":    `router.redirect đã bỏ (24/10/2026): đường dẫn cũ là một thư mục có router.get((context) => context.status(301).redirect("/mới"))`,
	"drive":       "router.drive đã bỏ (11/10/2026): chuyển trang không tải lại là router.reload(false)",
	"javascript":  "router.javascript đã bỏ (11/10/2026): chuyển trang không tải lại là router.reload(false), component tìm theo tên",
	"jitjs":       "router.jitjs đã bỏ (11/10/2026): chuyển trang không tải lại là router.reload(false), component tìm theo tên",
	"connection":  RouterConnectionRemoved,
	"connect":     RouterConnectRemoved,
	"view":        RouterViewRemoved,
}

// RouterRemovedWord is the error of an old word a line begins with (router.ratelimit), naming the new
// spelling; false for any other word.
func RouterRemovedWord(word string) (string, bool) {
	message, ok := routerRemoved[word]
	return message, ok
}

// RouteRuleRemoved is the error of an old word chained after a door (router.get(…).error(…)), naming what to
// write instead; false for any other word.
func RouteRuleRemoved(door, word string) (string, bool) {
	after := fmt.Sprintf(".%s(…) sau router.%s(…)", word, door)
	switch word {
	case "ratelimit", "rateLimit":
		return after + " đã đổi tên (24/10/2026): trần tần suất của route là .limit({ rate, per, by })", true
	case "error", "success", "finally", "then", "catch":
		return after + " đã bỏ (24/10/2026): lỗi của route vẽ error.ui gần nhất; ghi log hay dọn dẹp viết trong handler", true
	case "middleware":
		return after + " đã bỏ: guard của route là .guard(…)", true
	case "view":
		return after + ` đã bỏ: trang là câu trả lời của handler, router.get((context) => context.title("…").view({ … }))`, true
	case "handle":
		return after + " đã bỏ: handler là tham số của cửa, router." + door + "((context) => …)", true
	}
	return "", false
}

// RouterUnknownWord is the error of a line that begins with a word the router does not have, with the nearest
// word of the file's set: a config.work writes only the calls of a branch.
func RouterUnknownWord(word string, config bool) string {
	words := RouterWords(RouterFolderCall)
	if !config {
		words = append(append(RouterWords(RouterDoor), RouterWords(RouterBuiltIn)...), words...)
	}
	if nearest := RouterNearestWord(word, words); nearest != "" {
		return fmt.Sprintf("router.%s không có; ý là router.%s?", word, nearest)
	}
	return fmt.Sprintf("router.%s không có; router có %s (KITWORK_ROUTER.md mục 3, 4)", word, strings.Join(words, ", "))
}

// RouteRuleUnknown is the error of a word chained after a door that is none of the four rules of a route.
func RouteRuleUnknown(door, word string) string {
	if nearest := RouterNearestWord(word, RouterWords(RouterRouteRule)); nearest != "" {
		return fmt.Sprintf(".%s không nối sau router.%s(…); ý là .%s?", word, door, nearest)
	}
	return fmt.Sprintf(".%s không nối sau router.%s(…): sau cửa chỉ nối .guard(…), .limit(…), .cache(…), .persist(…)", word, door)
}

// RouteRuleNotACall is the error of a door chained with what is not a call of a rule: a property
// (router.get(…).cache), a call of what it gave (router.get(…)(…)), an item (router.get(…)[…]).
func RouteRuleNotACall(door, written string) string {
	return fmt.Sprintf("router.%s(…)%s: sau cửa chỉ nối lời gọi .guard(…), .limit(…), .cache(…), .persist(…)", door, written)
}

// RouteRuleTwice is the error of cache or persist chained twice on one route; first and second are the
// places of the two.
func RouteRuleTwice(door, word, first, second string) string {
	return fmt.Sprintf(".%s nối hai lần sau router.%s(…), ở %s và %s: mỗi route một .%s(…), giữ một", word, door, first, second, word)
}

// RouterCallChained is the error of something chained after a folder call: it gives nothing to chain or to
// call, each call is a line of its own. next is what follows, as written: ".get(…)", ".x", "(…)", "[…]".
func RouterCallChained(word, next string) string {
	if strings.HasPrefix(next, ".") && strings.HasSuffix(next, "(…)") {
		return fmt.Sprintf("router.%s(…)%s nối tiếp: lời gọi cấp thư mục không trả gì để nối; tách thành hai dòng: router.%s(…); router%s;",
			word, next, word, next)
	}
	return fmt.Sprintf("router.%s(…)%s: lời gọi cấp thư mục không trả gì để nối hay gọi; tách thành hai dòng, mỗi lời gọi một dòng: router.%s(…);",
		word, next, word)
}

// RouterCallTwice is the error of a call whose nearest folder wins (router.language) written twice for one
// folder; first and second are the places of the two ("dòng 3", "config.work:2").
func RouterCallTwice(word, first, second string) string {
	return fmt.Sprintf("router.%s gọi hai lần, %s và %s: thư mục gần nhất thắng nên mỗi thư mục một router.%s(…); giữ một dòng",
		word, first, second, word)
}

// RouterDoorInConfig is the error of a door declared in a config.work, which holds only the rules of the
// branch.
func RouterDoorInConfig(word string) string {
	return fmt.Sprintf("router.%s(…) trong config.work: cửa khai trong router.work; config.work chỉ chứa luật của nhánh (router.guard(…), router.colors(…)…)", word)
}

// RouterWrongLevel is the error of a word written where its level does not take it; where is that place in
// the author's words ("router.work", "config.work của một nhánh").
func RouterWrongLevel(word, where string) string {
	return fmt.Sprintf("router.%s chỉ viết %s, không trong %s (KITWORK_ROUTER.md mục 4)", word, routerLevelsText(word), where)
}

// routerLevelsText says where a word is written.
func routerLevelsText(word string) string {
	known, _ := RouterWordOf(word)
	switch {
	case known.Levels == RouterApp:
		return "trong config.work của gốc app"
	case known.Kind == RouterBuiltIn:
		return "trong router.work ở gốc site"
	case known.Levels == RouterSite:
		return "ở gốc site"
	case known.Kind == RouterDoor:
		return "trong router.work của site và nhánh"
	case known.Levels == RouterSite|RouterBranch:
		return "ở gốc site và trong nhánh, không ở gốc app"
	}
	return "ở gốc app, gốc site và trong nhánh"
}

// RouterNearestWord is the word of words nearest to written: the same word in other case first (the VM reads
// router.GET as router.get, so the compiler names it), then the fewest edits, two at most (one for a word of
// four letters or fewer); "" when none is that near.
func RouterNearestWord(written string, words []string) string {
	for _, word := range words {
		if strings.EqualFold(word, written) {
			return word
		}
	}
	limit := 2
	if utf8.RuneCountInString(written) <= 4 {
		limit = 1
	}
	nearest, distance := "", limit+1
	for _, word := range words {
		if edits := wordDistance(strings.ToLower(written), strings.ToLower(word), limit); edits < distance {
			nearest, distance = word, edits
		}
	}
	return nearest
}

// wordDistance counts the edits (insert, delete, replace, swap two neighbours) between two short words,
// limit+1 as soon as it is past limit.
func wordDistance(left, right string, limit int) int {
	first, second := []rune(left), []rune(right)
	if difference := len(first) - len(second); difference > limit || -difference > limit {
		return limit + 1
	}
	twoBack := make([]int, len(second)+1)
	oneBack := make([]int, len(second)+1)
	current := make([]int, len(second)+1)
	for column := range oneBack {
		oneBack[column] = column
	}
	for row := 1; row <= len(first); row++ {
		current[0] = row
		smallest := current[0]
		for column := 1; column <= len(second); column++ {
			cost := 1
			if first[row-1] == second[column-1] {
				cost = 0
			}
			edits := min(oneBack[column]+1, current[column-1]+1, oneBack[column-1]+cost)
			if row > 1 && column > 1 && first[row-1] == second[column-2] && first[row-2] == second[column-1] {
				edits = min(edits, twoBack[column-2]+1)
			}
			current[column] = edits
			smallest = min(smallest, edits)
		}
		if smallest > limit {
			return limit + 1
		}
		twoBack, oneBack, current = oneBack, current, twoBack
	}
	return oneBack[len(second)]
}
