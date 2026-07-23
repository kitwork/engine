package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/value"
)

// nativeBundle giải quyết các ImportStatement TƯƠNG ĐỐI trong entryProg bằng cách
// đọc + parse + bọc IIFE từng module, rồi trả về MỘT Program đã ghép (không còn
// ImportStatement). Mỗi module được bọc:
//
//	const __kw_mod_N = (() => { <mã module>; return { <exports> }; })();
//
// nhờ vậy biến cục bộ của module nằm trong frame riêng (f.Vars) — không đụng tên
// với entry hay module khác (đã xác nhận qua runtime/vm.go). Import của entry/module
// được thay bằng khai báo binding (const { x } = __kw_mod_N).
//
// Bất kỳ trường hợp nào không xử lý được (cycle, không tìm thấy file, …) đều trả lỗi
// để caller (Bytecode) rớt về esbuild — nên đây là đường đi AN TOÀN, additive.
func nativeBundle(entryPath string, entryProg *Program) (*Program, []string, error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		abs = entryPath
	}
	b := &bundler{
		modules:  map[string]string{},
		visiting: map[string]bool{},
	}
	body, err := b.rewriteStatements(entryProg.Statements, filepath.Dir(abs))
	if err != nil {
		return nil, nil, err
	}
	combined := &Program{}
	combined.Statements = append(combined.Statements, b.defs...) // IIFE const của module (thứ tự phụ thuộc)
	combined.Statements = append(combined.Statements, body...)   // thân entry (chạy ở top-level)

	// Every bundled module file, sorted for determinism — surfaced on Bytecode.Files so hot
	// reload can watch imports, not just the entry.
	files := make([]string, 0, len(b.modules))
	for absPath := range b.modules {
		files = append(files, absPath)
	}
	sort.Strings(files)
	return combined, files, nil
}

type bundler struct {
	modules  map[string]string // abs path đã resolve -> tên biến module (__kw_mod_N)
	visiting map[string]bool   // phát hiện cycle
	defs     []Statement
	counter  int
}

// rewriteStatements thay mỗi ImportStatement tương đối bằng binding, và bảo đảm
// mỗi module được bundle đúng một lần. fromDir là thư mục của file chứa stmts.
func (b *bundler) rewriteStatements(stmts []Statement, fromDir string) ([]Statement, error) {
	out := make([]Statement, 0, len(stmts))
	for _, s := range stmts {
		imp, ok := s.(*ImportStatement)
		if !ok {
			out = append(out, s)
			continue
		}
		absPath, err := resolveModulePath(imp.Source, fromDir)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(absPath)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() && !imp.SideEffect {
			return nil, fmt.Errorf("native bundle: directory import %q cannot export bindings (must be a side-effect import)", imp.Source)
		}
		modVar, err := b.ensureModule(imp.Source, fromDir)
		if err != nil {
			return nil, err
		}
		if imp.SideEffect {
			continue // IIFE của module đã chạy (side-effect), không cần binding
		}
		if imp.Default != nil {
			out = append(out, memberBinding(imp.Default.Value, modVar, "default"))
		}
		for _, spec := range imp.Names {
			out = append(out, memberBinding(spec.Local, modVar, spec.Imported))
		}
	}
	return out, nil
}

// ensureModule resolve + parse + bọc IIFE một module (đệ quy cho import của nó),
// trả về tên biến module. Dùng cache để dedupe.
func (b *bundler) ensureModule(spec, fromDir string) (string, error) {
	absPath, err := resolveModulePath(spec, fromDir)
	if err != nil {
		return "", err
	}
	if v, ok := b.modules[absPath]; ok {
		return v, nil
	}
	if b.visiting[absPath] {
		return "", fmt.Errorf("native bundle: import cycle tại %s", absPath)
	}
	b.visiting[absPath] = true
	defer delete(b.visiting, absPath)

	fi, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}

	if fi.IsDir() {
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return "", err
		}
		var files []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".kitwork.js") {
				files = append(files, filepath.Join(absPath, entry.Name()))
			}
		}
		sort.Strings(files)
		for _, f := range files {
			_, err := b.ensureModule(f, "")
			if err != nil {
				return "", err
			}
		}
		b.modules[absPath] = ""
		return "", nil
	}

	src, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	prog, err := parseProgram(string(src))
	if err != nil {
		return "", err
	}
	// Đệ quy: import tương đối của module này → binding + append IIFE con trước.
	body, err := b.rewriteStatements(prog.Statements, filepath.Dir(absPath))
	if err != nil {
		return "", err
	}
	body = append(body, exportReturn(prog))

	b.counter++
	name := fmt.Sprintf("__kw_mod_%d", b.counter)
	b.modules[absPath] = name
	b.defs = append(b.defs, moduleIIFE(name, body)) // append SAU các module con → đúng thứ tự
	return name, nil
}

// resolveModulePath quy specifier tương đối về đường dẫn file tuyệt đối, thử các
// đuôi quen thuộc của tenant.
func resolveModulePath(spec, fromDir string) (string, error) {
	// App-shared specifier (`_core/…`): walk UP from the importing file's dir, matching the FIRST `_core`
	// found — the domain's own copy (apps/<id>/<domain>/_core) overrides, else the app-wide one at the
	// identity level (apps/<id>/_core). Relative/abs specifiers keep their exact single-location resolve.
	if !filepath.IsAbs(spec) && strings.HasPrefix(spec, "_") {
		dir := fromDir
		for {
			if resolved, ok := statModule(filepath.Join(dir, spec)); ok {
				return resolved, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break // reached the filesystem root without a match
			}
			dir = parent
		}
		return "", fmt.Errorf("native bundle: app-shared module %q không tìm thấy khi đi ngược từ %s", spec, fromDir)
	}

	base := spec
	if !filepath.IsAbs(spec) {
		base = filepath.Join(fromDir, spec)
	}
	if resolved, ok := statModule(base); ok {
		return resolved, nil
	}
	return "", fmt.Errorf("native bundle: không resolve được %q từ %s", spec, fromDir)
}

// statModule tries a module base path plus the usual extension/index candidates, returning the first
// real file (a bare directory only matches itself, for side-effect dir imports).
func statModule(base string) (string, bool) {
	candidates := []string{base}
	if filepath.Ext(base) == "" {
		candidates = append(candidates,
			base+".kitwork.js", base+".js",
			filepath.Join(base, "index.kitwork.js"), filepath.Join(base, "index.js"))
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil {
			if fi.IsDir() {
				if c == base {
					return filepath.Clean(c), true
				}
				continue
			}
			return filepath.Clean(c), true
		}
	}
	return "", false
}

/* ---- builder AST tổng hợp (lower về node sẵn có, không opcode mới) ---- */

// moduleIIFE: const <name> = (() => { body })();
func moduleIIFE(name string, body []Statement) Statement {
	fn := &FunctionLiteral{
		Token:      Token{Kind: Function},
		Parameters: []*Identifier{},
		Body:       &BlockStatement{Token: Token{Kind: LeftBrace}, Statements: body},
	}
	return &VarStatement{
		Token:        constTok(),
		Names:        []*Identifier{ident(name)},
		DestructMode: DestructNone,
		Value: &CallExpression{
			Token:    Token{Kind: LeftParen},
			Function: fn,
		},
	}
}

// exportReturn: return { a: a, b: b, default: __kw_default };
func exportReturn(prog *Program) Statement {
	entries := make([]ObjectEntry, 0, len(prog.Exports)+1)
	for _, name := range prog.Exports {
		entries = append(entries, ObjectEntry{Key: ident(name), Value: ident(name)})
	}
	if prog.HasDefault {
		entries = append(entries, ObjectEntry{Key: ident("default"), Value: ident(DefaultExportName)})
	}
	return &ReturnStatement{
		Token:       Token{Kind: Return},
		ReturnValue: &ObjectLiteral{Token: Token{Kind: LeftBrace}, Entries: entries},
	}
}

// memberBinding: const <local> = <modVar>.<prop>;  (xử lý cả named + alias + default)
func memberBinding(local, modVar, prop string) Statement {
	return &VarStatement{
		Token:        constTok(),
		Names:        []*Identifier{ident(local)},
		DestructMode: DestructNone,
		Value: &MemberExpression{
			Token:    Token{Kind: Dot},
			Object:   ident(modVar),
			Property: ident(prop),
		},
	}
}

func ident(name string) *Identifier {
	return &Identifier{
		Token: Token{Kind: Ident, Value: value.NewString(name)},
		Value: name,
	}
}

func constTok() Token {
	return Token{Kind: Const, Value: value.NewString("const")}
}

// hasRelativeImports báo Program có ImportStatement (luôn là import tương đối, vì
// import "kitwork" được hạ thẳng về VarStatement trong parser).
func hasRelativeImports(prog *Program) bool {
	for _, s := range prog.Statements {
		if _, ok := s.(*ImportStatement); ok {
			return true
		}
	}
	return false
}
