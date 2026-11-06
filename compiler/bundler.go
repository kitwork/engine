package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/database/statement"
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
	program, files, _, err := nativeBundleWithSources(entryPath, entryProg, "", nil)
	return program, files, err
}

// nativeBundleWithSources is nativeBundle that also gives every module's source by its name. appRoot is
// the folder of the app the entry belongs to, where an `app/…` import resolves ("" when the file is no
// app's, as the host's manifest: an `app/…` import is then a compile error). sources holds the entry's
// own source by its name (nil for none), so an error of an import written in it says where it stands.
func nativeBundleWithSources(
	entryPath string,
	entryProg *Program,
	appRoot string,
	sources map[string]string,
) (*Program, []string, map[string]string, error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		abs = entryPath
	}
	if appRoot != "" {
		if absolute, err := filepath.Abs(appRoot); err == nil {
			appRoot = absolute
		}
	}
	known := make(map[string]string, len(sources))
	for name, text := range sources {
		known[name] = text
	}
	b := &bundler{
		modules:   map[string]string{},
		visiting:  map[string]bool{},
		sources:   known,
		debugRoot: filepath.Dir(abs),
		appRoot:   appRoot,
	}
	body, err := b.rewriteStatements(entryProg.Statements, filepath.Dir(abs))
	if err != nil {
		return nil, nil, nil, err
	}
	combined := &Program{Database: b.database, Queue: b.queue, Schedule: b.schedule}
	combined.Statements = append(combined.Statements, b.defs...) // IIFE const của module (thứ tự phụ thuộc)
	combined.Statements = append(combined.Statements, body...)   // thân entry (chạy ở top-level)

	// Every bundled module file, sorted for determinism — surfaced on Bytecode.Files so hot
	// reload can watch imports, not just the entry.
	files := make([]string, 0, len(b.modules))
	for absPath := range b.modules {
		files = append(files, absPath)
	}
	sort.Strings(files)
	return combined, files, b.sources, nil
}

type bundler struct {
	sources   map[string]string
	debugRoot string
	modules   map[string]string // abs path đã resolve -> tên biến module (__kw_mod_N)
	visiting  map[string]bool   // phát hiện cycle
	defs      []Statement
	counter   int
	// appRoot is the app's folder an `app/…` import resolves from, "" for a file of no app;
	// appRootReal is it past links, found at the first app/ import.
	appRoot     string
	appRootReal string
	// schemas holds, per schema file of .shared/schema/ (by its absolute path), the databases it
	// exports, read from its source (database_catalog.go); exports the names every module exports.
	schemas map[string]map[string]*statement.Database
	exports map[string][]string
	// database is what the resolver needs of the schema files: nil while no module is one.
	database *DatabaseBundle
	// queue is what the resolver needs of the files of .shared/queue/: nil while no module is one.
	queue *QueueBundle
	// schedule is the same of the files of .schedule/.
	schedule *ScheduleBundle
	// rewrite readies the program of the file entry before anything is bundled into it (CompileComponentFile);
	// nil for any other compile
	entry   string
	rewrite func(*Program)
}

// schemaModule reports a file of the app's .shared/schema/, and its name from the app's folder. The
// folder's spelling is exact: .Shared/Schema is not one, and its struct() is refused where it is imported.
// A file of no app has no schema folder.
func (b *bundler) schemaModule(absPath string) (string, bool) {
	if b.appRoot == "" {
		return "", false
	}
	relative, err := b.appRelative(absPath)
	if err != nil {
		return "", false
	}
	relative = filepath.ToSlash(relative)
	segments := strings.Split(relative, "/")
	if len(segments) < 3 || segments[0] != ".shared" || segments[1] != "schema" {
		return "", false
	}
	return relative, true
}

func (b *bundler) databaseBundle() *DatabaseBundle {
	if b.database == nil {
		b.database = &DatabaseBundle{
			Imports:       map[*Identifier]*DatabaseImport{},
			SchemaModules: map[*FunctionLiteral]string{},
		}
	}
	return b.database
}

// databaseImports records the bindings of an import of a schema file: a database it exports becomes a
// handle the resolver translates statements on. A name the file does not export, and a default import,
// are compile errors: the handle would be null and every statement on it would fail when it runs.
func (b *bundler) databaseImports(imp *ImportStatement, absPath string, bindings []Statement) error {
	databases, isSchema := b.schemas[absPath]
	if !isSchema {
		return nil
	}
	relative, _ := b.schemaModule(absPath)
	name := filepath.Base(absPath)
	if imp.Default != nil {
		return fmt.Errorf("%s: %s không có export default: import { %s } from %q", b.place(imp.Token), name, firstOr(b.exports[absPath], "db"), imp.Source)
	}
	for index, spec := range imp.Names {
		exported := false
		for _, export := range b.exports[absPath] {
			exported = exported || export == spec.Imported
		}
		if !exported {
			return fmt.Errorf("%s: %s không export %s; có: %s", b.place(imp.Token), name, spec.Imported, sortedExports(b.exports[absPath]))
		}
		database := databases[spec.Imported]
		if database == nil {
			continue
		}
		local := bindings[index].(*VarStatement).Names[0]
		b.databaseBundle().Imports[local] = &DatabaseImport{Module: relative, Export: spec.Imported, Database: database}
	}
	return nil
}

func firstOr(names []string, otherwise string) string {
	for _, name := range names {
		if name != DefaultExportName {
			return name
		}
	}
	return otherwise
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
		absPath, err := b.resolve(imp, fromDir)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(absPath)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() && !imp.SideEffect {
			// said where the import stands, in the author's words, with the two forms that work (22/10/2026)
			return nil, fmt.Errorf("%s: import %q là một thư mục, không có gì để lấy theo tên: lấy từ một file trong đó (%q), hay viết import %q không tên để chạy cả thư mục",
				b.place(imp.SourceToken), imp.Source, strings.TrimSuffix(imp.Source, "/")+"/x.work", imp.Source)
		}
		modVar, err := b.ensureModule(absPath)
		if err != nil {
			return nil, err
		}
		if imp.SideEffect {
			continue // IIFE của module đã chạy (side-effect), không cần binding
		}
		if imp.Default != nil {
			out = append(out, memberBinding(imp.Default.Value, modVar, "default", imp.Token))
		}
		bindings := make([]Statement, 0, len(imp.Names))
		for _, spec := range imp.Names {
			bindings = append(bindings, memberBinding(spec.Local, modVar, spec.Imported, imp.Token))
		}
		if err := b.databaseImports(imp, absPath, bindings); err != nil {
			return nil, err
		}
		b.queueImports(absPath, bindings)
		b.scheduleImports(absPath, bindings)
		out = append(out, bindings...)
	}
	return out, nil
}

// ensureModule parse + bọc IIFE một module đã resolve (absPath, đệ quy cho import của nó),
// trả về tên biến module. Dùng cache để dedupe.
func (b *bundler) ensureModule(absPath string) (string, error) {
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
			// a .work file, as the error above says (27/10/2026: only .kitwork.js members were read, so
			// import "./dir/" of .work files ran nothing), and a template site's .kitwork.js until it goes
			if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".work") || strings.HasSuffix(entry.Name(), ".kitwork.js")) {
				files = append(files, filepath.Join(absPath, entry.Name()))
			}
		}
		sort.Strings(files)
		for _, f := range files {
			_, err := b.ensureModule(f)
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
	sourceName := b.sourceName(absPath)
	sourceText := string(src)
	b.sources[sourceName] = sourceText
	prog, err := parseProgramSource(sourceText, sourceName)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(absPath, ".work") {
		// a .work file bundled into another is never a router.work nor a site's config.work: it has no
		// router (router_rules.go)
		if err := refuseRouterImport(prog, src); err != nil {
			return "", err
		}
		// nor a design.work: a file bundled into another never declares a folder's look (design.go)
		if err := refuseDesignImport(prog, src); err != nil {
			return "", err
		}
	}
	if b.rewrite != nil && absPath == b.entry {
		b.rewrite(prog)
	}
	if b.exports == nil {
		b.exports = map[string][]string{}
	}
	b.exports[absPath] = prog.Exports
	schema, isSchema := b.schemaModule(absPath)
	if isSchema {
		// a database is read from the file's own source, before anything is bundled into it
		databases, err := readSchemaModule(prog, b.place)
		if err != nil {
			return "", err
		}
		if b.schemas == nil {
			b.schemas = map[string]map[string]*statement.Database{}
		}
		b.schemas[absPath] = databases
	}
	// Đệ quy: import tương đối của module này → binding + append IIFE con trước.
	body, err := b.rewriteStatements(prog.Statements, filepath.Dir(absPath))
	if err != nil {
		return "", err
	}
	if err := b.refuseDatabaseReexport(prog, body); err != nil {
		return "", err
	}
	origin := Token{
		Kind:     Return,
		Source:   sourceName,
		Position: int32(len(src)),
	}
	queue, isQueue := b.queueModule(absPath)
	schedule, isSchedule := b.scheduleModule(absPath)
	switch {
	case isQueue:
		// a job is named by its export, in every bundle the file is in (queue.go)
		if err := b.queueExportsRule(prog); err != nil {
			return "", err
		}
		body = append(body, namedExportReturn(prog, origin, queue, "queue"))
	case isSchedule:
		// a schedule too (schedule.go)
		if err := b.scheduleExportsRule(prog); err != nil {
			return "", err
		}
		body = append(body, namedExportReturn(prog, origin, schedule, "schedule"))
	default:
		body = append(body, exportReturn(prog, origin))
	}

	b.counter++
	name := fmt.Sprintf("__kw_mod_%d", b.counter)
	b.modules[absPath] = name
	definition := moduleIIFE(name, body)
	if isSchema {
		function := definition.(*VarStatement).Value.(*CallExpression).Function.(*FunctionLiteral)
		b.databaseBundle().SchemaModules[function] = schema
	}
	if isQueue {
		function := definition.(*VarStatement).Value.(*CallExpression).Function.(*FunctionLiteral)
		b.queueBundle().Modules[function] = queue
	}
	if isSchedule {
		function := definition.(*VarStatement).Value.(*CallExpression).Function.(*FunctionLiteral)
		b.scheduleBundle().Modules[function] = schedule
	}
	b.defs = append(b.defs, definition) // append SAU các module con → đúng thứ tự
	return name, nil
}

// refuseDatabaseReexport refuses a module that exports a database it imported (export { db }): a
// statement is translated only on a name imported straight from the schema file, so a handle passed on
// would reach files that cannot use it.
func (b *bundler) refuseDatabaseReexport(prog *Program, body []Statement) error {
	if b.database == nil || len(prog.Exports) == 0 {
		return nil
	}
	for _, node := range body {
		binding, ok := node.(*VarStatement)
		if !ok || len(binding.Names) != 1 {
			continue
		}
		imported := b.database.Imports[binding.Names[0]]
		if imported == nil {
			continue
		}
		for _, export := range prog.Exports {
			if export == binding.Names[0].Value {
				return fmt.Errorf("%s: export lại %s chưa có trong engine hôm nay: file dùng database import thẳng từ app/%s",
					b.place(binding.Token), export, imported.Module)
			}
		}
	}
	return nil
}

// sourceName is how a module's file is named in the debug table, and so in an error's at: relative to
// the entry's folder, never the machine's absolute path. When no relative path exists (another drive on
// Windows) it is the file's name alone (14/10/2026; until then the absolute path).
func (b *bundler) sourceName(path string) string {
	relative, err := filepath.Rel(b.debugRoot, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(relative)
}

// resolve is the file an import names: an `app/…` path from the app's folder, any other from the
// folder of the file that imports (fromDir).
func (b *bundler) resolve(imp *ImportStatement, fromDir string) (string, error) {
	if !isAppRootSpecifier(imp.Source) {
		resolved, err := resolveModulePath(imp.Source, fromDir)
		if err != nil {
			return "", err
		}
		// nothing imports from an app's data, whatever the spelling (KITWORK_UI.md mục 4): a relative path into
		// a .database/ is refused as app/.database/ is (22/10/2026)
		if b.appRoot != "" && b.inAppData(resolved) {
			return "", fmt.Errorf("%s: import %q: .database/ là dữ liệu của app, không import được; code dùng chung đặt trong .shared/", b.place(imp.SourceToken), imp.Source)
		}
		return resolved, nil
	}
	where := b.place(imp.SourceToken)
	if b.appRoot == "" {
		return "", fmt.Errorf("%s: import %q: file này không thuộc app nào nên không có app/; app/ là thư mục của app chứa file đang viết (app/ khi một app, apps/<identity>/ khi nhiều app)", where, imp.Source)
	}
	// the parser refused every spelling that leaves the app; a program built another way meets the rule here
	if problem := appRootSpecifierProblem(imp.Source); problem != "" {
		return "", fmt.Errorf("%s: %s", where, problem)
	}
	rest := strings.TrimPrefix(imp.Source, "app/")
	resolved, ok := statModule(filepath.Join(b.appRoot, filepath.FromSlash(rest)))
	if !ok {
		return "", fmt.Errorf("%s: import %q: app không có file %s", where, imp.Source, rest)
	}
	// the same rule on the disk, past links: the file stays inside the app and out of its data
	relative, err := b.appRelative(resolved)
	if err != nil {
		// a link the system does not let Go follow (a junction on Windows): refused, never guessed inside
		return "", fmt.Errorf("%s: import %q: không lần được đường tới file qua liên kết; app/ chỉ trỏ tới file nằm trong gốc app", where, imp.Source)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("%s: import %q ra ngoài app: file nó tới (qua liên kết) không nằm trong gốc app", where, imp.Source)
	}
	if first, _, _ := strings.Cut(filepath.ToSlash(relative), "/"); strings.EqualFold(first, ".database") {
		return "", fmt.Errorf("%s: import %q: .database/ là dữ liệu của app, không import được; code dùng chung đặt trong .shared/", where, imp.Source)
	}
	return resolved, nil
}

// inAppData reports whether path lies in a .database/ folder of this app or of an app beside it (counted
// from the folder that holds the app: apps/ for many apps, the host for one), as written or past links.
func (b *bundler) inAppData(path string) bool {
	holder := filepath.Dir(b.appRoot)
	if hasDataSegment(holder, path) {
		return true
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	holderReal, err := filepath.EvalSymlinks(holder)
	if err != nil {
		return false
	}
	return hasDataSegment(holderReal, real)
}

// hasDataSegment reports a folder .database on the way from base to path (".database." and ".database "
// too, which Windows opens as .database).
func hasDataSegment(base, path string) bool {
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if strings.EqualFold(strings.TrimRight(segment, ". "), ".database") {
			return true
		}
	}
	return false
}

// appRelative is path from the app's folder, both past links.
func (b *bundler) appRelative(path string) (string, error) {
	if b.appRootReal == "" {
		real, err := filepath.EvalSymlinks(b.appRoot)
		if err != nil {
			return "", err
		}
		b.appRootReal = real
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Rel(b.appRootReal, real)
}

// place is where tok stands, file:line:column as the parser writes it, read from the source of the file it
// is in; the file's name alone when the bundler does not hold that source.
func (b *bundler) place(tok Token) string {
	text, ok := b.sources[tok.Source]
	if !ok {
		if tok.Source != "" {
			return tok.Source
		}
		return fmt.Sprintf("pos %d", tok.Position)
	}
	return textLocation([]byte(text), tok)
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
			filepath.Join(base, "index.kitwork.js"), filepath.Join(base, "index.js"),
			base+".work", filepath.Join(base, "index.work")) // 27/10/2026: an import without its .work
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
	origin := Token{}
	if len(body) > 0 {
		origin.Source = getNodeSource(body[0])
		origin.Position = getNodePosition(body[0])
	}
	fn := &FunctionLiteral{
		Token:      tokenFrom(origin, Function),
		Parameters: []*Identifier{},
		Body:       &BlockStatement{Token: tokenFrom(origin, LeftBrace), Statements: body},
		DebugName:  "<module>",
	}
	return &VarStatement{
		Token:        constTok(origin),
		Names:        []*Identifier{ident(name, origin)},
		DestructMode: DestructNone,
		Value: &CallExpression{
			Token:    tokenFrom(origin, LeftParen),
			Function: fn,
		},
	}
}

// exportReturn: return { a: a, c: b, default: __kw_default }; (export { b as c } gives c the value of b)
func exportReturn(prog *Program, origin Token) Statement {
	locals := make(map[string]string, len(prog.ExportAliases))
	for _, alias := range prog.ExportAliases {
		locals[alias.Exported] = alias.Local
	}
	entries := make([]ObjectEntry, 0, len(prog.Exports)+1)
	for _, name := range prog.Exports {
		local := name
		if aliased, ok := locals[name]; ok {
			local = aliased
		}
		entries = append(entries, ObjectEntry{
			Key:   ident(name, origin),
			Value: ident(local, origin),
		})
	}
	if prog.HasDefault {
		entries = append(entries, ObjectEntry{
			Key:   ident("default", origin),
			Value: ident(DefaultExportName, origin),
		})
	}
	return &ReturnStatement{
		Token:       tokenFrom(origin, Return),
		ReturnValue: &ObjectLiteral{Token: tokenFrom(origin, LeftBrace), Entries: entries},
	}
}

// memberBinding: const <local> = <modVar>.<prop>;  (xử lý cả named + alias + default)
func memberBinding(local, modVar, prop string, origin Token) Statement {
	return &VarStatement{
		Token:        constTok(origin),
		Names:        []*Identifier{ident(local, origin)},
		DestructMode: DestructNone,
		Value: &MemberExpression{
			Token:    tokenFrom(origin, Dot),
			Object:   ident(modVar, origin),
			Property: ident(prop, origin),
		},
	}
}

func ident(name string, origin Token) *Identifier {
	return &Identifier{
		Token: Token{
			Kind:     Ident,
			Value:    value.NewString(name),
			Source:   origin.Source,
			Position: origin.Position,
		},
		Value: name,
	}
}

func constTok(origin Token) Token {
	token := tokenFrom(origin, Const)
	token.Value = value.NewString("const")
	return token
}

func tokenFrom(origin Token, kind Kind) Token {
	return Token{
		Kind:     kind,
		Source:   origin.Source,
		Position: origin.Position,
	}
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
