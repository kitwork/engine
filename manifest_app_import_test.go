package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// The host's manifest belongs to no app, so it has no app/ (22/10/2026): an import from app/ is a compile
// error that says so at its place, even when a folder named app/ stands beside it.
func TestApplicationWorkHasNoAppImport(t *testing.T) {
	dir := writeFolder(t, map[string]string{
		"application.work":      "import * as app from \"kitwork/application\";\nimport { port } from \"app/.shared/port.work\";\napp.run(port);\n",
		"app/.shared/port.work": "export const port = 3000;",
	})
	_, err := evalConfigJS(filepath.Join(dir, "application.work"))
	want := `application.work:2:22: import "app/.shared/port.work": file này không thuộc app nào nên không có app/`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("evalConfigJS = %v, want %q", err, want)
	}
}
