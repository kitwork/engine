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
	dir := filepath.Join(root, identity, "_cron")
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
