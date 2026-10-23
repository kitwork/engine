package work

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/compiler"
)

// BackgroundCheckIssue is a file of an app's background work (.schedule/, .shared/queue/) that would not
// load, and why.
type BackgroundCheckIssue struct {
	File string
	Err  error
}

// CheckScheduleFiles is the preflight of the schedules of .schedule/: each file compiled as the module its
// importers and the loader see (CompileModuleFileInApp), config.work as a config file. Schedules boot
// eagerly, so a source that cannot compile stops the app's clock long before a request would have revealed
// it; the old spellings (cron, _cron/, .handle(fn), a chain that breaks mục 11 with literals) are compile
// errors there. A _cron/ folder is an issue of its own: the engine no longer reads it. It never evaluates
// a declaration, so nothing registers, connects, or starts.
func CheckScheduleFiles(root, identity string) []BackgroundCheckIssue {
	appRoot := filepath.Join(root, identity)
	var issues []BackgroundCheckIssue
	if info, err := os.Stat(filepath.Join(appRoot, "_cron")); err == nil && info.IsDir() {
		issues = append(issues, BackgroundCheckIssue{File: filepath.Join(appRoot, "_cron"),
			Err: fmt.Errorf("_cron/ không còn đọc: lịch ở .schedule/<tên>.work, export const <tên> = schedule.daily(\"09:00\").run(fn)")})
	}
	return append(issues, checkFolder(appRoot, filepath.Join(appRoot, ".schedule"))...)
}

// CheckQueueFiles is the same preflight for the job files of .shared/queue/. A _queue/ folder is an issue of
// its own: the engine no longer reads it.
func CheckQueueFiles(root, identity string) []BackgroundCheckIssue {
	appRoot := filepath.Join(root, identity)
	var issues []BackgroundCheckIssue
	if info, err := os.Stat(filepath.Join(appRoot, "_queue")); err == nil && info.IsDir() {
		issues = append(issues, BackgroundCheckIssue{File: filepath.Join(appRoot, "_queue"),
			Err: fmt.Errorf("_queue/ không còn đọc: khai job trong .shared/queue/<tên>.work bằng export const <tên> = queue.job(fn)")})
	}
	return append(issues, checkFolder(appRoot, filepath.Join(appRoot, ".shared", "queue"))...)
}

// checkFolder compiles every .work directly inside dir as a module of the app, config.work as a config file.
func checkFolder(appRoot, dir string) []BackgroundCheckIssue {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return []BackgroundCheckIssue{{File: dir, Err: err}}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var issues []BackgroundCheckIssue
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
			issues = append(issues, BackgroundCheckIssue{File: file, Err: compileErr})
		}
	}
	return issues
}
