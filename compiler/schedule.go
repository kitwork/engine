package compiler

import (
	"fmt"
	"path/filepath"
	"strings"

	schedulerule "github.com/kitwork/engine/utilities/schedule"
	"github.com/kitwork/engine/value"
)

// A schedule of an app is declared in its .schedule/ (KITWORK_SCHEDULE.md mục 2, 6): a file-level const
// whose value is a rule ended by .run(fn) or .once(fn), exported, and named by the name it is exported
// under:
//
//	export const baoCaoSang = schedule.daily("09:00").retry(3).timeout("10m").run((context) => { … });
//
// As a job's handle (queue.go), a schedule's handle is evaluated again in every bundle that imports its file,
// so its name comes from the bundler: the export object of a file of .schedule/ goes out through
// kitwork().schedule.#exports({ … }, "<file>", [names]). The resolver refuses here, where they are written,
// the old spellings of the cron .schedule/ replaced, schedule.<rule>(…) outside .schedule/, and a chain
// written with literals that breaks a rule of mục 11, each in the words of utilities/schedule, which the
// runtime reads every chain through too.

// ScheduleExportsMethod is the method of the schedule module the export object of a file of .schedule/
// goes through.
const ScheduleExportsMethod = "#exports"

// CronRemoved is the error of the module kitwork had for its schedules until 22/10/2026: import { cron }
// from "kitwork", kitwork/cron, kitwork().cron.
const CronRemoved = `cron đã bỏ: lịch là import * as schedule from "kitwork/schedule", khai trong .schedule/<tên>.work bằng export const baoCaoSang = schedule.daily("09:00").run((context) => { … }); _cron/ không còn đọc`

// ScheduleRuleOutside is the error of a rule of kitwork/schedule written outside the app's .schedule/: here
// where the compiler sees it, at run time where it does not (a namespace passed through a function).
const ScheduleRuleOutside = `schedule.%s(…) chỉ khai trong .schedule/ của app: export const baoCaoSang = schedule.daily("09:00").run(fn) trong .schedule/baoCao.work, tên lịch là tên export; file khác import { baoCaoSang } from "app/.schedule/baoCao.work" rồi đọc baoCaoSang.runs()`

// ScheduleBundle is what the bundler found of the app's .schedule/ for the resolver: the functions its files
// are wrapped in (where a rule is written), and the bindings an import of one declares, each a schedule's
// handle.
type ScheduleBundle struct {
	Modules map[*FunctionLiteral]string
	Handles map[*Identifier]string
}

func (b *bundler) scheduleBundle() *ScheduleBundle {
	if b.schedule == nil {
		b.schedule = &ScheduleBundle{Modules: map[*FunctionLiteral]string{}, Handles: map[*Identifier]string{}}
	}
	return b.schedule
}

// scheduleModule reports a file of the app's .schedule/, and its name from the app's folder. A file of no
// app has no .schedule/.
func (b *bundler) scheduleModule(absPath string) (string, bool) {
	if b.appRoot == "" {
		return "", false
	}
	relative, err := b.appRelative(absPath)
	if err != nil {
		return "", false
	}
	relative = filepath.ToSlash(relative)
	segments := strings.Split(relative, "/")
	if len(segments) < 2 || segments[0] != ".schedule" || filepath.Base(relative) == "config.work" {
		return "", false
	}
	return relative, true
}

// scheduleImports records the bindings of an import of a file of .schedule/: each is a schedule's handle.
func (b *bundler) scheduleImports(absPath string, bindings []Statement) {
	module, isSchedule := b.scheduleModule(absPath)
	if !isSchedule {
		return
	}
	for _, binding := range bindings {
		if declared, ok := binding.(*VarStatement); ok && len(declared.Names) == 1 {
			b.scheduleBundle().Handles[declared.Names[0]] = module
		}
	}
}

// scheduleExportsRule is the error of a file of .schedule/ whose exports would give a schedule a name other
// than the one it is declared under: a schedule is named by its export, one schedule one name (mục 6).
func (b *bundler) scheduleExportsRule(prog *Program) error {
	if prog.HasDefault {
		return fmt.Errorf("%s: export default trong .schedule/: lịch mang tên export của nó, viết export const baoCaoSang = schedule.daily(…).run(fn)", b.place(b.defaultToken(prog)))
	}
	for _, alias := range prog.ExportAliases {
		return fmt.Errorf("%s: export { %s as %s } trong .schedule/: lịch mang tên export của nó, một lịch một tên; viết export const %s = schedule.daily(…).run(fn)",
			b.place(alias.Token), alias.Local, alias.Exported, alias.Exported)
	}
	return nil
}

// ── the resolver ──────────────────────────────────────────────────────────────────────────────────────
//
// Every check below only ever fails: a program that writes none of them resolves and compiles byte for byte
// as before.

// markSchedule marks a declared variable as the module kitwork/schedule (import * as schedule from
// "kitwork/schedule", import { schedule } from "kitwork"), a rule of it imported by name (import { daily }
// from "kitwork/schedule"), or a schedule's handle (an import of a file of .schedule/).
func (r *resolver) markSchedule(node *VarStatement, name *Identifier, declared *binding) {
	if declared == nil || node.Pattern != nil {
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok {
		declared.scheduleModule = (node.DestructMode == DestructNone && module == "schedule") ||
			(node.DestructMode == DestructObject && module == "" && name.Value == "schedule")
		if node.DestructMode == DestructObject && module == "schedule" && schedulerule.RuleWords[name.Value] {
			declared.scheduleRule = name.Value
		}
	}
	if r.schedule != nil && r.schedule.Handles[name] != "" {
		declared.scheduleHandle = r.schedule.Handles[name]
	}
}

// scheduleDeclaration refuses an import of cron (import { cron } from "kitwork", anything of kitwork/cron),
// and of a word of kitwork/schedule the module has not, or a rule of it outside the app's .schedule/
// (import { daily } from "kitwork/schedule", const { daily } = schedule).
func (r *resolver) scheduleDeclaration(node *VarStatement) {
	var words []*Identifier
	if node.Pattern != nil {
		namespace, isName := node.Value.(*Identifier)
		if !isName || node.Pattern.Array {
			return
		}
		if found := r.lookup(namespace.Value); found == nil || !found.scheduleModule {
			return
		}
		for _, element := range node.Pattern.Elements {
			words = append(words, &Identifier{Token: element.KeyToken, Value: element.Key})
		}
	} else if module, ok := kitworkModulePath(node.Value); ok {
		switch {
		case module == "cron":
			if len(node.Names) > 0 {
				r.fail(fmt.Errorf("%s: %s", r.where(node.Names[0].Token), CronRemoved))
			}
			return
		case module == "" && node.DestructMode == DestructObject:
			for _, name := range node.Names {
				if name.Value == "cron" {
					r.fail(fmt.Errorf("%s: %s", r.where(name.Token), CronRemoved))
					return
				}
			}
			return
		case module != "schedule" || node.DestructMode != DestructObject:
			return
		}
		words = node.Names
	} else if member, isMember := node.Value.(*MemberExpression); isMember && member.Property != nil {
		module, ok := kitworkModulePath(member.Object)
		switch {
		case ok && module == "" && member.Property.Value == "cron":
			r.fail(fmt.Errorf("%s: %s", r.where(member.Property.Token), CronRemoved))
			return
		case !ok || module != "schedule":
			return
		}
		words = []*Identifier{member.Property}
	}
	for _, word := range words {
		if message := r.scheduleWord(word.Value); message != "" {
			r.fail(fmt.Errorf("%s: %s", r.where(word.Token), message))
			return
		}
	}
}

// scheduleWord is the error of reading word off kitwork/schedule here, "" when it may be read.
func (r *resolver) scheduleWord(word string) string {
	if message, removed := schedulerule.RemovedWord(word); removed {
		return message
	}
	switch {
	case schedulerule.RuleWords[word]:
		if !r.inScheduleModule {
			return fmt.Sprintf(ScheduleRuleOutside, word)
		}
	case FolderConfigWord("schedule", word):
		// a call of config.work written as a line of its own there never gets here (folderCall)
		return r.folderWordMisplaced("schedule", word)
	case !schedulerule.ReadWords[word] && word != ScheduleExportsMethod:
		return schedulerule.UnknownWord(word)
	}
	return ""
}

// scheduleMember refuses, where it is written, a word kitwork/schedule has not or that is written outside
// its place (schedule.cron, schedule.daily in a route), and a word chained after a schedule's handle
// (baoCaoSang.retry, baoCaoSang.handle).
func (r *resolver) scheduleMember(object Expression, property *Identifier) bool {
	root, isName := object.(*Identifier)
	if property == nil || !isName {
		return false
	}
	found := r.lookup(root.Value)
	switch {
	case found == nil:
		return false
	case found.scheduleModule:
		if message := r.scheduleWord(property.Value); message != "" {
			r.fail(fmt.Errorf("%s: %s", r.where(property.Token), message))
			return true
		}
	case found.scheduleHandle != "":
		if message := handleWord(property.Value); message != "" {
			r.fail(fmt.Errorf("%s: %s%s", r.where(property.Token), root.Value, message))
			return true
		}
	}
	return false
}

// handleWord is the error of chaining word after a schedule's handle, "" when the handle has it.
func handleWord(word string) string {
	switch word {
	case "runs", "status", "where", "now":
		return ""
	case "list":
		return `.list() không có trên lịch: các lịch đọc bằng schedule.list(), các lần chạy của lịch bằng .runs()`
	}
	if message, removed := schedulerule.RemovedChained(word); removed {
		return message
	}
	switch word {
	case "run", "once":
		return ": lịch đã kết thúc bằng .run(…) hay .once(…); .run và .once loại trừ nhau và đứng cuối"
	case "at", "first", "last", "before", "after", "retry", "timeout", "timezone":
		return fmt.Sprintf(": sau .run(…)/.once(…) không nối gì; .%s viết trước .run(…)", word)
	}
	return ""
}

// scheduleRead reports a read of the schedules: the module, a handle, or either after status(…) or where(…).
func (r *resolver) scheduleRead(e Expression) bool {
	switch node := e.(type) {
	case *Identifier:
		found := r.lookup(node.Value)
		return found != nil && (found.scheduleModule || found.scheduleHandle != "")
	case *MethodCallExpression:
		return node.Method != nil && (node.Method.Value == "status" || node.Method.Value == "where") && r.scheduleRead(node.Object)
	}
	return false
}

// scheduleCall refuses, besides what scheduleMember refuses, the two reads mục 8 rules out
// (status(…).list(), runs({ where })), and a chain of a rule that breaks a rule of mục 11: each call of the
// chain is read through utilities/schedule, its literals by value, anything else by its place in the chain.
func (r *resolver) scheduleCall(node *MethodCallExpression) bool {
	if node.Method == nil {
		return false
	}
	if r.scheduleMember(node.Object, node.Method) {
		return true
	}
	word := node.Method.Value
	if status, isCall := node.Object.(*MethodCallExpression); word == "list" && isCall && status.Method != nil &&
		status.Method.Value == "status" && r.scheduleRead(status.Object) {
		written := "…"
		if len(status.Arguments) == 1 {
			if literal, isLiteral := status.Arguments[0].(*Literal); isLiteral && literal.Value.K == value.String {
				written = literal.Value.Text()
			}
		}
		r.fail(fmt.Errorf(`%s: status("%s").list() không có: trạng thái chỉ có ở lần chạy, viết status("%s").runs()`, r.where(node.Method.Token), written, written))
		return true
	}
	if word == "runs" && len(node.Arguments) == 1 && r.scheduleRead(node.Object) {
		if options, isObject := node.Arguments[0].(*ObjectLiteral); isObject {
			for _, entry := range options.Entries {
				if key, ok := entry.Key.(*Identifier); ok && !entry.IsSpread && key.Value == "where" {
					r.fail(fmt.Errorf("%s: runs({ where }) không có: điều kiện viết trước hành động, schedule.where(fn).runs()", r.where(key.Token)))
					return true
				}
			}
		}
	}
	return r.scheduleChain(node)
}

// chainCall is one call of a chain as the rule reads it: its word, where the word is written, its arguments.
type chainCall struct {
	word string
	at   Token
	args []schedulerule.Arg
}

// scheduleChain reads the chain node ends, when it begins with a rule of kitwork/schedule
// (schedule.daily("09:00")…, or daily("09:00")… of an import by name), once: the calls inside it are marked
// read.
func (r *resolver) scheduleChain(node *MethodCallExpression) bool {
	if r.scheduleChains[node] || !r.ruleChain(node) {
		return false
	}
	var calls []chainCall
	var current Expression = node
	for {
		switch call := current.(type) {
		case *MethodCallExpression:
			calls = append(calls, chainCall{word: call.Method.Value, at: call.Method.Token, args: chainArgs(call.Arguments)})
			if _, isName := call.Object.(*Identifier); isName {
				return r.readChain(node, calls)
			}
			current = call.Object
		case *CallExpression:
			function := call.Function.(*Identifier)
			calls = append(calls, chainCall{word: r.lookup(function.Value).scheduleRule, at: function.Token, args: chainArgs(call.Arguments)})
			return r.readChain(node, calls)
		}
	}
}

// ruleChain reports a chain of calls that begins with a rule of kitwork/schedule: schedule.daily(…)…, or
// daily(…)… of an import by name.
func (r *resolver) ruleChain(node *MethodCallExpression) bool {
	var current Expression = node
	for {
		switch call := current.(type) {
		case *MethodCallExpression:
			if call.Method == nil {
				return false
			}
			if root, isName := call.Object.(*Identifier); isName {
				found := r.lookup(root.Value)
				return found != nil && found.scheduleModule && schedulerule.RuleWords[call.Method.Value]
			}
			current = call.Object
		case *CallExpression:
			function, isName := call.Function.(*Identifier)
			if !isName {
				return false
			}
			found := r.lookup(function.Value)
			return found != nil && found.scheduleRule != ""
		default:
			return false
		}
	}
}

// readChain reads calls, written outermost first, from the rule on, and marks every call of the chain read.
func (r *resolver) readChain(node *MethodCallExpression, calls []chainCall) bool {
	var inner Expression = node
	for inner != nil {
		call, isMethod := inner.(*MethodCallExpression)
		if !isMethod {
			break
		}
		r.scheduleChains[call] = true
		inner = call.Object
	}
	first := calls[len(calls)-1]
	if !r.inScheduleModule {
		r.fail(fmt.Errorf("%s: %s", r.where(first.at), fmt.Sprintf(ScheduleRuleOutside, first.word)))
		return true
	}
	chain, err := schedulerule.Begin(first.word, first.args)
	if err != nil {
		r.fail(fmt.Errorf("%s: %v", r.where(first.at), err))
		return true
	}
	for index := len(calls) - 2; index >= 0; index-- {
		ended := chain.Ended()
		if err := chain.Then(calls[index].word, calls[index].args); err != nil {
			if ended == "" && schedulerule.IsEnd(calls[index].word) {
				// an end chained after this one is what is wrong, not what this one says of its arguments
				for later := index - 1; later >= 0; later-- {
					if schedulerule.IsEnd(calls[later].word) {
						r.fail(fmt.Errorf("%s: %v", r.where(calls[later].at), schedulerule.TwoEnds(calls[index].word)))
						return true
					}
				}
			}
			r.fail(fmt.Errorf("%s: %v", r.where(calls[index].at), err))
			return true
		}
	}
	return false
}

// chainArgs are the arguments of a call as the rule reads them at compile time: a literal string or number
// by its value, a function written there as a function, anything else unknown.
func chainArgs(arguments []Expression) []schedulerule.Arg {
	out := make([]schedulerule.Arg, len(arguments))
	for index, argument := range arguments {
		switch node := argument.(type) {
		case *Literal:
			switch node.Value.K {
			case value.String:
				out[index] = schedulerule.Arg{Kind: schedulerule.String, Text: node.Value.Text()}
			case value.Number:
				out[index] = schedulerule.Arg{Kind: schedulerule.Number, Number: node.Value.N}
			default:
				out[index] = schedulerule.Arg{Kind: schedulerule.Other}
			}
		case *FunctionLiteral:
			out[index] = schedulerule.Arg{Kind: schedulerule.Function}
		}
	}
	return out
}
