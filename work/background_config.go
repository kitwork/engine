package work

import (
	"fmt"
	"path/filepath"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// ── the config.work of .shared/queue/ and of .schedule/ (KITWORK_QUEUE.md mục 2, KITWORK_SCHEDULE.md mục 2) ──
//
// A folder's config.work configures it with calls on the module's namespace, one call per line at its top
// level, never chained (24/10/2026):
//
//	// app/.shared/queue/config.work
//	import * as queue from "kitwork/queue";
//	queue.parallel(4);
//	queue.keep("30d");
//
// The compiler refuses, where it sees them, a call written anywhere else, chained or made twice, and the
// const of 17/10 (compiler/folder_config.go). The loader runs the file with modules that record each call
// (folderSettings), then reads what each was handed (readQueueConfig, readScheduleConfig). A call the
// compiler could not follow (the namespace under another name) is refused here when it runs, in the same
// words.

// folderSettings is what the config.work of module's folder called as the loader ran it, in the order
// called: one VM, one goroutine. source is the name the compiler gives the file in its program's places (its
// file name, CompileConfigFileInApp), against which a call is checked to be written in it.
type folderSettings struct {
	module   string
	source   string
	calls    []folderSetting
	problems []folderProblem
}

// folderSetting is one call: its word, what it was handed, and where it is written.
type folderSetting struct {
	word  string
	args  []value.Value
	place string // line:column in config.work, "" when the VM does not say
	line  int
}

// folderProblem is a call the loader says and leaves out, with where it is written.
type folderProblem struct {
	place, message string
}

// folderPlace is where a call is written for a boot line: the file from the app's folder, then line:column.
func folderPlace(where, place string) string {
	if place == "" {
		return where
	}
	return where + ":" + place
}

// only is the one argument a call was handed; nil when it was handed none, or more than one.
func (s folderSetting) only() value.Value {
	if len(s.args) != 1 {
		return value.Value{K: value.Nil}
	}
	return s.args[0]
}

// call is module.word(args) as the module runs it: recorded when s is the config.work of module's folder the
// loader runs and the call is written at that file's top level, refused anywhere else: another folder's
// config.work, a library the file imports (its functions and its own top level), a function of the file. A
// word called a second time is said, and the first call kept. It gives back nothing to chain.
func (s *folderSettings) call(vm *runtime.VM, module, word string, args []value.Value) value.Value {
	if s == nil || s.module != module {
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.FolderCallOutside(module, word))
	}
	var location runtime.SourceLocation
	if vm != nil && vm.FrameIdx >= 0 && vm.FrameIdx < len(vm.Frames) {
		location = vm.Program().SourceAt(vm.Frames[vm.FrameIdx].LastIP)
	}
	switch {
	case location.File != "" && location.File != s.source:
		// written in a library the file imports, which the VM names in its place
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.FolderCallOutside(module, word))
	case vm == nil || vm.FrameIdx != 0:
		// a function of the file, or a library's top level, which runs as one (bundler.go)
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.FolderCallNotAtTop(module, word))
	}
	setting := folderSetting{word: word, args: append([]value.Value(nil), args...)}
	if location.Line > 0 {
		setting.place, setting.line = fmt.Sprintf("%d:%d", location.Line, location.Column), int(location.Line)
	}
	result := value.Value{K: value.Proxy, V: &folderCallResult{module: module, word: word}}
	for _, earlier := range s.calls {
		if earlier.word == word {
			s.problems = append(s.problems, folderProblem{place: setting.place,
				message: compiler.FolderCallTwice(module, word, earlier.line, setting.line)})
			return result
		}
	}
	s.calls = append(s.calls, setting)
	return result
}

// folderCallResult is what a call of a folder's config.work gives: nothing to chain, each call is a line of
// its own.
type folderCallResult struct {
	module, word string
}

func (r *folderCallResult) OnGet(key string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, compiler.FolderCallChained(r.module, r.word, key))
}

func (r *folderCallResult) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (r *folderCallResult) OnInvoke(method string, _ ...value.Value) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, compiler.FolderCallChained(r.module, r.word, method))
}

// runFolderConfig compiles and runs file, the config.work of module's folder, in a VM of its own whose modules
// record its calls: what it called, or nil when it did not compile or failed as it ran, said on the server's
// output after label with where (the file from the app's folder). A key the file requires that the app's
// environment has not holds it back, as one that does not compile (environment.go).
func (t *Tenant) runFolderConfig(file, module, label, where string) *folderSettings {
	bc, err := compiler.CompileConfigFileInApp(t.resolveApp(), file)
	if err == nil {
		if missing := t.missingEnvironment(bc); missing != nil {
			err = missing
		}
	}
	if err != nil {
		fmt.Printf("%s compile %s: %v\n", label, where, err)
		return nil
	}
	settings := &folderSettings{module: module, source: filepath.ToSlash(filepath.Base(file))}
	vm := runtime.New(bc.Program)
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&KitWork{tenant: t, vm: vm, folderSettings: settings})
	})
	globals := make(map[string]value.Value, len(t.vm.Globals)+1)
	for name, item := range t.vm.Globals {
		globals[name] = item
	}
	globals[kitwork] = kitworkFunc
	vm.Builtins = []value.Value{kitworkFunc}
	vm.Globals = globals
	vm.MaxEnergy = t.MaxEnergy
	finished := loadingFile(vm)
	result := vm.Run()
	finished()
	if result.K == value.Invalid {
		fmt.Printf("%s run %s: %s\n", label, where, result.Text())
		return nil
	}
	for _, problem := range settings.problems {
		fmt.Printf("%s %s: %s\n", label, folderPlace(where, problem.place), problem.message)
	}
	return settings
}
