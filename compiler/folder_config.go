package compiler

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// The config.work of .shared/queue/ and of .schedule/ (KITWORK_QUEUE.md mục 2, KITWORK_SCHEDULE.md mục 2,
// 24/10/2026) configures its folder with calls on the module's namespace, one call per line at the file's top
// level, never chained, as a branch's rules are router.<name>(…) in a site's config.work (KITWORK_ROUTER.md
// mục 4) and the manifest's are app.<name>(…):
//
//	// app/.shared/queue/config.work
//	import * as queue from "kitwork/queue";
//	queue.parallel(4);
//	queue.keep("30d");
//
// CompileConfigFileInApp reads the file's own statements before anything else (readFolderCalls): each call
// written right is kept in Program.FolderCalls, and the resolver refuses the same words anywhere else
// (queueMember, scheduleWord). The const of 17/10 (const parallel = 4), a chain, and a word called twice are
// errors here. The loaders (work/background_config.go) run the file and read what each call was handed.

// folderWord is a call a folder's config.work makes: the word, what it sets, and the call as an error
// writes it.
type folderWord struct {
	word, meaning, example string
}

// folderWords are the calls of each folder's config.work, by the module whose folder it configures.
var folderWords = map[string][]folderWord{
	"queue": {
		{"parallel", "số lần chạy cùng lúc trên một máy", "queue.parallel(4)"},
		{"keep", "thời gian giữ lần chạy đã xong", `queue.keep("30d")`},
		{"database", "database của các lần chạy", "queue.database(db)"},
	},
	"schedule": {
		{"keep", "thời gian giữ lần chạy đã xong", `schedule.keep("7d")`},
		{"session", "phiên của lịch", `schedule.session({ role: "system" })`},
		{"database", "database của các lần chạy", "schedule.database(db)"},
	},
}

// folderConfigFiles is the config.work each module's calls are written in, from the app's folder.
var folderConfigFiles = map[string]string{"queue": ".shared/queue/config.work", "schedule": ".schedule/config.work"}

// FolderCalls is what a config.work of .shared/queue/ or of .schedule/ calls at its top level: the module
// whose folder it configures, and each call written right, one per line. The resolver lets these through and
// refuses the module's folder words anywhere else.
type FolderCalls struct {
	Module string
	Calls  map[*MethodCallExpression]bool
}

// FolderConfigWord reports word as a call of module's config.work (queue.parallel, schedule.session).
func FolderConfigWord(module, word string) bool {
	_, ok := folderWordOf(module, word)
	return ok
}

func folderWordOf(module, word string) (folderWord, bool) {
	for _, each := range folderWords[module] {
		if each.word == word {
			return each, true
		}
	}
	return folderWord{}, false
}

// FolderConfigWords are the calls of module's config.work, in the order the spec writes them.
func FolderConfigWords(module string) []string {
	words := make([]string, 0, len(folderWords[module]))
	for _, each := range folderWords[module] {
		words = append(words, each.word)
	}
	return words
}

// FolderCallOutside is the error of a call of a folder's config.work written anywhere else: a route, a job,
// a schedule, a library, another config.work.
func FolderCallOutside(module, word string) string {
	each, _ := folderWordOf(module, word)
	return fmt.Sprintf("%s.%s(…) chỉ viết trong %s, một dòng ở cấp file: %s; ở file khác nó không có nghĩa",
		module, word, folderConfigFiles[module], each.example)
}

// FolderCallNotAtTop is the error of a call of a folder's config.work written in it, but not as a line of its
// own at the file's top level: inside a function, a block, or as a value.
func FolderCallNotAtTop(module, word string) string {
	each, _ := folderWordOf(module, word)
	return fmt.Sprintf("%s.%s(…) là một dòng ở cấp file của config.work: %s; không viết trong hàm, trong khối hay làm giá trị",
		module, word, each.example)
}

// FolderCallChained is the error of something chained after a call of a folder's config.work: the call gives
// nothing to chain or to call, each call is a line of its own. An empty next is a call of what it gave:
// queue.parallel(4)().
func FolderCallChained(module, word, next string) string {
	if next == "" {
		return fmt.Sprintf("%s.%s(…)(…): lời gọi của config.work không trả gì để gọi, mỗi lời gọi một dòng: %s.%s(…);",
			module, word, module, word)
	}
	if FolderConfigWord(module, next) {
		return fmt.Sprintf("%s.%s(…).%s(…) nối tiếp: mỗi lời gọi của config.work một dòng, tách thành hai dòng: %s.%s(…); %s.%s(…);",
			module, word, next, module, word, module, next)
	}
	return fmt.Sprintf("%s.%s(…).%s: lời gọi của config.work không trả gì để nối, mỗi lời gọi một dòng: %s.%s(…);",
		module, word, next, module, word)
}

// FolderCallTwice is the error of a word a config.work calls a second time; first and second are the lines
// of the two calls, 0 when not known.
func FolderCallTwice(module, word string, first, second int) string {
	line := func(n int) string {
		if n <= 0 {
			return "ở trên"
		}
		return fmt.Sprintf("dòng %d", n)
	}
	return fmt.Sprintf("%s.%s gọi hai lần, %s và %s: mỗi chữ một lần trong config.work, giữ một dòng",
		module, word, line(first), line(second))
}

// folderConstRemoved is the error of the const a config.work wrote from 17/10/2026 to 23/10/2026.
func folderConstRemoved(module, word string) string {
	each, _ := folderWordOf(module, word)
	return fmt.Sprintf(`const %s đã bỏ: %s là lời gọi %s, một dòng ở cấp file của %s, sau import * as %s from "kitwork/%s"`,
		word, each.meaning, each.example, folderConfigFiles[module], module, module)
}

// folderOf is the module whose folder a config.work of the app configures: "queue" for one of .shared/queue/
// (a nested queue's own too), "schedule" for one of .schedule/; "" for any other.
func folderOf(appRoot, absolute string) string {
	if appRoot == "" {
		return ""
	}
	root, err := filepath.Abs(appRoot)
	if err != nil {
		root = appRoot
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return ""
	}
	segments := strings.Split(filepath.ToSlash(relative), "/")
	switch {
	case len(segments) >= 3 && segments[0] == ".shared" && segments[1] == "queue":
		return "queue"
	case len(segments) >= 2 && segments[0] == ".schedule":
		return "schedule"
	}
	return ""
}

// readFolderCalls reads the top level of a config.work of module's folder, as the author wrote it (before
// any import is bundled): the names bound to the module, the const of 17/10, and each line that calls a word
// of the folder. text is the file's source, for the places errors give.
func readFolderCalls(prog *Program, module string, text []byte) (*FolderCalls, error) {
	if module == "" {
		return nil, nil
	}
	place := func(token Token) string { return textLocation(text, token) }
	line := func(token Token) int {
		position := int(token.Position)
		if position < 0 || position > len(text) {
			return 0
		}
		return bytes.Count(text[:position], []byte{'\n'}) + 1
	}
	statements := topLevelStatements(prog.Statements)
	namespaces := map[string]bool{}
	for _, statement := range statements {
		binding, ok := statement.(*VarStatement)
		if !ok {
			continue
		}
		if path, isKitwork := kitworkModulePath(binding.Value); isKitwork {
			for _, name := range binding.Names {
				if (binding.DestructMode == DestructNone && path == module) ||
					(binding.DestructMode == DestructObject && path == "" && name.Value == module) {
					namespaces[name.Value] = true
				}
			}
			continue
		}
		for _, name := range binding.Names {
			if FolderConfigWord(module, name.Value) {
				return nil, fmt.Errorf("%s: %s", place(name.Token), folderConstRemoved(module, name.Value))
			}
		}
	}
	calls := &FolderCalls{Module: module, Calls: map[*MethodCallExpression]bool{}}
	seen := map[string]Token{}
	for _, statement := range statements {
		written, ok := statement.(*ExpressionStatement)
		if !ok {
			continue
		}
		call, next := folderCallOf(written.Expression, module, namespaces)
		if call == nil {
			continue
		}
		word := call.Method.Value
		if next != nil {
			return nil, fmt.Errorf("%s: %s", place(next.Token), FolderCallChained(module, word, next.Value))
		}
		if first, twice := seen[word]; twice {
			return nil, fmt.Errorf("%s: %s", place(call.Method.Token), FolderCallTwice(module, word, line(first), line(call.Method.Token)))
		}
		seen[word] = call.Method.Token
		calls.Calls[call] = true
	}
	return calls, nil
}

// topLevelStatements are a program's statements at its top level, those a group holds (const a = 1, b = 2)
// among them.
func topLevelStatements(statements []Statement) []Statement {
	var out []Statement
	for _, statement := range statements {
		if group, ok := statement.(*GroupStatement); ok {
			out = append(out, topLevelStatements(group.Statements)...)
			continue
		}
		out = append(out, statement)
	}
	return out
}

// folderCallOf finds, in the expression a line of config.work is, the call of a folder word on the module's
// namespace it begins with (queue.parallel(4)), and what is chained right after it: nil when the line is the
// call alone. A line that begins with no such call gives nil.
func folderCallOf(expression Expression, module string, namespaces map[string]bool) (call *MethodCallExpression, next *Identifier) {
	current := expression
	for {
		switch node := current.(type) {
		case *MethodCallExpression:
			if root, isName := node.Object.(*Identifier); isName && namespaces[root.Value] && node.Method != nil &&
				FolderConfigWord(module, node.Method.Value) {
				return node, next
			}
			next = node.Method
			current = node.Object
		case *MemberExpression:
			next = node.Property
			current = node.Object
		case *CallExpression:
			next = &Identifier{Token: node.Token}
			current = node.Function
		default:
			return nil, nil
		}
	}
}

// folderCall lets through a call of a folder's config.work written as a line of its own at the file's top
// level (readFolderCalls), reading its namespace and its arguments; what queue.database(…) and
// schedule.database(…) are handed is checked (folderDatabase). Any other call is not this one's.
func (r *resolver) folderCall(node *MethodCallExpression) bool {
	if r.folderCalls == nil || !r.folderCalls.Calls[node] {
		return false
	}
	r.expression(node.Object)
	if node.Method.Value == "database" {
		r.folderDatabase(node)
		return true
	}
	for _, argument := range node.Arguments {
		r.expression(argument)
	}
	return true
}

// folderDatabase checks what queue.database(…) or schedule.database(…) is handed (KITWORK_QUEUE.md mục 2,
// KITWORK_SCHEDULE.md mục 2, 24/10/2026): the name of a database a file of .shared/schema/ declares with
// connection() or connection("tên"), imported from it (or a const that names it again), read as the root it
// is. The engine makes and keeps the tables of the runs there, so a database of connect({ … }), outside the
// app, is refused, and so is a name that is not a database and anything else written there: a database is
// only ever reached by its name (a member, a computed value or a call's result is never one). A call the
// compiler does not see as queue.database (the namespace under another name) is left to the loader, which
// refuses what is not a database the same way as it runs (work/runs_database.go).
func (r *resolver) folderDatabase(node *MethodCallExpression) {
	module := r.folderCalls.Module
	if len(node.Arguments) != 1 {
		r.fail(fmt.Errorf("%s: %s", r.where(node.Method.Token), FolderDatabaseArity(module)))
		return
	}
	switch argument := node.Arguments[0].(type) {
	case *Identifier:
		found := r.lookup(argument.Value)
		if found == nil || found.database == nil {
			r.fail(fmt.Errorf("%s: %s", r.where(argument.Token), FolderDatabaseNotAHandle(module, argument.Value)))
			return
		}
		if found.database.Database.External != "" {
			r.fail(fmt.Errorf("%s: %s", r.where(argument.Token), FolderDatabaseExternal(module, argument.Value)))
			return
		}
		r.handleUse = true
		r.reference(argument, false)
		r.handleUse = false
	case *Literal:
		if argument.Value.K == value.String {
			r.fail(fmt.Errorf("%s: %s", r.where(argument.Token), FolderDatabaseName(module, argument.Value.Text())))
			return
		}
		r.fail(fmt.Errorf("%s: %s", r.where(argument.Token), FolderDatabaseNotAHandle(module, "")))
	case *TemplateLiteral:
		// `jobs` with nothing interpolated is the string "jobs"
		var text strings.Builder
		for _, part := range argument.Parts {
			if !templateText(part) {
				r.fail(fmt.Errorf("%s: %s", r.where(argument.Token), FolderDatabaseNotAHandle(module, "")))
				return
			}
			text.WriteString(part.(*Literal).Value.Text())
		}
		r.fail(fmt.Errorf("%s: %s", r.where(argument.Token), FolderDatabaseName(module, text.String())))
	default:
		r.fail(fmt.Errorf("%s: %s", r.where(expressionToken(argument)), FolderDatabaseNotAHandle(module, "")))
	}
}

// FolderDatabaseArity is the error of queue.database(…) or schedule.database(…) handed no database, or more
// than one.
func FolderDatabaseArity(module string) string {
	return fmt.Sprintf("%s.database(…) nhận đúng một database: %s.database(db), db là tên import của một database khai trong .shared/schema/",
		module, module)
}

// FolderDatabaseNotAHandle is the error of queue.database(…) or schedule.database(…) handed what is not a
// database of .shared/schema/: name is the name written, "" for a value written in place or one the loader
// read as it ran.
func FolderDatabaseNotAHandle(module, name string) string {
	const write = `import { db } from "app/.shared/schema/<tên>.work"; %s.database(db)`
	if name == "" {
		return fmt.Sprintf("%s.database(…) nhận tên import của một database khai trong .shared/schema/ (export const db = connection().schema({ … })): "+write+"; giá trị này không phải database đó",
			module, module)
	}
	return fmt.Sprintf("%s.database(%s): %s không phải database khai trong .shared/schema/; %s.database nhận tên import của database đó: "+write,
		module, name, name, module, module)
}

// FolderDatabaseName is the error of queue.database("tên") or schedule.database("tên"): a name of the host's
// catalogue is chosen in .shared/schema/, and the call is handed the database declared there. A name
// connection("…") does not take (statement.ReservedConnection, or nothing written) is never offered as one:
// "application" is the app's own database, connection() with no name, where the runs are when the call is not
// written; the others are no databases of the catalogue.
func FolderDatabaseName(module, name string) string {
	switch {
	case name == "application":
		return fmt.Sprintf(`%s.database("application"): một chuỗi không phải database; database của app là connection() không tên, và các lần chạy đã ở đó khi không gọi %s.database`,
			module, module)
	case strings.TrimSpace(name) == "" || statement.ReservedConnection(name) != "":
		return fmt.Sprintf(`%s.database(%q): một chuỗi không phải database; %q không phải tên database của danh mục, các lần chạy ở database của app (connection()) hay ở một tên host khai trong app.databases({ … })`,
			module, name, name)
	}
	return fmt.Sprintf(`%s.database(%q): một chuỗi không phải database; database tên %s của host khai trong .shared/schema/ bằng export const db = connection(%q).schema({ … }), rồi %s.database(db) với tên import của nó`,
		module, name, name, name, module)
}

// FolderDatabaseExternal is the error of queue.database(…) or schedule.database(…) handed a database of
// connect({ … }): outside the app, it has no tables the engine keeps. name is the name written, "" when the
// loader read it as it ran.
func FolderDatabaseExternal(module, name string) string {
	written := "…"
	if name != "" {
		written = name
	}
	return fmt.Sprintf(`%s.database(%s): database khai bằng connect(…) là database ngoài, engine không tạo bảng ở đó; các lần chạy cần database có bảng do engine giữ (kitwork_runs): connection() của app, hay connection("tên") với tên host khai trong app.databases({ … }) của application.work`,
		module, written)
}

// folderWordMisplaced is the error of a folder word of module read where it may not be: in its own
// config.work, not as a line of its own; anywhere else, at all.
func (r *resolver) folderWordMisplaced(module, word string) string {
	if r.folderCalls != nil && r.folderCalls.Module == module {
		return FolderCallNotAtTop(module, word)
	}
	return FolderCallOutside(module, word)
}
