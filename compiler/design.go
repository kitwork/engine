package compiler

import (
	"fmt"
	"path/filepath"
	"strings"
)

// design.work (KITWORK_DESIGN.md, 28/10/2026): the file a folder of a Kitwork UI site declares its look in, the
// seven words of kitwork/design, one call per line at the file's top level:
//
//	import * as design from "kitwork/design";
//	design.colors({ brand: "#f82244", ink: { light: "#18181b", dark: "#fafafa" } });
//	design.fonts({ body: "Outfit", code: "Fira Code" });
//
// Each word has the value, the meaning, the merge and the inheritance of router.<the same word> in router.work
// and config.work (KITWORK_ROUTER.md mục 4): the engine runs design.work into the folder before its config.work
// and its router.work (work/router_design.go), so design.work is a new place and a new spelling, not new
// behaviour. The rules of the file are read here, as the author wrote it, before anything is bundled into it
// (readDesignFile), and by the resolver where the namespace is read (designCall, designRead):
//
//   - exactly one Kitwork import line, import * as design from "kitwork/design", named design; another module
//     of Kitwork, or kitwork() written out, is an error: the file's vocabulary is closed. Values imported from a
//     file of the app ("app/.shared/brand.work") are allowed;
//   - each line a call design.<word>(…) of the seven words; another word is an error naming the nearest, a word of
//     the router (guard, meta, limit…) an error saying it belongs in router.work or config.work;
//   - a call never chained, a word whose nearest folder wins (appearance, contrast, transition) once per file;
//   - the namespace only at the head of such a line: in an if, a loop, a function's body, or read as a value, it
//     is an error.
//
// Any other file refuses kitwork/design (refuseDesignImport). router.colors(…) and its six neighbours keep
// working in router.work and config.work: this engine's tests and the sites written before design.work use them.

// DesignFile is the name of a folder's design file.
const DesignFile = "design.work"

// designWords are the seven words of kitwork/design, in the order of the spec (KITWORK_DESIGN.md mục 2).
var designWords = []string{"colors", "gradients", "fonts", "highlight", "appearance", "contrast", "transition"}

// designOldWords are words an author may reach for that the spec settled otherwise, each with the word it is.
var designOldWords = map[string]string{"palette": "colors"}

// DesignWords are the seven words of kitwork/design, in the order of the spec.
func DesignWords() []string {
	return append([]string(nil), designWords...)
}

// DesignWordOf is a word of kitwork/design as the router's table has it (its inheritance and its levels):
// design.colors is router.colors written in another file.
func DesignWordOf(word string) (RouterWord, bool) {
	for _, each := range designWords {
		if each == word {
			return RouterWordOf(word)
		}
	}
	return RouterWord{}, false
}

// IsDesignFile reports a path as a design.work.
func IsDesignFile(path string) bool {
	return filepath.Base(path) == DesignFile
}

// DesignImportOutside is the error of kitwork/design in any file but a design.work.
const DesignImportOutside = `kitwork/design chỉ có trong design.work: hình thức của một thư mục (màu, phông, gradient, màu khối code, sáng tối, tương phản, chuyển trang) khai trong design.work cùng thư mục, design.colors({ … }) (KITWORK_DESIGN.md mục 1)`

// DesignImportForm is the error of a way to kitwork/design other than its one line; written is what the file
// wrote.
func DesignImportForm(written string) string {
	return fmt.Sprintf(`%s: design.work đến Kitwork qua đúng một dòng import * as design from "kitwork/design", tên design, một lần mỗi file; rồi design.colors(…), design.fonts(…) (KITWORK_DESIGN.md mục 1)`, written)
}

// DesignImportTwice is the error of the design's import line written a second time.
func DesignImportTwice(first, second int) string {
	return fmt.Sprintf(`import * as design from "kitwork/design" viết hai lần, dòng %d và dòng %d: một file một dòng import đó`, first, second)
}

// DesignImportOther is the error of another module of Kitwork in a design.work; written is the import as the
// file wrote it, or kitwork() written out.
func DesignImportOther(written string) string {
	return fmt.Sprintf(`%s trong design.work: design.work có đúng một dòng import của Kitwork, import * as design from "kitwork/design"; từ vựng của file này đóng, module khác của Kitwork không dùng ở đây. Giá trị của app thì import từ file của app: import { brand } from "app/.shared/brand.work" (KITWORK_DESIGN.md mục 1)`, written)
}

// DesignWithoutImport is the error of design.<word>(…) in a file with no import line of kitwork/design.
func DesignWithoutImport(word string) string {
	return fmt.Sprintf(`design.%s(…) không có dòng import: design.work bắt đầu bằng import * as design from "kitwork/design" (KITWORK_DESIGN.md mục 1)`, word)
}

// DesignUnknownWord is the error of a word kitwork/design does not have, with the nearest of the seven.
func DesignUnknownWord(word string) string {
	nearest := designOldWords[word]
	if nearest == "" {
		nearest = RouterNearestWord(word, designWords)
	}
	if nearest != "" {
		return fmt.Sprintf("design.%s không có; ý là design.%s?", word, nearest)
	}
	return fmt.Sprintf("design.%s không có; design có %s (KITWORK_DESIGN.md mục 2)", word, strings.Join(designWords, ", "))
}

// DesignRouterWord is the error of a word of the router written as design.<word>: a rule of the router, not a
// look.
func DesignRouterWord(word string) string {
	return fmt.Sprintf("design.%s không có: %s là luật của router, viết router.%s(…) trong router.work hay config.work; design.work chỉ khai hình thức, %s (KITWORK_DESIGN.md mục 1)",
		word, word, word, strings.Join(designWords, ", "))
}

// designWordError is the error of design.<word> for a word that is not one of the seven: a word of the router,
// or any other, with the nearest.
func designWordError(word string) string {
	if _, ok := RouterWordOf(word); ok {
		return DesignRouterWord(word)
	}
	return DesignUnknownWord(word)
}

// DesignCallChained is the error of something chained after a call of design.work: it gives nothing to chain
// or to call, each call is a line of its own. next is what follows, as written: ".fonts(…)", ".x", "(…)".
func DesignCallChained(word, next string) string {
	if strings.HasPrefix(next, ".") && strings.HasSuffix(next, "(…)") {
		return fmt.Sprintf("design.%s(…)%s nối tiếp: lời gọi của design.work không trả gì để nối; tách thành hai dòng: design.%s(…); design%s;",
			word, next, word, next)
	}
	return fmt.Sprintf("design.%s(…)%s: lời gọi của design.work không trả gì để nối hay gọi; mỗi lời gọi một dòng: design.%s(…);",
		word, next, word)
}

// DesignCallTwice is the error of a word whose nearest folder wins (appearance, contrast, transition) written
// twice for one folder; first and second are the places of the two ("dòng 3", "router.work:5").
func DesignCallTwice(word, first, second string) string {
	return fmt.Sprintf("design.%s gọi hai lần, %s và %s: thư mục gần nhất thắng nên mỗi thư mục một design.%s(…); giữ một dòng (KITWORK_DESIGN.md mục 1)",
		word, first, second, word)
}

// DesignNotAtTop is the error of design.<word>(…) written anywhere but as a line of its own at the file's top
// level.
func DesignNotAtTop(word string) string {
	return fmt.Sprintf("design.%s(…) chỉ viết ở cấp file của design.work, một dòng; không trong if, vòng lặp hay thân hàm, không làm phần của một biểu thức khác (KITWORK_DESIGN.md mục 1)", word)
}

// DesignAsValue is the error of the design read as a value; written is what was read (design, design.colors).
func DesignAsValue(written string) string {
	return fmt.Sprintf("%s đọc làm giá trị: design chỉ đứng đầu một lời gọi ở cấp file, design.colors(…), design.fonts(…) (KITWORK_DESIGN.md mục 1)", written)
}

// DesignCalls are the lines of a design.work: Heads are the design.<word>(…) each line begins with, which the
// resolver lets through; every other use of the namespace is refused.
type DesignCalls struct {
	Heads map[*MethodCallExpression]bool
}

// designReader reads a design.work as the author wrote it.
type designReader struct {
	text      []byte
	namespace string
	declared  map[string]bool
	failures  []routerFailure
	calls     *DesignCalls
}

func (reader *designReader) fail(at Token, message string) {
	reader.failures = append(reader.failures, routerFailure{at: at, message: message})
}

func (reader *designReader) lineOf(token Token) int {
	return (&routerReader{text: reader.text}).lineOf(token)
}

// readDesignFile reads the top level of a design.work before any import is bundled into it: its imports of
// Kitwork, kitwork() written out anywhere, and each line that calls the design. text is the file's source, for
// the places errors give. The first error in the file is the one returned.
func readDesignFile(prog *Program, text []byte) (*DesignCalls, error) {
	reader := &designReader{text: text, declared: map[string]bool{}, calls: &DesignCalls{Heads: map[*MethodCallExpression]bool{}}}
	statements := topLevelStatements(prog.Statements)
	for _, statement := range statements {
		if node, ok := statement.(*VarStatement); ok {
			for _, name := range node.Names {
				reader.declared[name.Value] = true
			}
		}
	}
	reader.imports(statements)
	reader.kitworkWrittenOut(prog.Statements)
	seen := map[string]Token{}
	for _, statement := range statements {
		if line, ok := statement.(*ExpressionStatement); ok {
			reader.readLine(line.Expression, seen)
		}
	}
	if err := firstFailure(reader.failures, text); err != nil {
		return nil, err
	}
	return reader.calls, nil
}

// imports reads the file's imports of Kitwork: the one line of the design, and any other, refused.
func (reader *designReader) imports(statements []Statement) {
	var first *VarStatement
	for _, statement := range statements {
		node, ok := statement.(*VarStatement)
		if !ok || node.Import == ImportNone {
			continue
		}
		module, imported := importOf(node)
		switch {
		case module == "design" && node.Import == ImportNamespace && node.Names[0].Value == "design":
			if first != nil {
				reader.fail(node.Token, DesignImportTwice(reader.lineOf(first.Token), reader.lineOf(node.Token)))
				continue
			}
			first = node
			reader.namespace = "design"
		case module == "design":
			reader.fail(node.Token, DesignImportForm(importWritten(node, module, imported)))
		default:
			reader.fail(node.Token, DesignImportOther(importWritten(node, module, imported)))
		}
	}
}

// kitworkWrittenOut refuses kitwork() written anywhere in the file but in its import line: the file reaches
// Kitwork through that line alone.
func (reader *designReader) kitworkWrittenOut(statements []Statement) {
	eachNode(statements, func(node Node) bool {
		switch n := node.(type) {
		case *VarStatement:
			if n.Import != ImportNone {
				return false
			}
		case *CallExpression:
			if isKitworkCall(n) {
				reader.fail(n.Token, DesignImportOther("kitwork()"))
			}
		}
		return true
	})
}

// readLine reads a line: when it begins with design.<word>(…), the word and what is chained after it. Any other
// line is not the design's (the resolver refuses the design read in it).
func (reader *designReader) readLine(expression Expression, seen map[string]Token) {
	var links []routerLink
	current := expression
	for {
		switch node := current.(type) {
		case *ChainExpression:
			current = node.Expression
			continue
		case *MethodCallExpression:
			if node.Method == nil {
				return
			}
			if name, isName := node.Object.(*Identifier); isName && reader.namespace != "" && name.Value == reader.namespace {
				for left, right := 0, len(links)-1; left < right; left, right = left+1, right-1 {
					links[left], links[right] = links[right], links[left]
				}
				reader.call(node, links, seen)
				return
			}
			if name, isName := node.Object.(*Identifier); isName && reader.namespace == "" && name.Value == "design" && !reader.declared["design"] {
				reader.fail(name.Token, DesignWithoutImport(node.Method.Value))
				return
			}
			links = append(links, routerLink{at: node.Method.Token, word: node.Method.Value, written: "." + node.Method.Value + "(…)", call: true})
			current = node.Object
		case *MemberExpression:
			if node.Property == nil {
				return
			}
			links = append(links, routerLink{at: node.Property.Token, word: node.Property.Value, written: "." + node.Property.Value})
			current = node.Object
		case *CallExpression:
			links = append(links, routerLink{at: node.Token, written: "(…)"})
			current = node.Function
		case *IndexExpression:
			links = append(links, routerLink{at: node.Token, written: "[…]"})
			current = node.Left
		default:
			return
		}
	}
}

// call reads the design.<word>(…) a line begins with and what the line chains after it.
func (reader *designReader) call(head *MethodCallExpression, links []routerLink, seen map[string]Token) {
	word, at := head.Method.Value, head.Method.Token
	known, ok := DesignWordOf(word)
	if !ok {
		reader.fail(at, designWordError(word))
		return
	}
	reader.calls.Heads[head] = true
	if len(links) > 0 {
		reader.fail(links[0].at, DesignCallChained(word, links[0].written))
		return
	}
	if known.Inheritance == RouterNearestWins {
		if first, twice := seen[word]; twice {
			reader.fail(at, DesignCallTwice(word, fmt.Sprintf("dòng %d", reader.lineOf(first)), fmt.Sprintf("dòng %d", reader.lineOf(at))))
			return
		}
		seen[word] = at
	}
}

// refuseDesignImport refuses kitwork/design in a file that is not a design.work: an import of it in any form,
// and import { design } from "kitwork".
func refuseDesignImport(prog *Program, text []byte) error {
	var failures []routerFailure
	for _, statement := range topLevelStatements(prog.Statements) {
		node, ok := statement.(*VarStatement)
		if !ok || node.Import == ImportNone {
			continue
		}
		module, imported := importOf(node)
		if module == "design" || (module == "" && node.Import == ImportNamed && containsWord(imported, "design")) {
			failures = append(failures, routerFailure{at: node.Token, message: DesignImportOutside})
		}
	}
	return firstFailure(failures, text)
}

// designCall lets through the design.<word>(…) a line of a design.work begins with (readDesignFile), reading the
// namespace and the call's arguments; the same call anywhere else (in a function, an if, a loop, or as part of
// another expression) is refused, and so is a word that is not one of the seven.
func (r *resolver) designCall(node *MethodCallExpression) bool {
	if r.designCalls == nil || node.Method == nil {
		return false
	}
	name, isName := node.Object.(*Identifier)
	if !isName {
		return false
	}
	if found := r.lookup(name.Value); found == nil || !found.designModule {
		return false
	}
	word := node.Method.Value
	if _, ok := DesignWordOf(word); !ok {
		r.fail(fmt.Errorf("%s: %s", r.where(node.Method.Token), designWordError(word)))
		return true
	}
	if !r.designCalls.Heads[node] {
		r.fail(fmt.Errorf("%s: %s", r.where(node.Method.Token), DesignNotAtTop(word)))
		return true
	}
	r.designUse = true
	r.reference(name, false)
	r.designUse = false
	for _, argument := range node.Arguments {
		r.expression(argument)
	}
	return true
}

// designMember refuses a word read off the design without a call (design.colors, helper(design.fonts)).
func (r *resolver) designMember(object Expression, property *Identifier) bool {
	if r.designCalls == nil || property == nil {
		return false
	}
	name, isName := object.(*Identifier)
	if !isName {
		return false
	}
	if found := r.lookup(name.Value); found == nil || !found.designModule {
		return false
	}
	r.fail(fmt.Errorf("%s: %s", r.where(property.Token), DesignAsValue(name.Value+"."+property.Value)))
	return true
}

// designRead refuses the design read as a value (const d = design, helper(design), design["colors"]): it stands
// only at the head of a line's call (designCall).
func (r *resolver) designRead(name *Identifier, found *binding) bool {
	if !found.designModule || r.designUse {
		return false
	}
	r.fail(fmt.Errorf("%s: %s", r.where(name.Token), DesignAsValue(name.Value)))
	return true
}

// markDesign marks the name of a design.work's import line as the module kitwork/design itself.
func (r *resolver) markDesign(node *VarStatement, declared *binding) {
	if r.designCalls == nil || declared == nil || node.Pattern != nil || node.DestructMode != DestructNone {
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok && module == "design" {
		declared.designModule = true
	}
}
