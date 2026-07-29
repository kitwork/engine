package work

import (
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

// CheckQueueFiles is the same preflight for queue handlers. Both folders hold app-level background
// code that boots eagerly, so a source that cannot compile takes the app down long before a request
// would have revealed it — preflight is the only place that failure is cheap.
func CheckQueueFiles(root, identity string) []CronCheckIssue {
	return checkAppSources(root, identity, "_queue")
}

// checkAppSources compiles every .kitwork.js directly inside one app-level folder. It never
// evaluates declarations, so nothing registers, connects, or starts.
func checkAppSources(root, identity, folder string) []CronCheckIssue {
	dir := filepath.Join(root, identity, folder)
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
		if _, compileErr := compiler.CompileFile(file); compileErr != nil {
			issues = append(issues, CronCheckIssue{File: file, Err: compileErr})
		}
	}
	return issues
}
