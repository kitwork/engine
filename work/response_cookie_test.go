package work

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestResponseSetCookie(t *testing.T) {
	response := &Response{}
	response.SetCookie("studio", value.New("signed-token"), value.New(map[string]any{
		"path":     "/studio",
		"secure":   true,
		"sameSite": "strict",
		"maxAge":   3600,
	}))

	recorder := httptest.NewRecorder()
	response.writeCookies(recorder)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "studio" || cookie.Value != "signed-token" {
		t.Fatalf("unexpected cookie: %#v", cookie)
	}
	if cookie.Path != "/studio" || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("cookie security options not preserved: %#v", cookie)
	}
	if cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 3600 {
		t.Fatalf("cookie policy not preserved: %#v", cookie)
	}
}

func TestResponseDeleteCookie(t *testing.T) {
	response := &Response{}
	response.DeleteCookie("studio", value.New(map[string]any{"path": "/studio"}))

	recorder := httptest.NewRecorder()
	response.writeCookies(recorder)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if cookies[0].Value != "" || cookies[0].MaxAge != -1 || cookies[0].Path != "/studio" {
		t.Fatalf("delete cookie is not expired correctly: %#v", cookies[0])
	}
}

func TestContextCookieShorthand(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://example.com/studio", nil)
	request.AddCookie(&http.Cookie{Name: "existing", Value: "read-me"})
	router := &Router{request: request, response: &Response{}}
	ctx := &Context{request: &Request{router: router}}

	if got := ctx.Cookie("existing").String(); got != "read-me" {
		t.Fatalf("ctx.cookie(name) = %q, want read-me", got)
	}

	setValue := ctx.Cookie("studio", value.New("signed-token"), value.New(map[string]any{
		"path":     "/studio",
		"sameSite": "strict",
	}))
	if setValue.String() != "signed-token" {
		t.Fatalf("ctx.cookie(name, value) returned %q", setValue.String())
	}
	if len(router.response.cookies) != 1 || router.response.cookies[0].Value != "signed-token" {
		t.Fatalf("ctx.cookie did not queue the set cookie: %#v", router.response.cookies)
	}

	deleted := ctx.Cookie("studio", value.NewNil(), value.New(map[string]any{"path": "/studio"}))
	if deleted.K != value.Nil {
		t.Fatalf("ctx.cookie(name, null) returned %s, want nil", deleted.K.String())
	}
	if len(router.response.cookies) != 2 || router.response.cookies[1].MaxAge != -1 {
		t.Fatalf("ctx.cookie(name, null) did not queue deletion: %#v", router.response.cookies)
	}
}
