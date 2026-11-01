package statement

import (
	"sort"
	"strings"
)

// Builder is a column builder of kitwork/schema.
type Builder struct {
	// Kind is the column's kind in the spec's words (the builder's name).
	Kind string
	// Primary: id(), the table's key, which the engine generates.
	Primary bool
	// Timestamp: the only kind .created() and .updated() take.
	Timestamp bool
	// Parameters is how many numbers the builder takes: decimal(12, 2) takes two.
	Parameters int
}

// Builders are the column builders of kitwork/schema the engine has today (KITWORK_DATABASE.md mục 1).
var Builders = map[string]Builder{
	"id":        {Kind: "id", Primary: true},
	"text":      {Kind: "text"},
	"integer":   {Kind: "integer"},
	"float":     {Kind: "float"},
	"decimal":   {Kind: "decimal", Parameters: 2},
	"boolean":   {Kind: "boolean"},
	"date":      {Kind: "date"},
	"json":      {Kind: "json"},
	"timestamp": {Kind: "timestamp", Timestamp: true},
}

// BuildersNotYet are builders of the spec the engine does not have yet, with the slice that brings them.
var BuildersNotYet = map[string]int{
	"bigint":   9,
	"computed": 9,
}

// Modifier is a modifier of a column (text().nullable()).
type Modifier struct {
	// OnlyTimestamp: valid only after timestamp().
	OnlyTimestamp bool
}

// Modifiers are the column modifiers of the spec.
var Modifiers = map[string]Modifier{
	"nullable":   {},
	"unique":     {},
	"default":    {},
	"searchable": {},
	"index":      {},
	"created":    {OnlyTimestamp: true},
	"updated":    {OnlyTimestamp: true},
}

// LegacyBuilders are builders of the older spellings, with what the spec writes instead.
var LegacyBuilders = map[string]string{
	"kitid":    "viết id()",
	"int":      "viết integer()",
	"bool":     "viết boolean()",
	"real":     "viết float()",
	"double":   "viết float()",
	"jsonb":    "viết json()",
	"datetime": "viết timestamp()",
	"now":      "viết timestamp().created()",
	"updated":  "viết timestamp().updated()",
	"ref":      "khoá ngoại viết userId: users.id (chưa có trong engine hôm nay, lát 5)",
}

// NumberBuilder is the error of number(), which the spec has not (KITWORK_DATABASE.md mục 1).
const NumberBuilder = "number() không có (KITWORK_DATABASE.md mục 1): viết integer(), float() hay decimal(12, 2); có giữ number() làm tên tiện hay không còn mở"

// LegacyModifiers are modifiers of the older spellings, with what the spec writes instead.
var LegacyModifiers = map[string]string{
	"notNull":    "cột bắt buộc là mặc định; cột được bỏ trống viết .nullable()",
	"search":     "viết .searchable(): search là phương thức của chuỗi JavaScript",
	"primaryKey": "viết id()",
	"key":        "viết id()",
	"defaultNow": "viết timestamp().created()",
	"onUpdate":   "viết timestamp().updated()",
}

// Actions are the actions of a statement the engine runs today; Descriptions the descriptions before them.
var (
	Actions      = map[string]bool{ActionList: true, ActionFirst: true, ActionCount: true, ActionInsert: true}
	Descriptions = map[string]bool{"where": true, "orderBy": true}
)

// NotYet are words of the spec's statement the engine does not have yet, with the slice that brings them.
var NotYet = map[string]int{
	"detail": 3, "exists": 3, "sum": 3, "avg": 3, "min": 3, "max": 3,
	"update": 3, "delete": 3, "upsert": 3,
	"transaction": 4,
	"select":      5, "join": 5, "groupBy": 5,
	"at":      9,
	"history": 9,
}

// ManagedQuery is the error of query() on a database the app declared with connection(): the spec lets it
// run only when application.work allows it, and how application.work allows it is open
// (KITWORK_DATABASE.md mục 8.9), so the engine has no way to allow it yet.
const ManagedQuery = "query(…) trên database được quản lý chỉ chạy khi application.work cho phép, và cách cho phép còn mở (KITWORK_DATABASE.md mục 8.9): chưa có trong engine hôm nay; SQL thô trên database ngoài là connect({ … }).query(sql, [giá trị])"

// Absent are words the spec has not, with what it writes instead (KITWORK_DATABASE.md mục 9).
var Absent = map[string]string{
	"find":      "db.first((bảng) => bảng.id === id)",
	"get":       "db.first((bảng) => bảng.id === id)",
	"save":      "db.insert((bảng) => ({ … })) hay db.upsert(…)",
	"create":    "db.insert((bảng) => ({ … }))",
	"remove":    "db.delete((bảng) => …)",
	"set":       "db.where((bảng) => …).update((bảng) => ({ … }))",
	"table":     "db.list((bảng) => …): tham số của lambda là bảng",
	"offset":    "list({ page, limit })",
	"skip":      "list({ page, limit })",
	"take":      "list(n)",
	"limit":     "list(n)",
	"or":        "|| trong lambda",
	"whereIn":   "bảng.cột === one(mảng)",
	"notLike":   "bảng.cột !== like(mẫu)",
	"leftJoin":  "so cả bảng với null: (orders, payments) => payments === null",
	"innerJoin": "join((a, b) => …)",
	"rightJoin": "join((a, b) => …), bảng chính đứng trước",
	"with":      "select((users, orders) => ({ ...users, orders: [orders] }))",
	"include":   "select((users, orders) => ({ ...users, orders: [orders] }))",
	"having":    "where sau groupBy",
	"execute":   "không cần: hành động ở cuối chạy câu",
	"all":       "db.list((bảng) => bảng)",
}

// LambdaWords are the words of kitwork/database that mean something only inside a statement's lambda
// (KITWORK_DATABASE.md mục 2).
var LambdaWords = map[string]bool{
	"desc": true, "asc": true, "one": true, "like": true, "search": true,
	"sum": true, "count": true, "avg": true, "min": true, "max": true,
}

// LambdaWordsNotYet are lambda words the engine does not translate yet, with their slice.
var LambdaWordsNotYet = map[string]int{
	"like": 2, "search": 2,
	"sum": 5, "count": 5, "avg": 5, "min": 5, "max": 5,
}

// AfterAction reports a word that continues a statement, which nothing may follow its action with:
// db.list(…).where(…). A method of the result (map, join, at of an array) is ordinary code.
func AfterAction(word string) bool {
	if word == "join" || word == "at" {
		return false
	}
	_, notYet := NotYet[word]
	return Actions[word] || Descriptions[word] || notYet
}

// RequiredByDefault reports a column that refuses null: every column, unless .nullable() (KITWORK_DATABASE.md
// mục 1). Whether an insert must write it also depends on a default and on what the engine fills
// (Column.Required).
func RequiredByDefault(c Column) bool {
	return !c.Nullable
}

// Words lists the words of a statement the engine has, for an error that lists them.
func Words() string {
	words := make([]string, 0, len(Actions)+len(Descriptions))
	for word := range Descriptions {
		words = append(words, word)
	}
	sort.Strings(words)
	actions := make([]string, 0, len(Actions))
	for word := range Actions {
		actions = append(actions, word)
	}
	sort.Strings(actions)
	return strings.Join(append(words, actions...), ", ")
}
