package work

import (
	"net/http"
	"strings"
	"testing"
)

// napas.validate() and napas.generate() of a payment that lacks what a payload needs fail with
// INVALID_ARGUMENT (14/10/2026), the program's argument. validate() gave nothing at all: a Go method of
// no argument is read as a field, and that path dropped its error, so { value: null, error: null } for
// a payment with no bank; generate() was ERROR. CONTROL: a payment with a bank and an amount validates
// and generates its payload.
func TestNapasFailsWithInvalidArgument(t *testing.T) {
	serve := safeSite(t, map[string]string{
		"": `import { router, napas } from "kitwork";
router.get((ctx) => {
  const checked = safe(() => napas.validate());
  const made = safe(() => napas.generate());
  napas.bank("970436", "0011001234567").amount(10000);
  const fine = safe(() => napas.validate());
  const payload = safe(() => napas.generate());
  return ctx.json({
    checked: checked.error.code, checkedSays: checked.error.message,
    made: made.error.code,
    fine: fine.error, payload: payload.value.length > 20,
  });
});
`,
	})
	got := serve("/")
	body := strings.TrimSpace(got.Body.String())
	for _, want := range []string{`"checked":"INVALID_ARGUMENT"`, `"checkedSays":"call validate: napas:`, `"made":"INVALID_ARGUMENT"`, `"fine":null`, `"payload":true`} {
		if got.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("GET / = %d %s, lacks %s", got.Code, body, want)
		}
	}
}
