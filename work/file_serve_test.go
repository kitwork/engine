package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// NOT_FOUND and TOO_LARGE reach safe() as themselves, never folded into ERROR (KITWORK_LANG.md mục 6);
// a read past the ceiling of the VM's memory names context.file; a file of the data zone that the site's
// folder has says how to read it now; no message names a path of the machine (mục 12).
//
// CONTROL: with NOT_FOUND taken out of value.closedCodes, safe gave ERROR for the missing file, the hinted
// one, and copy and move of nothing; with codeHint giving "", "the hint toward app/" failed; with codeHint
// looking at the site's file with os.Stat in place of the code zone's Stat, the hint pointed to
// app/localhost/.env, a read that is always DENIED, and told that the site has one: "a hint toward what the
// code zone never reaches".
func TestFileFailuresKeepTheirCodes(t *testing.T) {
	saved := fileReadLimit
	t.Cleanup(func() { fileReadLimit = saved })
	fileReadLimit = 16
	tn, app := fileSite(t, map[string]string{
		"codes": `router.get((context) => context.json({
  missing: reason(() => file.json("settings.json")),
  hinted: reason(() => file.json("data/menu.json")),
  large: reason(() => file.text("app/localhost/large.txt")),
  copyMissing: reason(() => file.open("none/a.png").copy("b.png")),
  moveMissing: reason(() => file.open("none.txt").move("b.txt")),
  env: reason(() => file.text(".env")),
  envLocal: reason(() => file.text(".env.local"))
}));`,
	}, map[string]string{"localhost/data/menu.json": `[]`, "localhost/large.txt": strings.Repeat("x", 40),
		"localhost/.env": "SECRET=1", "localhost/.env.local": "SECRET=2"})
	var got struct{ Missing, Hinted, Large, CopyMissing, MoveMissing, Env, EnvLocal fileFailed }
	body := fileJSON(t, tn, "/codes", &got)
	if got.Env.Code != "NOT_FOUND" || got.Env.Message != `không có file ".env"` ||
		got.EnvLocal.Code != "NOT_FOUND" || got.EnvLocal.Message != `không có file ".env.local"` {
		t.Errorf("a hint toward what the code zone never reaches: %+v, %+v", got.Env, got.EnvLocal)
	}
	if got.Missing.Code != "NOT_FOUND" || got.Missing.Message != `không có file "settings.json"` {
		t.Errorf("missing = %+v", got.Missing)
	}
	if got.Hinted.Code != "NOT_FOUND" || got.Hinted.Message != `không có file "data/menu.json"; file đi kèm code của site đọc qua tiền tố app/: file.json("app/localhost/data/menu.json") (KITWORK_FILE.md mục 1)` {
		t.Errorf("the hint toward app/ = %+v", got.Hinted)
	}
	if got.Large.Code != "TOO_LARGE" || got.Large.Message != `đọc "app/localhost/large.txt" vào bộ nhớ tối đa 16 byte, file dài 40 byte; trả file cho trình duyệt thì dùng context.file("app/localhost/large.txt")` {
		t.Errorf("large = %+v", got.Large)
	}
	if got.CopyMissing.Code != "NOT_FOUND" || got.MoveMissing.Code != "NOT_FOUND" {
		t.Errorf("copy/move of nothing = %+v, %+v", got.CopyMissing, got.MoveMissing)
	}
	if strings.Contains(body, filepath.Dir(app)) || strings.Contains(body, filepath.ToSlash(filepath.Dir(app))) {
		t.Errorf("a message names the machine's path: %s", body)
	}
}

// Every member of a handle is a function: read without a call, delete, clear, write and create do
// nothing; called, they act (mục 2: open() and a handle do no I/O until an action runs).
//
// CONTROL: with fileHandle.OnGet running a change at once for a member read (h.call(nil, key, nil) for
// the words of fileChanges), "reading .delete removed the file, or .clear emptied the folder:
// {"inside":0,"kind":"boolean","there":false}".
func TestFileHandleActsOnlyWhenCalled(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"read": `router.get((context) => {
  const handle = file.open("keep.txt");
  const remove = handle.delete;
  const wipe = file.open("folder/").clear;
  return context.json({ there: handle.exists(), kind: typeof remove, inside: file.list("folder/").length });
});`,
		"call": `router.get((context) => { const remove = file.open("keep.txt").delete; return context.json(remove()); });`,
	}, map[string]string{".database/files/keep.txt": "giữ", ".database/files/folder/a.txt": "a"})
	if body := fileJSON(t, tn, "/read", nil); body != `{"inside":1,"kind":"function","there":true}` {
		t.Fatalf("reading .delete removed the file, or .clear emptied the folder: %s", body)
	}
	if body := fileJSON(t, tn, "/call", nil); body != `true` {
		t.Fatalf("calling it = %s", body)
	}
	if _, err := os.Stat(filepath.Join(app, ".database", "files", "keep.txt")); err == nil {
		t.Error("the call did not delete")
	}
}

// write() takes data that fits the extension and turns nothing into another kind (mục 5): an object into
// .txt, a string into .png, a "data:…" string into an image are INVALID_ARGUMENT; a value JSON cannot
// write is refused where it lies; a string of bytes that are not UTF-8 (a form sent so) is refused for a
// text extension and inside JSON, as text() would refuse to read it back; a value that holds itself, or
// nests past value.MaxDepth, written to .json is INVALID_ARGUMENT in JSON.stringify's words; a change at
// the top level of a file fails as the file loads.
//
// CONTROL: with source() taking any value's text for any extension (the kind switch skipped), the object
// written to .txt, the string and the data URI written to .png, LICENSE, the function and the bytes
// written to .json each answered a FileInfo; with the utf8.ValidString check of the text case removed,
// "bytes that are not UTF-8 written as text" answered a FileInfo for a.txt; with notJSONWalk not putting
// what it walks into on its path (value.Path's Key and Index skipped), the test process died: "fatal
// error: stack overflow" in notJSONWalk, as one tenant's handler took the host down before 23/10/2026.
func TestFileWriteTakesWhatFitsTheExtension(t *testing.T) {
	tn, _ := fileSite(t, map[string]string{
		"write": `router.get((context) => context.json({
  object: reason(() => file.open("a.txt").write({ a: 1 })),
  text: reason(() => file.open("a.png").write("hello")),
  dataURI: reason(() => file.open("a.png").write("data:image/png;base64,iVBORw0KGgo=")),
  function: reason(() => file.open("a.json").write({ run: () => 1 })),
  bytesToJSON: reason(() => file.open("b.json").write(file.bytes("app/localhost/x.bin"))),
  license: reason(() => file.open("LICENSE").write("MIT")),
  folder: reason(() => file.open("dir/").write("x")),
  fine: file.open("ok.txt").write("ok").size
}));`,
	}, map[string]string{"localhost/x.bin": "\x00\x01"})
	var got map[string]json.RawMessage
	fileJSON(t, tn, "/write", &got)
	want := map[string]string{
		"object":  `"a.txt" nhận chuỗi (đuôi chữ), không nhận object; object và mảng ghi vào .json, hay JSON.stringify(…) trước`,
		"text":    `"a.png" nhận bytes hay một upload (đuôi nhị phân hay không đuôi), không nhận chuỗi; chữ ghi vào đuôi chữ (.txt, .md, .csv…), giá trị JSON vào .json`,
		"dataURI": `"a.png" nhận bytes hay một upload (đuôi nhị phân hay không đuôi); một chuỗi "data:…" không được giải mã ngầm, giải mã là việc tường minh của người viết (KITWORK_FILE.md mục 5)`,
		"license": `"LICENSE" nhận bytes hay một upload (đuôi nhị phân hay không đuôi), không nhận chuỗi; chữ ghi vào đuôi chữ (.txt, .md, .csv…), giá trị JSON vào .json`,
	}
	for name, message := range want {
		var failed fileFailed
		if err := json.Unmarshal(got[name], &failed); err != nil || failed.Code != "INVALID_ARGUMENT" || failed.Message != message {
			t.Errorf("%s = %s, want INVALID_ARGUMENT %q", name, got[name], message)
		}
	}
	for name, code := range map[string]string{"function": "INVALID_ARGUMENT", "bytesToJSON": "INVALID_ARGUMENT", "folder": "CONFLICT"} {
		var failed fileFailed
		if err := json.Unmarshal(got[name], &failed); err != nil || failed.Code != code {
			t.Errorf("%s = %s, want %s", name, got[name], code)
		}
	}
	if string(got["fine"]) != "2" {
		t.Errorf("ok.txt = %s", got["fine"])
	}

	// a value that holds itself, or one nested past value.MaxDepth, is refused as JSON.stringify refuses
	// it, never walked down until Go's stack ends the process and every app of the host with it
	held, _ := fileSite(t, map[string]string{
		"held": `router.get((context) => {
  const order = { id: 1 };
  order.self = order;
  const list = [1];
  list.push(list);
  let deep = 1;
  for (let i = 0; i < 10001; i++) {
    deep = [deep];
  }
  return context.json({ object: reason(() => file.open("c.json").write(order)), array: reason(() => file.open("d.json").write({ items: list })),
    deep: reason(() => file.open("e.json").write(deep)), written: file.exists("c.json") || file.exists("d.json") || file.exists("e.json") });
});`,
	}, nil)
	var cyclic struct {
		Object, Array, Deep fileFailed
		Written             bool
	}
	fileJSON(t, held, "/held", &cyclic)
	if cyclic.Object.Code != "INVALID_ARGUMENT" || cyclic.Object.Message != `"c.json" nhận một giá trị viết được thành JSON; object tự tham chiếu (vòng ở self, quay về gốc)` ||
		cyclic.Array.Code != "INVALID_ARGUMENT" || cyclic.Array.Message != `"d.json" nhận một giá trị viết được thành JSON; mảng tự tham chiếu (vòng ở items[1], quay về items)` ||
		cyclic.Deep.Code != "INVALID_ARGUMENT" || !strings.HasPrefix(cyclic.Deep.Message, `"e.json" nhận một giá trị viết được thành JSON; giá trị lồng quá sâu (hơn 10000 tầng object và mảng`) ||
		cyclic.Written {
		t.Errorf("a value that holds itself, or nests too deep, written to .json = %+v", cyclic)
	}

	// a string of bytes from outside that are not UTF-8 is not text: text() could not read it back
	form, _ := fileSite(t, map[string]string{
		"form": `router.post((context) => context.json({ txt: reason(() => file.open("a.txt").write(context.request.form("note"))),
  json: reason(() => file.open("a.json").write({ note: context.request.form("note") })) }));`,
	}, nil)
	raw := sendRequest(form, http.MethodPost, "/form", urlencoded, "note=%FF%FEabc", nil)
	var invalid struct{ Txt, JSON fileFailed }
	if err := json.Unmarshal(raw.Body.Bytes(), &invalid); err != nil || invalid.Txt.Code != "INVALID_ARGUMENT" ||
		invalid.Txt.Message != `"a.txt" nhận chữ UTF-8; chuỗi này có byte không phải UTF-8 (dữ liệu từ ngoài kiểm trước khi ghi, hay ghi bytes vào đuôi nhị phân)` ||
		invalid.JSON.Code != "INVALID_ARGUMENT" || !strings.Contains(invalid.JSON.Message, "giá trị ở note là chuỗi có byte không phải UTF-8") {
		t.Errorf("bytes that are not UTF-8 written as text = %d %s", raw.Code, raw.Body.String())
	}

	_, err := bootUISite(t, map[string]string{
		"localhost/router.work": fileHeader + "file.open(\"boot.txt\").write(\"x\");\n",
	}, true)
	if err == nil || !strings.Contains(err.Error(), `file.open("boot.txt").write ở cấp file: lúc nạp, file chỉ khai`) {
		t.Errorf("a change at the top level = %v", err)
	}
}

// The spellings the spec replaced stop the boot where they are written, with the new spelling (mục 14,
// mục 17): file.write, file.save, file.base64, file.delete and file.directory, a word imported by name;
// one reached through another name fails when it runs, in the same words; file.env is the manifest's.
//
// CONTROL: with the resolver's fileMember hooks removed (compiler/scope.go), the sites with file.write,
// file.save, file.base64, file.delete and file.directory booted ("boot = <nil>").
func TestFileOldSpellingsNameTheNew(t *testing.T) {
	for source, want := range map[string]string{
		"router.get((context) => context.json(file.write(\"a.txt\", \"x\")));": `router.work:3:43: file.write(path, data) đã bỏ: đổi thì open() trước, file.open(path).write(data) (KITWORK_FILE.md mục 3, 17)`,
		"router.get((context) => context.json(file.save(\"a.txt\", \"x\")));":  `file.save(path, data) đã bỏ: file.open(path).write(data)`,
		"router.get((context) => context.json(file.base64(\"a.png\")));":       `file.base64(path) đã bỏ: ảnh trả cho trình duyệt bằng context.file(path)`,
		"router.get((context) => context.json(file.delete(\"a.txt\")));":       `file.delete(path) không có: đổi thì open() trước, file.open(path).delete()`,
		"router.get((context) => context.json(file.directory(\"a/\")));":       `file.directory() không có: thư mục là đường dẫn có "/" cuối`,
		"import { json } from \"kitwork/file\";":                               `kitwork/file import một cách: import * as file from "kitwork/file"`,
		"router.get((context) => { const { write } = file; return write; });":  `file.write(path, data) đã bỏ`,
	} {
		_, err := bootUISite(t, map[string]string{"localhost/router.work": fileHeader + source + "\n"}, true)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s\nboot = %v\nwant %q", source, err, want)
		}
	}

	tn, _ := fileSite(t, map[string]string{
		"alias": `router.get((context) => {
  const other = { file: file }.file;
  return context.json({ write: reason(() => other.write("a.txt", "x")), env: reason(() => other.env("./.env")),
    remove: reason(() => other.open("tmp/").remove()), name: reason(() => other.open("uploads/").name("a.png")) });
});`,
	}, nil)
	var got struct{ Write, Env, Remove, Name fileFailed }
	fileJSON(t, tn, "/alias", &got)
	if got.Write.Code != "INVALID_ARGUMENT" || got.Write.Message != `file.write(path, data) đã bỏ: đổi thì open() trước, file.open(path).write(data) (KITWORK_FILE.md mục 3, 17)` {
		t.Errorf("write through another name = %+v", got.Write)
	}
	if got.Env.Code != "INVALID_ARGUMENT" || !strings.HasPrefix(got.Env.Message, "file.env chỉ có trong application.work (manifest); code trong app đọc biến môi trường bằng environment.KEY") {
		t.Errorf("file.env in an app = %+v", got.Env)
	}
	if !strings.Contains(got.Remove.Message, `clear() rồi file.open("tmp/").delete()`) || !strings.Contains(got.Name.Message, "chọn con bằng segment(…)") {
		t.Errorf("remove = %+v, name(child) = %+v", got.Remove, got.Name)
	}
}

// context.file(path) and context.file(handle) answer from the disk (mục 9, mục 13.14), alike: the path is
// written as the module's reads write it (23/10/2026), the handle is file.open's. The media type by the
// extension unless .type() came first, an ETag, Range; app/… works for a file that comes with the code, and
// what the zone never reaches (.env), a file that is not there, a folder, answer 404 alike. A file of no
// extension, or of one no type is known for, is bytes, application/octet-stream, and every file answer says
// nosniff: an upload that looks like HTML is never served as a page of the site. Bytes the handler holds
// are sent as they are, in .type()'s media type else application/octet-stream; a path the module refuses
// fails in its words, INVALID_ARGUMENT.
//
// CONTROL: with Zone.Open not asking denied() (the code zone's deny removed for serving), app/.env and
// app/.ENV answered 200 "SECRET=1", and app/.database/… its data; with serveFile setting no type when
// the extension gives none (as before 23/10/2026), uploads/7/original answered "text/html; charset=utf-8"
// sniffed from its content, and no answer said nosniff. CONTROL (23/10/2026, go test -overlay): with
// Response.File refusing a string as on 22/10, "path: context.file = 500 … không nhận chuỗi đường dẫn"; with
// .file() taking the answer before it read what it was given, every refused path then answered "call json:
// `.json()` sau `.file()`", not INVALID_ARGUMENT.
func TestFileContextFileServesFromTheDisk(t *testing.T) {
	tn, _ := fileSite(t, map[string]string{
		"get":    `router.get((context) => context.file(file.open(context.query("p"))));`,
		"path":   `router.get((context) => context.file(context.query("p")));`,
		"typed":  `router.get((context) => context.type("text/plain; charset=utf-8").file("exports/a.csv"));`,
		"bytes":  `router.get((context) => context.file(file.bytes("exports/a.csv")));`,
		"csv":    `router.get((context) => context.type("text/csv").file(file.bytes("exports/a.csv")));`,
		"reason": `router.get((context) => context.json(safe(() => context.file(context.query("p"))).error));`,
	}, map[string]string{
		".database/files/exports/a.csv": "id,name\n1,Kitwork\n", ".env": "SECRET=1", "localhost/logo.svg": "<svg/>",
		".database/files/uploads/7/original":             "<html><script>alert(1)</script></html>",
		".database/files/uploads/7/page.kitwork-unknown": "<!doctype html><title>x</title>",
	})
	for _, route := range []string{"get", "path"} {
		get := func(target string, header map[string]string) *httptest.ResponseRecorder {
			return serveGet(tn, "/"+route+strings.TrimPrefix(target, "/get"), header)
		}
		fileServesFromTheDisk(t, route, get)
	}
	get := func(target string, header map[string]string) *httptest.ResponseRecorder {
		return serveGet(tn, target, header)
	}
	if typed := get("/typed", nil); typed.Header().Get("Content-Type") != "text/plain; charset=utf-8" || typed.Body.String() != "id,name\n1,Kitwork\n" {
		t.Errorf(".type() before .file(path) = %v %q", typed.Header(), typed.Body.String())
	}
	if held := get("/bytes", nil); held.Code != http.StatusOK || held.Body.String() != "id,name\n1,Kitwork\n" ||
		held.Header().Get("Content-Type") != "application/octet-stream" || held.Header().Get("ETag") != "" {
		t.Errorf("context.file(bytes) = %d %v %q, want the bytes as they are, application/octet-stream", held.Code, held.Header(), held.Body.String())
	}
	if csv := get("/csv", nil); csv.Header().Get("Content-Type") != "text/csv" || csv.Body.String() != "id,name\n1,Kitwork\n" {
		t.Errorf(".type() before .file(bytes) = %v %q", csv.Header(), csv.Body.String())
	}
	for _, p := range []string{"../a.csv", "/a.csv", "App/a.csv", "exports/../a.csv"} {
		rec := get("/reason?p="+url.QueryEscape(p), nil)
		if !strings.Contains(rec.Body.String(), `"code":"INVALID_ARGUMENT"`) || strings.Contains(rec.Body.String(), "không nhận chuỗi") {
			t.Errorf("context.file(%q) failed with %d %.300q, want INVALID_ARGUMENT in the words of kitwork/file", p, rec.Code, rec.Body.String())
		}
	}
}

// A template site's router.kitwork.js gives .file() a path too (23/10/2026), read as kitwork/file reads it:
// the 22/10 refusal of a string held at both kinds of site, and its reversal does as well. A file out of
// reach is 404 there as in router.work.
//
// CONTROL (23/10/2026, go test -overlay): with Response.File refusing a string as on 22/10, "?p=exports/a.csv
// = 500 \"router.kitwork.js:2:24: call file: `.file()` nhận một handle … không nhận chuỗi đường dẫn\"", and app/.env
// answered that 500 too, not 404.
func TestFileTemplateSiteFileTakesAPath(t *testing.T) {
	tenant := buildUISite(t, map[string]string{
		"localhost/router.kitwork.js":   "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.file(ctx.query(\"p\")));\n",
		".database/files/exports/a.csv": "id,name\n1,Kitwork\n",
		"localhost/logo.svg":            "<svg/>",
	})
	for p, want := range map[string]string{"exports/a.csv": "id,name\n1,Kitwork\n", "app/localhost/logo.svg": "<svg/>"} {
		if rec := serveUI(t, tenant, "http://localhost/?p="+url.QueryEscape(p)); rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("?p=%s = %d %.300q, want 200 %q", p, rec.Code, rec.Body.String(), want)
		}
	}
	if rec := serveUI(t, tenant, "http://localhost/?p=app/.env"); rec.Code != http.StatusNotFound {
		t.Errorf("?p=app/.env = %d, want 404", rec.Code)
	}
}

// fileServesFromTheDisk is what context.file of a path, and of a handle, answers for the same files.
func fileServesFromTheDisk(t *testing.T, route string, get func(target string, header map[string]string) *httptest.ResponseRecorder) {
	t.Helper()
	whole := get("/get?p=exports/a.csv", nil)
	if whole.Code != http.StatusOK || whole.Body.String() != "id,name\n1,Kitwork\n" || !strings.HasPrefix(whole.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("%s: context.file = %d %q %v", route, whole.Code, whole.Body.String(), whole.Header())
	}
	etag := whole.Header().Get("ETag")
	if etag == "" || whole.Header().Get("Last-Modified") == "" {
		t.Errorf("no ETag or Last-Modified: %v", whole.Header())
	}
	if part := get("/get?p=exports/a.csv", map[string]string{"Range": "bytes=0-1"}); part.Code != http.StatusPartialContent || part.Body.String() != "id" {
		t.Errorf("Range = %d %q", part.Code, part.Body.String())
	}
	if same := get("/get?p=exports/a.csv", map[string]string{"If-None-Match": etag}); same.Code != http.StatusNotModified {
		t.Errorf("If-None-Match = %d", same.Code)
	}
	if code := get("/get?p=app/localhost/logo.svg", nil); code.Code != http.StatusOK || code.Body.String() != "<svg/>" {
		t.Errorf("a file of the code = %d %q", code.Code, code.Body.String())
	}
	for _, p := range []string{"app/.env", "app/.ENV", "none.csv", "exports/", "app/.database/files/exports/a.csv"} {
		if rec := get("/get?p="+p, nil); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "SECRET") {
			t.Errorf("%s: context.file(%q) = %d %.200q, want 404", route, p, rec.Code, rec.Body.String())
		}
	}
	// an upload that looks like HTML, kept without an extension or with one no type is known for, is bytes
	// (mục 4): never a page of the site guessed from its content
	for _, p := range []string{"uploads/7/original", "uploads/7/page.kitwork-unknown"} {
		if rec := get("/get?p="+p, nil); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("context.file(%q) = %d %v, want application/octet-stream, nosniff", p, rec.Code, rec.Header())
		}
	}
	if whole.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("%s: a file answer without nosniff: %v", route, whole.Header())
	}
}

// write(upload) copies an upload to the disk without its bytes in the VM's memory, nor the whole body in
// the host's (mục 9, mục 13.13): an upload larger than the read ceiling streams through, the memory the
// process allocates stays a small part of its size, and the file is byte for byte the upload. A field of
// the same body reads with request.form after request.file, its 1 MiB counting only the fields; the
// upload's name, size and type are what the client said (the name its last name only, "../../me.png"
// reads "me.png"); request.saveFile names the new spelling. Under the race detector the memory is not
// measured: its own allocations are not the upload's (bench_handler_test.go skips its budgets alike), and
// the rest of the test runs.
//
// CONTROL: with source() reading the upload into memory (io.ReadAll of the opened upload, then a
// bytes.Reader), "the upload passed through memory: 54818544 bytes allocated for an upload of 25165824";
// with readForm not taking the fields of the upload's parse, request.form("title") answered 413
// ("thân request vượt trần 1048576 byte của request.form(…)").
func TestFileUploadStreamsToTheDisk(t *testing.T) {
	savedLimit, savedMaximum := fileReadLimit, MaxBodyBytes
	t.Cleanup(func() { fileReadLimit, MaxBodyBytes = savedLimit, savedMaximum })
	fileReadLimit = 1 << 20
	MaxBodyBytes = 64 << 20
	tn, app := fileSite(t, map[string]string{
		"upload": `router.post(({ request, response }) => {
  const avatar = request.file("avatar");
  const saved = file.open("uploads/avatars/").segment("7", "original.bin").write(avatar);
  return response.status(201).json({ size: saved.size, name: avatar.name, type: avatar.type, uploaded: avatar.size,
    title: request.form("title"), none: request.file("none"), all: request.files("avatar").length });
});`,
		"legacy": `router.post(({ request, response }) => response.json(reason(() => request.saveFile("avatar", "uploads/"))));`,
	}, nil)
	const size = 24 << 20
	pattern := []byte("0123456789abcdef")
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		form.WriteField("title", "ảnh đại diện")
		part, _ := form.CreatePart(map[string][]string{
			"Content-Disposition": {`form-data; name="avatar"; filename="../../me.png"`},
			"Content-Type":        {"image/png"},
		})
		for written := 0; written < size; written += len(pattern) {
			part.Write(pattern)
		}
		form.Close()
		writer.Close()
	}()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/upload", reader)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.ContentLength = -1
	recorder := httptest.NewRecorder()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tn.Serve(recorder, request)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc

	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload = %d %.600s", recorder.Code, recorder.Body.String())
	}
	want := fmt.Sprintf(`{"all":1,"name":"me.png","none":null,"size":%d,"title":"ảnh đại diện","type":"image/png","uploaded":%d}`, size, size)
	if recorder.Body.String() != want {
		t.Errorf("upload = %s\nwant %s", recorder.Body.String(), want)
	}
	if allocated > size/4 && !raceEnabled { // the race detector's own allocations are not the upload's
		t.Errorf("the upload passed through memory: %d bytes allocated for an upload of %d", allocated, size)
	}
	saved, err := os.ReadFile(filepath.Join(app, ".database", "files", "uploads", "avatars", "7", "original.bin"))
	if err != nil || len(saved) != size || !bytes.Equal(saved[:32], append(append([]byte{}, pattern...), pattern...)) {
		t.Errorf("the file on the disk is not the upload: %d bytes, %v", len(saved), err)
	}

	contentType, body := multipartForm(t, "\x89PNG")
	legacy := sendRequest(tn, http.MethodPost, "/legacy", contentType, body, nil)
	var failed fileFailed
	if err := json.Unmarshal(legacy.Body.Bytes(), &failed); err != nil || failed.Code != "INVALID_ARGUMENT" ||
		!strings.HasPrefix(failed.Message, `request.saveFile(tên, đích) đã bỏ: lưu một upload bằng file.open("uploads/").segment(id, "avatar.png").write(request.file("avatar"))`) {
		t.Errorf("request.saveFile = %d %s", legacy.Code, legacy.Body.String())
	}
}

// What goes wrong with an upload is told, never taken for "no file was sent" (mục 9): a body past the
// host's ceiling, and one of more parts than the parse reads, are INVALID_INPUT, 413 when the handler does
// not catch them; a temp folder the parse cannot write to is DATABASE, naming no path of the machine. And
// the rest of an upload's words: request.files of a field with no file is [], a word an upload has not
// names the ones it has, an upload written to an extension of text is INVALID_ARGUMENT, and the files the
// parse put in the system's temp folder are gone when the request ends.
//
// CONTROL: with uploadsOf answering null for every failure but the host's ceiling (as before 23/10/2026),
// "a broken temp folder = null" and "too many parts = null"; with the request scope's cleanup of the parse
// not added, "the request left 1 file(s) of its parse in the temp folder".
func TestFileUploadFailuresAreTold(t *testing.T) {
	savedMaximum := MaxBodyBytes
	t.Cleanup(func() { MaxBodyBytes = savedMaximum })
	MaxBodyBytes = 4 << 20
	tn, app := fileSite(t, map[string]string{
		"upload": `router.post(({ request, response }) => {
  const avatar = request.file("avatar");
  return response.json({ missing: request.files("none"), word: reason(() => avatar.path),
    asText: reason(() => file.open("a.txt").write(avatar)), saved: file.open("a.bin").write(avatar).size });
});`,
		"caught":   `router.post(({ request, response }) => response.json(reason(() => request.file("avatar"))));`,
		"uncaught": `router.post(({ request }) => request.file("avatar").name);`,
	}, nil)
	variable := "TMPDIR"
	if runtime.GOOS == "windows" {
		variable = "TMP"
	}
	temporary := t.TempDir()
	t.Setenv(variable, temporary)
	parse := func() []string {
		names, _ := filepath.Glob(filepath.Join(temporary, "multipart-*"))
		return names
	}

	// an upload past the parse's memory: its bytes go to a file of the system, gone after the request
	upload := strings.Repeat("x", 1<<20)
	contentType, body := multipartForm(t, upload)
	rec := sendRequest(tn, http.MethodPost, "/upload", contentType, body, nil)
	var got struct {
		Missing []any      `json:"missing"`
		Word    fileFailed `json:"word"`
		AsText  fileFailed `json:"asText"`
		Saved   int        `json:"saved"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("upload = %d %.600s", rec.Code, rec.Body.String())
	}
	if got.Missing == nil || len(got.Missing) != 0 || got.Saved != len(upload) {
		t.Errorf("request.files of nothing = %v, saved %d", got.Missing, got.Saved)
	}
	if got.Word.Code != "INVALID_ARGUMENT" || !strings.HasPrefix(got.Word.Message, "upload.path không có: upload có name, size, type") {
		t.Errorf("upload.path = %+v", got.Word)
	}
	if got.AsText.Code != "INVALID_ARGUMENT" || got.AsText.Message != `"a.txt" nhận chuỗi (đuôi chữ), không nhận một upload` {
		t.Errorf("an upload written to .txt = %+v", got.AsText)
	}
	if left := parse(); len(left) != 0 {
		t.Errorf("the request left %d file(s) of its parse in the temp folder: %v", len(left), left)
	}

	// past the host's ceiling
	contentType, body = multipartForm(t, strings.Repeat("x", 5<<20))
	var over fileFailed
	if rec := sendRequest(tn, http.MethodPost, "/caught", contentType, body, nil); json.Unmarshal(rec.Body.Bytes(), &over) != nil ||
		over.Code != "INVALID_INPUT" || over.Message != fmt.Sprintf("thân request vượt trần %d byte của host (app.web({ maxBody })); upload không lớn hơn trần đó", MaxBodyBytes) {
		t.Errorf("an upload past the host's ceiling = %d %s", rec.Code, rec.Body.String())
	}
	if rec := sendRequest(tn, http.MethodPost, "/uncaught", contentType, body, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an upload past the host's ceiling, uncaught = %d %.300s", rec.Code, rec.Body.String())
	}

	// more parts than the parse reads (Go's 1000)
	fields := make([]string, 0, 2*1100)
	for index := 0; index < 1100; index++ {
		fields = append(fields, "f", "x")
	}
	contentType, body = multipartForm(t, "\x89PNG", fields...)
	var parts fileFailed
	if rec := sendRequest(tn, http.MethodPost, "/caught", contentType, body, nil); json.Unmarshal(rec.Body.Bytes(), &parts) != nil ||
		parts.Code != "INVALID_INPUT" || !strings.HasPrefix(parts.Message, "thân multipart vượt giới hạn của bộ đọc: quá nhiều phần") {
		t.Errorf("too many parts = %d %s", rec.Code, rec.Body.String())
	}
	if rec := sendRequest(tn, http.MethodPost, "/uncaught", contentType, body, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too many parts, uncaught = %d %.300s", rec.Code, rec.Body.String())
	}

	// a temp folder the parse cannot write to: the host's failure, not "no file"
	t.Setenv(variable, filepath.Join(temporary, "gone"))
	contentType, body = multipartForm(t, upload)
	var broken fileFailed
	rec = sendRequest(tn, http.MethodPost, "/caught", contentType, body, nil)
	if json.Unmarshal(rec.Body.Bytes(), &broken) != nil || broken.Code != "DATABASE" ||
		!strings.HasPrefix(broken.Message, "không lưu được upload ra file tạm của hệ thống: ") || strings.Contains(broken.Message, temporary) ||
		strings.Contains(broken.Message, filepath.Dir(app)) {
		t.Errorf("a broken temp folder = %d %s", rec.Code, rec.Body.String())
	}
}

// A file of the data zone (what the app wrote, an upload under the name its sender chose) served as a type a browser
// runs as a page (HTML, XHTML, SVG, XML) comes with Content-Security-Policy: sandbox: shown, its scripts not run, its
// origin not the site's, so an upload "a.html" or "a.svg" cannot read the viewer's cookies or act as them (security
// review of 04/11/2026, B). An image and a file of the code zone (the author's own) are served as they were.
//
// CONTROL (09/11/2026): with no sandbox, "context.file(\"uploads/a.html\") as text/html; charset=utf-8:
// Content-Security-Policy \"\", want sandbox".
func TestFileServedFromTheDataZoneRunsNoScript(t *testing.T) {
	tn, _ := fileSite(t, map[string]string{
		"get":   `router.get((context) => context.file(file.open(context.query("p"))));`,
		"typed": `router.get((context) => context.type("text/html").file("uploads/a.txt"));`,
	}, map[string]string{
		".database/files/uploads/a.html": `<script>document.cookie</script>`,
		".database/files/uploads/a.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		".database/files/uploads/a.png":  "\x89PNG",
		".database/files/uploads/a.txt":  "chữ",
		"localhost/page.html":            `<p>của tác giả</p>`,
	})
	for target, sandboxed := range map[string]bool{
		"/get?p=uploads/a.html": true, "/get?p=uploads/a.svg": true, "/typed": true,
		"/get?p=uploads/a.png": false, "/get?p=uploads/a.txt": false, "/get?p=app/localhost/page.html": false,
	} {
		rec := serveGet(tn, target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %.200q", target, rec.Code, rec.Body.String())
		}
		if got, want := rec.Header().Get("Content-Security-Policy"), map[bool]string{true: "sandbox"}[sandboxed]; got != want {
			t.Errorf("%s as %s: Content-Security-Policy %q, want %q", target, rec.Header().Get("Content-Type"), got, want)
		}
	}
}
