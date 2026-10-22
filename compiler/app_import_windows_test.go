//go:build windows

package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// shortName is the 8.3 name Windows gives the folder path (DATABA~1 for .database), "" when the volume
// makes none.
func shortName(t *testing.T, path string) string {
	t.Helper()
	long, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, syscall.MAX_PATH)
	n, err := syscall.GetShortPathName(long, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || int(n) > len(buffer) {
		return ""
	}
	short := filepath.Base(syscall.UTF16ToString(buffer[:n]))
	if strings.EqualFold(short, filepath.Base(path)) {
		return ""
	}
	return short
}

// The 8.3 name Windows gives .database (DATABA~1) passes every check of the path as written; the disk has
// the last word, past the short name: app/DATABA~1/… and ../DATABA~1/… are the app's data, refused.
func TestAppImportRefusesTheShortNameOfTheData(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/.database/files/notes.work": `export const notes = "data";`,
	})
	app := filepath.Join(root, "apps", "acme")
	short := shortName(t, filepath.Join(app, ".database"))
	if short == "" {
		t.Skip("this volume makes no 8.3 names")
	}
	for relative, content := range map[string]string{
		"shop.com/router.work":       `import { notes } from "app/` + short + `/files/notes.work";`,
		"shop.com/local/router.work": `import { notes } from "../../` + short + `/files/notes.work";`,
	} {
		full := filepath.Join(app, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refused := `: .database/ là dữ liệu của app, không import được; code dùng chung đặt trong .shared/`
	for _, c := range []struct{ entry, want string }{
		{filepath.Join(app, "shop.com", "router.work"), `router.work:1:23: import "app/` + short + `/files/notes.work"` + refused},
		{filepath.Join(app, "shop.com", "local", "router.work"), `router.work:1:23: import "../../` + short + `/files/notes.work"` + refused},
	} {
		_, err := CompileFileInApp(app, c.entry)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s:\n  error %v\n  want  %s", c.entry, err, c.want)
		}
	}
}
