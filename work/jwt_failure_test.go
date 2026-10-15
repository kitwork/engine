package work

import (
	"net/http"
	"strings"
	"testing"
)

// jwt.decode of what is not a token is INVALID_INPUT (14/10/2026), data from outside that cannot be
// read, as the closed set says of a token: it gave {}. jwt.verify stays data, null for a token it
// cannot trust. CONTROL: a token jwt.sign made decodes to its claims.
func TestJWTDecodeOfANonTokenIsInvalidInput(t *testing.T) {
	serve := safeSite(t, map[string]string{
		"": `import { router, jwt } from "kitwork";
router.get((ctx) => {
  const broken = safe(() => jwt.decode("%%%"));
  const parts = safe(() => jwt.decode("a.b"));
  const checked = safe(() => jwt.verify("not.a.token", "secret"));
  const claims = jwt.decode(jwt.sign({ who: "quoc" }, "secret"));
  return ctx.json({ broken: broken.error.code, says: broken.error.message, parts: parts.error.code, checked: checked.value, checkedError: checked.error, who: claims.who });
});
`,
	})
	got := serve("/")
	body := strings.TrimSpace(got.Body.String())
	for _, want := range []string{`"broken":"INVALID_INPUT"`, `"says":"jwt decode: invalid token format"`, `"parts":"INVALID_INPUT"`, `"checked":null`, `"checkedError":null`, `"who":"quoc"`} {
		if got.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("GET / = %d %s, lacks %s", got.Code, body, want)
		}
	}
}
