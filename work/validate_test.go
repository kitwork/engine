package work

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func validateCtx(method, contentType, body string) *Context {
	req := httptest.NewRequest(method, "/register", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return &Context{request: &Request{router: &Router{request: req}}}
}

const registerRule = "password.length >= 6 && confirm == password && email.includes('@')"

// The end-to-end server half: a form POST re-checked with the SAME rule the client walked.
func TestValidateFormBody(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{"password=secret1&confirm=secret1&email=a%40b.vn", true},
		{"password=abc&confirm=abc&email=a%40b.vn", false},         // too short
		{"password=secret1&confirm=khac&email=a%40b.vn", false},    // mismatch
		{"password=secret1&confirm=secret1&email=khong-co", false}, // no @
		{"", false}, // empty submission — fail closed
	}
	for _, c := range cases {
		ctx := validateCtx("POST", "application/x-www-form-urlencoded", c.body)
		if got := ctx.Validate(value.New(registerRule)).Truthy(); got != c.want {
			t.Errorf("validate(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

// A JSON submission (fetch) judges the same way.
func TestValidateJSONBody(t *testing.T) {
	ctx := validateCtx("POST", "application/json",
		`{"password":"secret1","confirm":"secret1","email":"a@b.vn"}`)
	if !ctx.Validate(value.New(registerRule)).Truthy() {
		t.Error("valid JSON body should pass")
	}
	ctx = validateCtx("POST", "application/json",
		`{"password":"secret1","confirm":"khac","email":"a@b.vn"}`)
	if ctx.Validate(value.New(registerRule)).Truthy() {
		t.Error("mismatched JSON body should fail")
	}
}

// An explicit data map overrides the request body.
func TestValidateExplicitData(t *testing.T) {
	ctx := validateCtx("POST", "", "")
	data := value.New(map[string]any{"password": "secret1", "confirm": "secret1", "email": "a@b.vn"})
	if !ctx.Validate(value.New(registerRule), data).Truthy() {
		t.Error("explicit data should pass")
	}
}

// Fail-closed: a rule that does not compile, or no rule at all, must never let data through.
func TestValidateFailClosed(t *testing.T) {
	ctx := validateCtx("POST", "application/x-www-form-urlencoded", "password=secret1")
	if ctx.Validate(value.New("password >=")).Truthy() {
		t.Error("uncompilable rule must fail closed")
	}
	if ctx.Validate().Truthy() {
		t.Error("missing rule must fail closed")
	}
}
