package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// The default codec (decimal → base58) must round-trip, including values FAR bigger than uint64 —
// proving the big.Int core, not a machine integer.
func TestShortbaseDefaultRoundtrip(t *testing.T) {
	sb := (&KitWork{}).Shortbase()
	for _, v := range []string{"0", "1", "12345", "999999999999999999999999999999"} {
		code := sb.Encode(value.NewString(v))
		if code.K != value.String {
			t.Fatalf("encode(%s) = %v", v, code)
		}
		back := sb.Decode(code)
		if back.String() != v {
			t.Errorf("roundtrip %q → %q → %q", v, code.String(), back.String())
		}
	}
	// A large value encodes SHORT.
	if c := sb.Encode(value.NewString("1000000000")).String(); len(c) > 6 {
		t.Errorf("encode(1e9) = %q (%d chars), want ≤ 6", c, len(c))
	}
}

// The value under test: a dotted decimal over an alphabet that INCLUDES ".", re-encoded to base58 and
// brought back EXACTLY — the general case my integer version wrongly dropped.
func TestShortbaseDottedValue(t *testing.T) {
	sb := (&KitWork{}).Shortbase().From(value.NewString("0123456789."))

	const v = "12312321.1232134124"
	code := sb.Encode(value.NewString(v))
	if code.K != value.String || code.String() == "" {
		t.Fatalf("encode(%s) = %v", v, code)
	}
	if strings.Contains(code.String(), ".") {
		// the "." belongs to the FROM alphabet; the base58 code should not contain it
		t.Errorf("code %q leaked a '.' from the input alphabet", code.String())
	}
	if back := sb.Decode(code); back.String() != v {
		t.Errorf("dotted roundtrip failed: %q → %q → %q", v, code.String(), back.String())
	}
}

// Validation: a character outside the relevant alphabet makes encode/decode return nil, so a caller
// can distinguish a real value/code from junk (a bogus /:code → 404, not a wrong lookup).
func TestShortbaseRejectsInvalid(t *testing.T) {
	sb := (&KitWork{}).Shortbase() // default: from = base11 (0-9 .), to = base62 (1234567890 A-Z a-z)

	// A letter is not in the FROM alphabet (base11 = digits + '.') → encode rejects it.
	if got := sb.Encode(value.NewString("12a5")); got.K != value.Nil {
		t.Errorf("encode(12a5) over base11 should be nil, got %v", got)
	}
	// base62 has no '.' '-' '_' or space → a code using them is invalid.
	for _, bad := range []string{".", "1.5", "a-b", "a_b", "", " ", "hi there"} {
		if got := sb.Decode(value.NewString(bad)); got.K != value.Nil {
			t.Errorf("decode(%q) should be nil, got %v", bad, got)
		}
	}
}

// prefix/suffix wrap the code as literal markers, and their characters are RESERVED — the body must
// never contain them — while the whole thing still round-trips exactly.
func TestShortbasePrefixSuffix(t *testing.T) {
	// "KIT" overlaps base58 (K, I, T) → they must be absent from the body.
	sb := (&KitWork{}).Shortbase().Prefix(value.NewString("KIT")).Suffix(value.NewString("_v1"))

	const v = "12345678901234567890"
	code := sb.Encode(value.NewString(v)).String()
	if !strings.HasPrefix(code, "KIT") || !strings.HasSuffix(code, "_v1") {
		t.Fatalf("code %q missing markers", code)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(code, "KIT"), "_v1")
	if strings.ContainsAny(body, "KIT_v1") {
		t.Errorf("body %q contains a reserved marker character", body)
	}
	if back := sb.Decode(value.NewString(code)); back.String() != v {
		t.Errorf("prefix/suffix roundtrip failed: %q → %q → %q", v, code, back.String())
	}

	// A marker outside the `to` alphabet (e.g. "_") leaves the body base at full size.
	under := (&KitWork{}).Shortbase().Prefix(value.NewString("_"))
	uc := under.Encode(value.NewString("100")).String()
	if !strings.HasPrefix(uc, "_") || under.Decode(value.NewString(uc)).String() != "100" {
		t.Errorf("underscore-prefix roundtrip failed: %q", uc)
	}
}

// The router test: `import { shortbase } from "kitwork"` used in a real .kitwork.js route, exercising
// encode AND decode across every mode — default (decimal→base58), a custom `from` alphabet with ".",
// prefix/suffix markers, and an invalid code → null — all round-tripping through the VM.
func TestShortbaseRouter(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-shortbase-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "acme", "localhost")
	os.MkdirAll(dir, 0755)

	router := `import { router, shortbase } from "kitwork";
const dotted = shortbase.from("0123456789.");
const tagged = shortbase.prefix("KIT").suffix("_v1");
router.get((ctx) => {
  const a = shortbase.encode("12345");
  const b = dotted.encode("12312321.1232134124");
  const c = tagged.encode("100");
  const bad = shortbase.decode("no.pe");
  return ctx.json({
    a: a, aBack: shortbase.decode(a),
    b: b, bBack: dotted.decode(b),
    c: c, cBack: tagged.decode(c),
    badIsNull: bad == null
  });
});`
	os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", rec.Code, rec.Body.String())
	}

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v — body: %s", err, rec.Body.String())
	}

	// default codec: decimal → base58 and back
	if out["aBack"] != "12345" {
		t.Errorf("default decode(encode(12345)) = %v, want 12345", out["aBack"])
	}
	// custom `from` with '.': round-trips EXACTLY, and the '.' never leaks into the code
	if out["bBack"] != "12312321.1232134124" {
		t.Errorf("dotted roundtrip = %v, want 12312321.1232134124", out["bBack"])
	}
	if code, _ := out["b"].(string); strings.Contains(code, ".") {
		t.Errorf("code %q leaked a '.' from the input alphabet", code)
	}
	// prefix/suffix markers wrap the code and still round-trip
	if out["cBack"] != "100" {
		t.Errorf("prefix/suffix decode = %v, want 100", out["cBack"])
	}
	if code, _ := out["c"].(string); !strings.HasPrefix(code, "KIT") || !strings.HasSuffix(code, "_v1") {
		t.Errorf("code %q missing KIT…_v1 markers", code)
	}
	// invalid character ('.' is not in the base62 `to` alphabet) → nil in Go, null in JS
	if out["badIsNull"] != true {
		t.Errorf("invalid code should decode to null, got badIsNull=%v", out["badIsNull"])
	}
}
