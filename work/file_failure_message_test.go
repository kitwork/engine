package work

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A file failure names the file as the program wrote it, never where the app lives on the server
// (14/10/2026): a site may hand error.message to a browser, as the skills say to. A path outside the
// app's folder is DENIED, "file: đường dẫn "…" nằm ngoài thư mục của app"; a file that is not there is
// INVALID_ARGUMENT, "open missing.txt: …". Both carried the server's absolute paths, the boundary's
// included. CONTROL: a file inside the app is written and read back.
func TestFileFailureNamesThePathAsWritten(t *testing.T) {
	serve := safeSite(t, map[string]string{
		"": `import { router, file } from "kitwork";
router.get((ctx) => {
  const outside = safe(() => file.read("../../../../etc/passwd"));
  const missing = safe(() => file.read("missing.txt"));
  const escaped = safe(() => file.write("../../../escape-write.txt", "x"));
  const encoded = safe(() => file.base64("../../x.png"));
  file.write("notes/ok.txt", "kept");
  return ctx.json({ outside: outside.error, missing: missing.error, escaped: escaped.error, encoded: encoded.error, kept: file.read("notes/ok.txt") });
});
`,
	})
	got := serve("/")
	var answer struct {
		Outside, Missing, Escaped, Encoded struct{ Message, Code string }
		Kept                               string
	}
	if err := json.Unmarshal(got.Body.Bytes(), &answer); err != nil || got.Code != http.StatusOK {
		t.Fatalf("GET / = %d %s (%v)", got.Code, got.Body.String(), err)
	}
	for name, failure := range map[string]struct{ Message, Code string }{
		"outside": answer.Outside, "missing": answer.Missing, "escaped": answer.Escaped, "encoded": answer.Encoded,
	} {
		if strings.Contains(failure.Message, "acme") || strings.Contains(failure.Message, "localhost") || strings.Contains(failure.Message, ":\\") {
			t.Errorf("%s: message %q shows where the app lives", name, failure.Message)
		}
	}
	if answer.Outside.Code != "DENIED" || answer.Outside.Message != `file: đường dẫn "../../../../etc/passwd" nằm ngoài thư mục của app` {
		t.Errorf("outside = %+v", answer.Outside)
	}
	if answer.Escaped.Code != "DENIED" || answer.Encoded.Code != "DENIED" {
		t.Errorf("escaped = %+v, encoded = %+v, want DENIED", answer.Escaped, answer.Encoded)
	}
	if answer.Missing.Code != "INVALID_ARGUMENT" || !strings.HasPrefix(answer.Missing.Message, "open missing.txt: ") {
		t.Errorf("missing = %+v, want INVALID_ARGUMENT open missing.txt: …", answer.Missing)
	}
	if answer.Kept != "kept" {
		t.Errorf("CONTROL: a file inside the app read back %q", answer.Kept)
	}
}
