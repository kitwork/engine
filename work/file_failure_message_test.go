package work

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A file failure names the file as the program wrote it, never where the app lives on the server
// (14/10/2026; kitwork/file keeps it, KITWORK_FILE.md mục 12): a site may hand error.message to a browser,
// as the skills say to. A path that climbs out is INVALID_ARGUMENT before anything is touched, a file that
// is not there NOT_FOUND, the app's .env through app/ DENIED; none carries the server's absolute paths.
// CONTROL: a file of the data zone is written and read back.
func TestFileFailureNamesThePathAsWritten(t *testing.T) {
	serve := safeRouterWorkSite(t, map[string]string{
		"": `import * as router from "kitwork/router";
import * as file from "kitwork/file";
router.get((ctx) => {
  const outside = safe(() => file.text("../../../../etc/passwd"));
  const missing = safe(() => file.text("missing.txt"));
  const escaped = safe(() => file.open("app/../../escape-write.txt").write("x"));
  const env = safe(() => file.text("app/.env"));
  file.open("notes/ok.txt").write("kept");
  return ctx.json({ outside: outside.error, missing: missing.error, escaped: escaped.error, env: env.error, kept: file.text("notes/ok.txt") });
});
`,
	})
	got := serve("/")
	var answer struct {
		Outside, Missing, Escaped, Env struct{ Message, Code string }
		Kept                           string
	}
	if err := json.Unmarshal(got.Body.Bytes(), &answer); err != nil || got.Code != http.StatusOK {
		t.Fatalf("GET / = %d %s (%v)", got.Code, got.Body.String(), err)
	}
	for name, failure := range map[string]struct{ Message, Code string }{
		"outside": answer.Outside, "missing": answer.Missing, "escaped": answer.Escaped, "env": answer.Env,
	} {
		if strings.Contains(failure.Message, "acme") || strings.Contains(failure.Message, "localhost") || strings.Contains(failure.Message, ":\\") {
			t.Errorf("%s: message %q shows where the app lives", name, failure.Message)
		}
	}
	if answer.Outside.Code != "INVALID_ARGUMENT" || answer.Outside.Message != `đường dẫn file "../../../../etc/passwd" có "..": đường dẫn tính từ gốc vùng, không đi lên hay đứng yên` {
		t.Errorf("outside = %+v", answer.Outside)
	}
	if answer.Escaped.Code != "INVALID_ARGUMENT" || answer.Env.Code != "DENIED" {
		t.Errorf("escaped = %+v, env = %+v", answer.Escaped, answer.Env)
	}
	if answer.Missing.Code != "NOT_FOUND" || answer.Missing.Message != `không có file "missing.txt"` {
		t.Errorf("missing = %+v, want NOT_FOUND không có file \"missing.txt\"", answer.Missing)
	}
	if answer.Kept != "kept" {
		t.Errorf("CONTROL: a file of the data zone read back %q", answer.Kept)
	}
}
