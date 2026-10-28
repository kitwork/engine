package compiler

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/value"
)

// A job of an app is declared in its .shared/queue/ (KITWORK_QUEUE.md mục 2, 3): a file-level const whose
// value is queue.job(fn), exported, and named by the name it is exported under:
//
//	export const camOn = queue.job((context) => { … }).retry(3).timeout("30s");   // the job camOn
//
// A handle is a value like any other, evaluated again in every bundle that imports its file, so the name
// cannot come from the evaluation alone. The bundler gives it: the export object of a file of .shared/queue/
// goes through kitwork().queue.#exports({ … }, "<file>", [names]) on its way out, which names each job after its
// key, in every bundle that imports the file and in the loader's own (CompileModuleFileInApp). An author
// cannot write #exports: the name is not one the parser reads.

// QueueExportsMethod is the method of the queue module the export object of a file of .shared/queue/ goes
// through.
const QueueExportsMethod = "#exports"

// queueRemoved says what the spec writes for each word of kitwork/queue it replaced (21/10/2026). The
// compiler refuses one where it is written (queueMember); the module refuses it too when it runs, for a
// namespace the compiler could not follow.
var queueRemoved = map[string]string{
	"handle":   `queue.handle đã bỏ: job khai trong .shared/queue/<tên>.work bằng export const camOn = queue.job((context) => { … })`,
	"retry":    `queue.retry đã bỏ: lần thử thêm là luật của job, queue.job(fn).retry(3); chạy lại một lần chạy là queue.replay(id)`,
	"dispatch": `queue.dispatch đã bỏ: import { camOn } from "app/.shared/queue/camOn.work" rồi camOn.send(data, { delay, key }); biên nhận là { id, name, status }`,
	"send":     `queue.send không có: gửi qua handle đã import, camOn.send(data), không gửi bằng tên chuỗi`,
	"jobs":     `queue.jobs đã bỏ: các lần chạy là queue.runs() hay queue.status("failed").runs(), định nghĩa là queue.list()`,
	"failed":   `queue.failed đã bỏ: queue.status("failed").runs()`,
	"resend":   `queue.resend không có: send(data) gửi việc từ dữ liệu người gọi đưa, replay(id) tạo việc từ lịch sử`,
}

// queueJobRemoved is the same for the words of a job's handle.
var queueJobRemoved = map[string]string{
	"handle":   `.handle đã bỏ: handler là tham số của queue.job((context) => { … })`,
	"success":  `.success đã bỏ: việc sau khi chạy xong viết ở cuối handler`,
	"error":    `.error đã bỏ: handler biết lần thử cuối qua context.job.isFinalAttempt; các lần chạy thất bại đọc bằng queue.status("failed").runs()`,
	"parallel": `.parallel đã bỏ: số lần chạy cùng lúc là luật của cả hàng đợi, một dòng trong .shared/queue/config.work: queue.parallel(4)`,
	"keep":     `.keep đã bỏ: thời gian giữ lịch sử là luật của cả hàng đợi, một dòng trong .shared/queue/config.work: queue.keep("30d")`,
	"list":     `.list() của một job không có: định nghĩa đọc bằng queue.list(), các lần chạy của job bằng .runs()`,
}

// QueueRemovedWord is what the spec writes for word, a word of kitwork/queue it replaced.
func QueueRemovedWord(word string) (string, bool) {
	message, removed := queueRemoved[word]
	return message, removed
}

// QueueJobRemovedWord is what the spec writes for word, a word of a job's handle it replaced.
func QueueJobRemovedWord(word string) (string, bool) {
	message, removed := queueJobRemoved[word]
	return message, removed
}

// queueJobOutside is the error of queue.job written outside the app's .shared/queue/ (mục 2).
const queueJobOutside = `queue.job chỉ khai trong .shared/queue/ của app: export const camOn = queue.job(fn) trong .shared/queue/camOn.work, tên job là tên export; file khác import { camOn } from "app/.shared/queue/camOn.work" rồi camOn.send(data)`

// queueReceiptQueued is the error of reading queued off the receipt of a send (queue.dispatch gave
// { queued, id }).
const queueReceiptQueued = `biên nhận của send không có queued: send không lỗi là lần chạy đã lưu; biên nhận là { id, name, status: "pending" }, thêm duplicate: true khi trùng key`

// QueueBundle is what the bundler found of the app's .shared/queue/ for the resolver: the functions its
// files are wrapped in (where queue.job is written), and the bindings an import of one declares, each a
// job's handle (the camOn of import { camOn } from "app/.shared/queue/camOn.work").
type QueueBundle struct {
	Modules map[*FunctionLiteral]string
	Handles map[*Identifier]string
}

func (b *bundler) queueBundle() *QueueBundle {
	if b.queue == nil {
		b.queue = &QueueBundle{Modules: map[*FunctionLiteral]string{}, Handles: map[*Identifier]string{}}
	}
	return b.queue
}

// queueImports records the bindings of an import of a file of .shared/queue/: each is a job's handle.
func (b *bundler) queueImports(absPath string, bindings []Statement) {
	module, isQueue := b.queueModule(absPath)
	if !isQueue {
		return
	}
	for _, binding := range bindings {
		if declared, ok := binding.(*VarStatement); ok && len(declared.Names) == 1 {
			b.queueBundle().Handles[declared.Names[0]] = module
		}
	}
}

// ── the resolver: the queue's old spellings where they are written (KITWORK_QUEUE.md mục 14) ─────────
//
// Every check below only ever fails: a program that writes none of them resolves and compiles byte for byte
// as before.

// markQueue marks a declared variable as the module kitwork/queue (import * as queue from "kitwork/queue",
// import { queue } from "kitwork"), a job's handle (an import of a file of .shared/queue/), or the receipt of
// a send (const receipt = camOn.send(…)).
func (r *resolver) markQueue(node *VarStatement, name *Identifier, declared *binding) {
	if declared == nil || node.Pattern != nil {
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok {
		declared.queueModule = (node.DestructMode == DestructNone && module == "queue") ||
			(node.DestructMode == DestructObject && module == "" && name.Value == "queue")
	}
	if r.queue != nil && r.queue.Handles[name] != "" {
		declared.queueHandle = r.queue.Handles[name]
	}
	if node.Token.Kind == Const && node.DestructMode == DestructNone && len(node.Names) == 1 {
		if call, isCall := node.Value.(*MethodCallExpression); isCall && r.queueSend(call) {
			declared.queueReceipt = true
		}
	}
}

// queueSend reports handle.send(…), a send on a job's handle.
func (r *resolver) queueSend(call *MethodCallExpression) bool {
	if call.Method == nil || call.Method.Value != "send" {
		return false
	}
	handle, ok := call.Object.(*Identifier)
	if !ok {
		return false
	}
	found := r.lookup(handle.Value)
	return found != nil && found.queueHandle != ""
}

// queueDeclaration refuses an import of a removed word of kitwork/queue (import { dispatch } from
// "kitwork/queue", const { jobs } = queue), and of job outside the app's .shared/queue/.
func (r *resolver) queueDeclaration(node *VarStatement) {
	var words []*Identifier
	if node.Pattern != nil {
		namespace, isName := node.Value.(*Identifier)
		if !isName || node.Pattern.Array {
			return
		}
		if found := r.lookup(namespace.Value); found == nil || !found.queueModule {
			return
		}
		for _, element := range node.Pattern.Elements {
			words = append(words, &Identifier{Token: element.KeyToken, Value: element.Key})
		}
	} else if module, ok := kitworkModulePath(node.Value); ok {
		if module != "queue" || node.DestructMode != DestructObject {
			return
		}
		words = node.Names
	} else if member, isMember := node.Value.(*MemberExpression); isMember && member.Property != nil {
		if module, ok := kitworkModulePath(member.Object); !ok || module != "queue" {
			return
		}
		words = []*Identifier{member.Property}
	}
	for _, word := range words {
		if message, removed := queueRemoved[word.Value]; removed {
			r.fail(fmt.Errorf("%s: %s", r.where(word.Token), message))
			return
		}
		if FolderConfigWord("queue", word.Value) {
			// import { parallel } from "kitwork/queue": a call of config.work is written on the namespace
			r.fail(fmt.Errorf("%s: %s", r.where(word.Token), r.folderWordMisplaced("queue", word.Value)))
			return
		}
		if word.Value == "job" && !r.inQueueModule {
			r.fail(fmt.Errorf("%s: %s", r.where(word.Token), queueJobOutside))
			return
		}
	}
}

// queueMember refuses, where it is written, a word of kitwork/queue or of a job's handle the spec
// replaced (queue.dispatch, camOn.success), queue.job outside the app's .shared/queue/, and queued read off
// the receipt of a send.
func (r *resolver) queueMember(object Expression, property *Identifier) bool {
	if property == nil {
		return false
	}
	switch root := object.(type) {
	case *Identifier:
		found := r.lookup(root.Value)
		if found == nil {
			return false
		}
		switch {
		case found.queueModule:
			if message, removed := queueRemoved[property.Value]; removed {
				r.fail(fmt.Errorf("%s: %s", r.where(property.Token), message))
				return true
			}
			if FolderConfigWord("queue", property.Value) {
				// a call of config.work written as a line of its own there never gets here (folderCall)
				r.fail(fmt.Errorf("%s: %s", r.where(property.Token), r.folderWordMisplaced("queue", property.Value)))
				return true
			}
			if property.Value == "job" && !r.inQueueModule {
				r.fail(fmt.Errorf("%s: %s", r.where(property.Token), queueJobOutside))
				return true
			}
		case found.queueHandle != "":
			if message, removed := queueJobRemoved[property.Value]; removed {
				r.fail(fmt.Errorf("%s: %s%s", r.where(property.Token), root.Value, message))
				return true
			}
		case found.queueReceipt && property.Value == "queued":
			r.fail(fmt.Errorf("%s: %s", r.where(property.Token), queueReceiptQueued))
			return true
		}
	case *MethodCallExpression:
		if property.Value == "queued" && r.queueSend(root) {
			r.fail(fmt.Errorf("%s: %s", r.where(property.Token), queueReceiptQueued))
			return true
		}
	}
	return false
}

// queueRead reports a read of the queue: the module, a handle, or either after status(…) or where(…).
func (r *resolver) queueRead(e Expression) bool {
	switch node := e.(type) {
	case *Identifier:
		found := r.lookup(node.Value)
		return found != nil && (found.queueModule || found.queueHandle != "")
	case *MethodCallExpression:
		return node.Method != nil && (node.Method.Value == "status" || node.Method.Value == "where") && r.queueRead(node.Object)
	}
	return false
}

// queueCall refuses, besides what queueMember refuses, the two reads mục 8 rules out: status(…).list()
// (a status is a run's, list() reads definitions) and runs({ where }) (a condition is written before the
// action, where(fn).runs()).
func (r *resolver) queueCall(node *MethodCallExpression) bool {
	if node.Method == nil {
		return false
	}
	if r.queueMember(node.Object, node.Method) {
		return true
	}
	word := node.Method.Value
	if status, isCall := node.Object.(*MethodCallExpression); word == "list" && isCall && status.Method != nil &&
		status.Method.Value == "status" && r.queueRead(status.Object) {
		written := "…"
		if len(status.Arguments) == 1 {
			if literal, isLiteral := status.Arguments[0].(*Literal); isLiteral && literal.Value.K == value.String {
				written = literal.Value.Text()
			}
		}
		r.fail(fmt.Errorf(`%s: status("%s").list() không có: trạng thái chỉ có ở lần chạy, viết status("%s").runs()`, r.where(node.Method.Token), written, written))
		return true
	}
	if (word == "runs" || word == "list") && len(node.Arguments) == 1 && r.queueRead(node.Object) {
		if options, isObject := node.Arguments[0].(*ObjectLiteral); isObject {
			for _, entry := range options.Entries {
				if entry.IsSpread {
					continue
				}
				var key string
				var at Token
				switch written := entry.Key.(type) {
				case *Identifier:
					key, at = written.Value, written.Token
				case *Literal:
					key, at = written.Value.Text(), written.Token
				}
				if key == "where" {
					r.fail(fmt.Errorf("%s: %s({ where }) không có: điều kiện viết trước hành động, queue.where(fn).%s()", r.where(at), word, word))
					return true
				}
			}
		}
	}
	return false
}

// queueModule reports a file of the app's .shared/queue/, at any depth, and its name from the app's folder.
// A file of no app has no queue folder.
func (b *bundler) queueModule(absPath string) (string, bool) {
	if b.appRoot == "" {
		return "", false
	}
	relative, err := b.appRelative(absPath)
	if err != nil {
		return "", false
	}
	relative = filepath.ToSlash(relative)
	segments := strings.Split(relative, "/")
	if len(segments) < 3 || segments[0] != ".shared" || segments[1] != "queue" {
		return "", false
	}
	return relative, true
}

// queueExportsRule is the error of a file of .shared/queue/ whose exports would give a job a name other
// than the one it is declared under: a job is named by its export, one job one name (mục 3).
func (b *bundler) queueExportsRule(prog *Program) error {
	if prog.HasDefault {
		return fmt.Errorf("%s: export default trong .shared/queue/: job mang tên export của nó, viết export const camOn = queue.job(…)", b.place(b.defaultToken(prog)))
	}
	for _, alias := range prog.ExportAliases {
		return fmt.Errorf("%s: export { %s as %s } trong .shared/queue/: job mang tên export của nó, một job một tên; viết export const %s = queue.job(…)",
			b.place(alias.Token), alias.Local, alias.Exported, alias.Exported)
	}
	return nil
}

// defaultToken is where a program's export default is written, as far as its statements say.
func (b *bundler) defaultToken(prog *Program) Token {
	for _, statement := range prog.Statements {
		if binding, ok := statement.(*VarStatement); ok && len(binding.Names) == 1 && binding.Names[0].Value == DefaultExportName {
			return binding.Names[0].Token
		}
	}
	if len(prog.Statements) > 0 {
		return Token{Source: getNodeSource(prog.Statements[0]), Position: getNodePosition(prog.Statements[0])}
	}
	return Token{}
}

// namedExportReturn is the export return of a file of .shared/queue/ or of .schedule/, its exports' names in
// the order they are written last (the first name a job or a schedule is exported under is its name):
//
//	return kitwork().queue.#exports({ camOn: camOn, … }, ".shared/queue/camOn.work", ["camOn", …]);
//	return kitwork().schedule.#exports({ baoCaoSang: baoCaoSang, … }, ".schedule/baoCao.work", ["baoCaoSang", …]);
func namedExportReturn(prog *Program, origin Token, module, word string) Statement {
	ret := exportReturn(prog, origin).(*ReturnStatement)
	namespace := &MemberExpression{
		Token:    tokenFrom(origin, Dot),
		Object:   &CallExpression{Token: tokenFrom(origin, LeftParen), Function: ident("kitwork", origin)},
		Property: ident(word, origin),
	}
	text := func(s string) Expression {
		return &Literal{Token: Token{Kind: String, Value: value.NewString(s), Source: origin.Source, Position: origin.Position}, Value: value.NewString(s)}
	}
	order := &ArrayLiteral{Token: tokenFrom(origin, LeftBracket)}
	for _, name := range prog.Exports {
		order.Elements = append(order.Elements, text(name))
	}
	ret.ReturnValue = &MethodCallExpression{
		Token:     tokenFrom(origin, Dot),
		Object:    namespace,
		Method:    ident(QueueExportsMethod, origin),
		Arguments: []Expression{ret.ReturnValue, text(module), order},
	}
	return ret
}

// CompileModuleFileInApp compiles a file of an app as a module: its program returns the file's export
// object, as an import of it sees it (a file of .shared/queue/ names its jobs on the way out, a file of
// .schedule/ its schedules). The loaders of .shared/queue/ and .schedule/ run it to find the jobs and the
// schedules a file exports, and under which names.
func CompileModuleFileInApp(appRoot, path string) (*Bytecode, error) {
	return compileModuleFile(appRoot, path, nil)
}

// CompileComponentFile compiles a site's components.work as a module, each component.define readied for the
// bridge (router_view.go, rewriteComponentDefines): the loader of a component's job runs it to find the
// function a name defines (work/component_call.go).
func CompileComponentFile(appRoot, path string) (*Bytecode, error) {
	return compileModuleFile(appRoot, path, rewriteComponentDefines)
}

// compileModuleFile is CompileModuleFileInApp, rewrite (when not nil) readying the file's own program before
// anything is bundled into it.
func compileModuleFile(appRoot, path string, rewrite func(*Program)) (*Bytecode, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	root := appRoot
	if root != "" {
		if resolved, err := filepath.Abs(root); err == nil {
			root = resolved
		}
	}
	b := &bundler{
		modules:   map[string]string{},
		visiting:  map[string]bool{},
		sources:   map[string]string{},
		debugRoot: filepath.Dir(absolute),
		appRoot:   root,
		entry:     absolute,
		rewrite:   rewrite,
	}
	module, err := b.ensureModule(absolute)
	if err != nil {
		return nil, err
	}
	origin := Token{Source: b.sourceName(absolute)}
	program := &Program{Database: b.database, Queue: b.queue, Schedule: b.schedule, App: root != ""}
	program.Statements = append(program.Statements, b.defs...)
	program.Statements = append(program.Statements, &ReturnStatement{Token: tokenFrom(origin, Return), ReturnValue: ident(module, origin)})
	// the file first, as the entry of any other compile, then every module it bundles
	files := make([]string, 0, len(b.modules))
	for file := range b.modules {
		if file != absolute {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return compilePreparedFile(program, append([]string{absolute}, files...), b.sources)
}
