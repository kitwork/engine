package work

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/compiler"
)

type CronCheckIssue struct {
	File string
	Err  error
}

// CheckCronFiles compiles every identity-level cron source without evaluating
// declarations or starting the scheduler.
func CheckCronFiles(root, identity string) []CronCheckIssue {
	return checkAppSources(root, identity, "_cron")
}

// CheckQueueFiles is the same preflight for the job files of .shared/queue/, each compiled as the module
// its importers and the loader see (CompileModuleFileInApp), config.work as a config file. Jobs boot
// eagerly, so a source that cannot compile takes the queue down long before a request would have revealed
// it — preflight is the only place that failure is cheap. A _queue/ folder is an issue of its own: the
// engine no longer reads it.
func CheckQueueFiles(root, identity string) []CronCheckIssue {
	appRoot := filepath.Join(root, identity)
	var issues []CronCheckIssue
	if info, err := os.Stat(filepath.Join(appRoot, "_queue")); err == nil && info.IsDir() {
		issues = append(issues, CronCheckIssue{File: filepath.Join(appRoot, "_queue"),
			Err: fmt.Errorf("_queue/ không còn đọc: khai job trong .shared/queue/<tên>.work bằng export const <tên> = queue.job(fn)")})
	}
	dir := filepath.Join(appRoot, ".shared", "queue")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return issues
	}
	if err != nil {
		return append(issues, CronCheckIssue{File: dir, Err: err})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".work") {
			continue
		}
		file := filepath.Join(dir, entry.Name())
		var compileErr error
		if entry.Name() == "config.work" {
			_, _, compileErr = compiler.CompileConfigFileInApp(appRoot, file)
		} else {
			_, compileErr = compiler.CompileModuleFileInApp(appRoot, file)
		}
		if compileErr != nil {
			issues = append(issues, CronCheckIssue{File: file, Err: compileErr})
		}
	}
	return issues
}

// checkAppSources compiles every .kitwork.js directly inside one app-level folder. It never
// evaluates declarations, so nothing registers, connects, or starts.
func checkAppSources(root, identity, folder string) []CronCheckIssue {
	appRoot := filepath.Join(root, identity)
	dir := filepath.Join(appRoot, folder)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return []CronCheckIssue{{File: dir, Err: err}}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var issues []CronCheckIssue
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".kitwork.js") {
			continue
		}
		file := filepath.Join(dir, entry.Name())
		if _, compileErr := compiler.CompileFileInApp(appRoot, file); compileErr != nil {
			issues = append(issues, CronCheckIssue{File: file, Err: compileErr})
		}
	}
	return issues
}
