package work

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sendRequest sends one request to the tenant: method, target (a path with its query), the body
// with its Content-Type ("" for none), and any other header.
func sendRequest(tn *Tenant, method, target, contentType, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://localhost"+target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	return rec
}

// multipartForm writes a multipart/form-data body of the fields, in order (name, value, name,
// value…), and a file part named file when file is not "": its Content-Type and the body.
func multipartForm(t *testing.T, file string, fields ...string) (string, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i := 0; i+1 < len(fields); i += 2 {
		if err := writer.WriteField(fields[i], fields[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if file != "" {
		part, err := writer.CreateFormFile("avatar", "avatar.png")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(file))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType(), body.String()
}

const urlencoded = "application/x-www-form-urlencoded"

// formSite is a Kitwork UI site whose /form reads the body's fields with request.form: email twice
// (the same value), a name no field has, and avatar, a file of a multipart form.
func formSite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"form/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post((context) => context.json([context.request.form(\"email\"), context.request.form(\"missing\"), context.request.form(\"avatar\"), context.request.form(\"email\")]));\n",
		"after/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post(({ request, response }) => response.json([request.formValue(\"email\"), request.form(\"email\")]));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// request.form(name) reads a field of the form the BODY carries, urlencoded or multipart, never the
// URL's query: a query value of the same name is not read. A name the body does not have is "", and
// so is every name of a body that is not a form (JSON, no Content-Type, a broken form) and a file of a
// multipart form. Read twice, the same value.
//
// CONTROL: with Form reading req.FormValue (the query and the body, as formValue does), /form with the
// email only in its query gave "query@example.com" and this test failed; with the multipart case of
// formOfBody taken out, the multipart form had no fields and this test failed.
func TestRequestFormReadsTheBodyOnly(t *testing.T) {
	tn := formSite(t)
	contentType, multipartBody := multipartForm(t, "\x89PNG", "email", "body@example.com", "name", "Quốc")
	_, onlyFile := multipartForm(t, "\x89PNG")
	for _, c := range []struct {
		name, target, contentType, body, want string
	}{
		{"urlencoded", "/form?email=query%40example.com", urlencoded, "email=body%40example.com&name=Qu%E1%BB%91c", `["body@example.com","","","body@example.com"]`},
		{"query only", "/form?email=query%40example.com", urlencoded, "", `["","","",""]`},
		{"query only, another field in the body", "/form?email=query%40example.com", urlencoded, "name=x", `["","","",""]`},
		{"urlencoded with a charset", "/form", urlencoded + "; charset=UTF-8", "email=a%40b.c", `["a@b.c","","","a@b.c"]`},
		{"multipart", "/form?email=query%40example.com", contentType, multipartBody, `["body@example.com","","","body@example.com"]`},
		{"multipart with only a file", "/form?email=query%40example.com", contentType, onlyFile, `["","","",""]`},
		{"json", "/form?email=query%40example.com", "application/json", `{"email":"json@example.com"}`, `["","","",""]`},
		{"no Content-Type", "/form?email=query%40example.com", "", "email=body%40example.com", `["","","",""]`},
		{"broken urlencoded", "/form", urlencoded, "email=body%40example.com&name=%zz", `["","","",""]`},
		{"multipart without its boundary", "/form", "multipart/form-data", multipartBody, `["","","",""]`},
		{"broken multipart", "/form", contentType, multipartBody[:len(multipartBody)/2], `["","","",""]`},
	} {
		rec := sendRequest(tn, http.MethodPost, c.target, c.contentType, c.body, nil)
		if rec.Code != http.StatusOK || rec.Body.String() != c.want {
			t.Errorf("%s: %d %s, want %s", c.name, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// request.form reads its body under the 1 MiB ceiling every body read has: past it, urlencoded or
// multipart, the request answers 413 and the handler goes no further. A form under the ceiling reads.
//
// CONTROL: with readForm reading the body up to MaxBodyBytes instead of DefaultBodyLimit, both forms
// past 1 MiB read their field and answered 200, and this test failed.
func TestRequestFormCeiling(t *testing.T) {
	tn := formSite(t)
	big := strings.Repeat("a", 3<<19) // 1.5 MiB
	if rec := sendRequest(tn, http.MethodPost, "/form", urlencoded, "email=x&pad="+big, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("urlencoded past 1 MiB: %d %.120q, want 413", rec.Code, rec.Body.String())
	}
	contentType, body := multipartForm(t, big, "email", "x")
	if rec := sendRequest(tn, http.MethodPost, "/form", contentType, body, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("multipart past 1 MiB: %d %.120q, want 413", rec.Code, rec.Body.String())
	}
	under := strings.Repeat("a", 1<<19) // 512 KiB
	if rec := sendRequest(tn, http.MethodPost, "/form", urlencoded, "email=x&pad="+under, nil); rec.Code != http.StatusOK || rec.Body.String() != `["x","","","x"]` {
		t.Errorf("urlencoded under 1 MiB: %d %.120q", rec.Code, rec.Body.String())
	}
}

// A handler that read request.formValue first (Go's ParseForm took the body from the request) still
// reads the body's fields with request.form, and still not the query's.
//
// CONTROL: with readForm no longer taking the fields of Go's parse (goReadTheForm always false), the
// body was gone and request.form gave "" after formValue; this test failed.
func TestRequestFormAfterFormValue(t *testing.T) {
	tn := formSite(t)
	if rec := sendRequest(tn, http.MethodPost, "/after?email=q", urlencoded, "email=b", nil); rec.Body.String() != `["b","b"]` {
		t.Errorf("urlencoded: %d %s", rec.Code, rec.Body.String())
	}
	if rec := sendRequest(tn, http.MethodPost, "/after?email=q", urlencoded, "", nil); rec.Body.String() != `["q",""]` {
		t.Errorf("query only: %d %s", rec.Code, rec.Body.String())
	}
	contentType, body := multipartForm(t, "", "email", "m")
	if rec := sendRequest(tn, http.MethodPost, "/after?email=q", contentType, body, nil); rec.Body.String() != `["q","m"]` {
		t.Errorf("multipart: %d %s", rec.Code, rec.Body.String())
	}
}

// request.form keeps its ceiling when request.formValue or request.formParams read the body first (Go's
// parse read it whole, with no ceiling of request.form's): a form past 1 MiB answers 413 all the same,
// urlencoded or multipart, whether the body said its length or came without one; a form under the
// ceiling reads. formValue itself still reads such a body (TestRequestFormValueKeepsItsWay).
//
// CONTROL: with readForm taking the fields of Go's parse whatever their size (its goFormSize check
// removed), every form past 1 MiB read after formValue or formParams answered 200 with its field, and
// this test failed.
func TestRequestFormCeilingAfterFormValue(t *testing.T) {
	tn := formSite(t)
	params := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"params/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.post(({ request, response }) => { const all = request.formParams(); return response.json([request.form(\"email\")]); });\n",
	}), "localhost")
	if err := params.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	big := "email=b&pad=" + strings.Repeat("a", 3<<19) // 1.5 MiB
	multipartType, multipartBig := multipartForm(t, strings.Repeat("a", 3<<19), "email", "m")
	send := func(tenant *Tenant, target, contentType, body string, length bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://localhost"+target, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		if !length {
			req.ContentLength = -1 // sent in chunks: the length is not known before it is read
		}
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}
	for _, c := range []struct {
		name                      string
		tenant                    *Tenant
		target, contentType, body string
		length                    bool
	}{
		{"formValue first", tn, "/after", urlencoded, big, true},
		{"formValue first, no length", tn, "/after", urlencoded, big, false},
		{"formValue first, multipart", tn, "/after", multipartType, multipartBig, true},
		{"formValue first, multipart, no length", tn, "/after", multipartType, multipartBig, false},
		{"formParams first", params, "/params", urlencoded, big, true},
		{"formParams first, no length", params, "/params", urlencoded, big, false},
	} {
		if rec := send(c.tenant, c.target, c.contentType, c.body, c.length); rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s, past 1 MiB: %d %.120q, want 413", c.name, rec.Code, rec.Body.String())
		}
	}
	under := "email=b&pad=" + strings.Repeat("a", 1<<19) // 512 KiB
	if rec := send(tn, "/after", urlencoded, under, false); rec.Code != http.StatusOK || rec.Body.String() != `["b","b"]` {
		t.Errorf("formValue first, under 1 MiB: %d %.120q", rec.Code, rec.Body.String())
	}
	if rec := send(params, "/params", urlencoded, under, true); rec.Code != http.StatusOK || rec.Body.String() != `["b"]` {
		t.Errorf("formParams first, under 1 MiB: %d %.120q", rec.Code, rec.Body.String())
	}
}

// A body sent without its length (in chunks) that Go's own parse could not read to its end, because the
// host's ceiling stopped it (http.MaxBytesHandler in kitwork.go, MaxBodyBytes), urlencoded or multipart,
// or because an urlencoded body went past Go's own 10 MB, leaves request.formValue with no fields:
// request.form after it is past its ceiling all the same, 413, not a form with no fields. A form within
// the host's ceiling still reads.
//
// CONTROL: with readForm no longer asking whether Go's parse stopped short (goStoppedShort always
// false), the three bodies read as a form with no fields and answered 200 ["",""], and this test failed.
func TestRequestFormCeilingAfterGoStoppedShort(t *testing.T) {
	savedMaximum, savedLocal := MaxBodyBytes, AllowLocal
	AllowLocal = true // developing: a failure answers with its text, which the test reads
	t.Cleanup(func() { MaxBodyBytes, AllowLocal = savedMaximum, savedLocal })
	tn := formSite(t)
	send := func(handler http.Handler, contentType, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/after", strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		req.ContentLength = -1 // sent in chunks: the length is not known before it is read
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	MaxBodyBytes = 1000
	host := http.MaxBytesHandler(http.HandlerFunc(tn.Serve), MaxBodyBytes) // as kitwork.go serves a tenant
	multipartType, multipartBody := multipartForm(t, "", "email", "m", "pad", strings.Repeat("a", 2000))
	for _, c := range []struct{ name, contentType, body string }{
		{"urlencoded", urlencoded, "email=b&pad=" + strings.Repeat("a", 2000)},
		{"multipart", multipartType, multipartBody},
	} {
		rec := send(host, c.contentType, c.body)
		if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "vượt trần 1000 byte của host") {
			t.Errorf("%s past the host's 1000 bytes, formValue first: %d %.300q, want 413 naming the host's ceiling", c.name, rec.Code, rec.Body.String())
		}
	}
	if rec := send(host, urlencoded, "email=b"); rec.Code != http.StatusOK || rec.Body.String() != `["b","b"]` {
		t.Errorf("under the host's 1000 bytes, formValue first: %d %.120q", rec.Code, rec.Body.String())
	}

	MaxBodyBytes = 32 << 20
	rec := send(http.HandlerFunc(tn.Serve), urlencoded, "email=b&pad="+strings.Repeat("a", 11<<20)) // 11 MiB, past Go's 10 MB
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "vượt trần 1048576 byte của request.form(…)") {
		t.Errorf("past Go's own 10 MB, formValue first: %d %.300q, want 413", rec.Code, rec.Body.String())
	}
}

// When the host's ceiling (app.web({ maxBody })) is lower than request.form's 1 MiB, a form past it is
// refused at the host's, and the failure names that ceiling; with the host's ceiling above 1 MiB it
// names request.form's.
//
// CONTROL: with Form naming DefaultBodyLimit whatever the host's ceiling (formTooLarge's first case
// removed), a form refused at 1000 bytes was said to pass 1048576, and this test failed.
func TestRequestFormCeilingNamesTheHostsWhenLower(t *testing.T) {
	savedMaximum, savedLocal := MaxBodyBytes, AllowLocal
	AllowLocal = true // developing: a failure answers with its text, which the test reads
	t.Cleanup(func() { MaxBodyBytes, AllowLocal = savedMaximum, savedLocal })
	tn := formSite(t)

	MaxBodyBytes = 1000
	rec := sendRequest(tn, http.MethodPost, "/form", urlencoded, "email=x&pad="+strings.Repeat("a", 2000), nil)
	host := "thân request vượt trần 1000 byte của host (app.web({ maxBody })), thấp hơn trần 1048576 byte của request.form(…)"
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), host) {
		t.Errorf("past the host's 1000 bytes: %d %.300q, want 413 naming the host's ceiling", rec.Code, rec.Body.String())
	}
	if rec := sendRequest(tn, http.MethodPost, "/form", urlencoded, "email=x", nil); rec.Code != http.StatusOK || rec.Body.String() != `["x","","","x"]` {
		t.Errorf("under the host's 1000 bytes: %d %.120q", rec.Code, rec.Body.String())
	}

	MaxBodyBytes = 32 << 20
	rec = sendRequest(tn, http.MethodPost, "/form", urlencoded, "email=x&pad="+strings.Repeat("a", 3<<19), nil)
	if body := rec.Body.String(); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(body, "thân request vượt trần 1048576 byte của request.form(…)") || strings.Contains(body, "của host") {
		t.Errorf("past 1 MiB under a host of 32 MiB: %d %.300q, want 413 naming request.form's ceiling", rec.Code, body)
	}
}

// request.formValue keeps what it always did, in a template site (apptop.vn reads its forms with it
// in 58 files) and in router.work: Go's FormValue, an urlencoded body's field before the query's, the
// query's before a multipart field's, the query of a GET, and a body past 1 MiB still read
// (request.form's ceiling is not formValue's). request.form is there for a template site too, body
// only.
//
// CONTROL: with FormValue reading req.PostFormValue (the body only), "/login?email=q" with no body
// field gave "" and this test failed; with FormValue read through readBody's 1 MiB ceiling, the form
// past 1 MiB answered 413 and this test failed.
func TestRequestFormValueKeepsItsWay(t *testing.T) {
	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"login/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.post((ctx) => ctx.json([ctx.request.formValue(\"email\"), ctx.request.form(\"email\")]));\n" +
			"router.get((ctx) => ctx.json([ctx.request.formValue(\"email\")]));\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	ui := formSite(t)
	contentType, body := multipartForm(t, "", "email", "m")
	big := "email=b&pad=" + strings.Repeat("a", 3<<19)
	for _, c := range []struct {
		name, method, target, contentType, body, want string
	}{
		{"body and query", http.MethodPost, "/login?email=q", urlencoded, "email=b", `["b","b"]`},
		{"query only", http.MethodPost, "/login?email=q", urlencoded, "", `["q",""]`},
		{"multipart: the query first", http.MethodPost, "/login?email=q", contentType, body, `["q","m"]`},
		{"multipart", http.MethodPost, "/login", contentType, body, `["m","m"]`},
		{"get", http.MethodGet, "/login?email=q", "", "", `["q"]`},
	} {
		if rec := sendRequest(template, c.method, c.target, c.contentType, c.body, nil); rec.Code != http.StatusOK || rec.Body.String() != c.want {
			t.Errorf("template, %s: %d %s, want %s", c.name, rec.Code, rec.Body.String(), c.want)
		}
	}
	if rec := sendRequest(ui, http.MethodPost, "/after?email=q", urlencoded, "", nil); rec.Body.String() != `["q",""]` {
		t.Errorf("router.work, query only: %d %s", rec.Code, rec.Body.String())
	}

	// formValue reads a form past 1 MiB, as it did: a template site with formValue alone
	past := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"login/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.post((ctx) => ctx.json([ctx.request.formValue(\"email\")]));\n",
	}), "localhost")
	if err := past.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if rec := sendRequest(past, http.MethodPost, "/login", urlencoded, big, nil); rec.Code != http.StatusOK || rec.Body.String() != `["b"]` {
		t.Errorf("formValue past 1 MiB: %d %.120q, want 200 [\"b\"]", rec.Code, rec.Body.String())
	}
}
