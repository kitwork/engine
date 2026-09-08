package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemplate(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The mistake this exists for: a colour where a triplet belongs. CSS drops the
// declaration without a word, so the only useful report is at build time.
func TestColourValueInADesignTokenIsReported(t *testing.T) {
	for _, value := range []string{"#00ff00", "#0f0", "rgb(0, 255, 0)", "red", "oklch(0.7 0.2 145)"} {
		dir := writeTemplate(t, "index.kitwork.html",
			"<style>[data-theme=\"dark\"] { --color-brand: "+value+"; }</style>")

		issues := checkColorTokenFormat(dir)
		if len(issues) != 1 {
			t.Fatalf("value %q produced %d issues, want 1", value, len(issues))
		}
		if !strings.Contains(issues[0].File, "index.kitwork.html:1") {
			t.Fatalf("the issue did not point at the file and line: %q", issues[0].File)
		}
		if !strings.Contains(issues[0].Err.Error(), value) {
			t.Fatalf("the report did not quote the offending value: %v", issues[0].Err)
		}
	}
}

// The control. Flagging a correct declaration would make the check worse than
// having none, because every site would learn to ignore it.
func TestTripletAndAliasAreAccepted(t *testing.T) {
	dir := writeTemplate(t, "index.kitwork.html", `<style>
:root { --color-canvas: 244, 243, 240; --color-brand:248,34,68 }
[data-theme="dark"] { --color-paper: var(--color-canvas); }
.x { color: rgb(var(--color-brand, 248, 34, 68)); }
</style>`)

	if issues := checkColorTokenFormat(dir); len(issues) != 0 {
		t.Fatalf("valid declarations were reported: %v", issues)
	}
}

// A reference is not a declaration. Reporting every var(--color-x) use would
// bury the real finding.
func TestReferencesAreNotReported(t *testing.T) {
	dir := writeTemplate(t, "index.kitwork.html",
		`<div class="x" style="color: var(--color-brand)"></div>`)

	if issues := checkColorTokenFormat(dir); len(issues) != 0 {
		t.Fatalf("a variable reference was reported as a declaration: %v", issues)
	}
}

func TestEveryOffenderOnOneLineIsReported(t *testing.T) {
	dir := writeTemplate(t, "index.kitwork.html",
		`<style>:root{--color-a:#111;--color-b:#222;--color-c: 1, 2, 3}</style>`)

	if issues := checkColorTokenFormat(dir); len(issues) != 2 {
		t.Fatalf("got %d issues, want 2 (the third declaration is valid)", len(issues))
	}
}

// A value built at runtime cannot be judged here. Reporting it would make every
// finding suspect, and a check nobody trusts reports nothing at all.
func TestInterpolatedValuesAreNotReported(t *testing.T) {
	dir := writeTemplate(t, "index.kitwork.html", `<script>
  root.style.setProperty('--color-brand', `+"`${brandRgb}`"+`);
</script>
<style>:root { --color-ink: {{ theme.ink }} }</style>`)

	if issues := checkColorTokenFormat(dir); len(issues) != 0 {
		t.Fatalf("an interpolated value was reported: %v", issues)
	}
}

// The control: skipping interpolation must not become skipping everything.
func TestInterpolationSkipDoesNotHideARealColour(t *testing.T) {
	dir := writeTemplate(t, "index.kitwork.html",
		`<style>:root { --color-a: ${x}; --color-b: #ff0000 }</style>`)

	issues := checkColorTokenFormat(dir)
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1 — only the literal colour", len(issues))
	}
	if !strings.Contains(issues[0].Err.Error(), "#ff0000") {
		t.Fatalf("the wrong declaration was reported: %v", issues[0].Err)
	}
}
