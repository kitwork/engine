package work

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// kitwork/file (KITWORK_FILE.md, 22/10/2026) driven through real sites: router.work compiled and served,
// the app acme in a temp folder, its site localhost at acme/localhost, its data zone at
// acme/.database/files/, apart from the site.

// fileHeader leads every route of these sites: the route's own source starts at line 3.
const fileHeader = "import * as router from \"kitwork/router\";\nimport * as file from \"kitwork/file\";\n"

// fileReason is a helper every route has: what safe gave, the value when it worked, else the failure's
// code and message (and progress when it carries one).
const fileReason = "const reason = (fn) => { const r = safe(fn); return r.ok ? r.value : { code: r.error.code, message: r.error.message, progress: r.error.progress ?? null }; };\n"

// fileSite serves routes (each a folder's router.work of the site, under fileHeader and fileReason) and
// other files, each by its path under the app's folder (acme/); it gives the tenant and the app's folder.
func fileSite(t *testing.T, routes, others map[string]string) (*Tenant, string) {
	t.Helper()
	saved := AllowLocal
	AllowLocal = true // developing: an uncaught failure answers its text, which a test reads
	t.Cleanup(func() { AllowLocal = saved })
	root := t.TempDir()
	app := filepath.Join(root, "acme")
	write := func(name, body string) {
		path := filepath.Join(app, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("localhost/router.work", `import * as router from "kitwork/router";`)
	for folder, source := range routes {
		write(filepath.ToSlash(filepath.Join("localhost", folder, "router.work")), fileHeader+fileReason+source)
	}
	for name, body := range others {
		write(name, body)
	}
	tn := NewTenant(root, "localhost")
	tn.MaxEnergy = 50_000_000
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(tn.Close)
	return tn, app
}

// fileJSON serves GET path and reads its JSON answer into into.
func fileJSON(t *testing.T, tn *Tenant, path string, into any) string {
	t.Helper()
	rec := serveGet(tn, path, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %.1500s", path, rec.Code, rec.Body.String())
	}
	if into != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("GET %s: not JSON (%v): %.600s", path, err, rec.Body.String())
		}
	}
	return rec.Body.String()
}

// fileFailed is a failure as fileReason gives it.
type fileFailed struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Progress any    `json:"progress"`
}

// The example at the top of the spec runs end to end in a handler: read directly, change through open(),
// a folder of the data zone copied from and cleared, a JSON file of the code read through app/. The data
// lands in the app's .database/files/, never in the site's folder, and write() gives the FileInfo of what
// is on the disk (mục 13.4).
//
// CONTROL: with fileZone giving the site's folder for the data zone (t.resolve() instead of
// .database/files), "settings.json is not under the data zone" and "the data zone wrote into the site's
// folder" failed.
func TestFileTopExampleRunsInTheDataZone(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"example": `router.get((context) => {
  file.open("settings.json").write({ theme: "dark" });
  file.open("uploads/photos/a.png").write(file.bytes("app/localhost/logo.png"));
  const settings = file.json("settings.json");
  const photos = file.list("uploads/photos/");
  const present = file.exists("uploads/photos/a.png");
  const saved = file.open("notes.txt").write("Xin chào");
  const copied = file.open("uploads/photos/").segment("a.png").copy("backup/a.png");
  file.open("tmp/x/y.txt").write("t");
  const cleared = file.open("tmp/").clear();
  const menu = file.json("app/localhost/data/menu.json");
  const nested = file.open("x/y/z.json").write({ a: 1, list: [1, 2] });
  return context.json({ settings, photos, present, saved, copied: copied.path, cleared, menu, nested,
    tmp: file.list("tmp/").length });
});`,
	}, map[string]string{
		"localhost/logo.png":       "\x89PNG-logo",
		"localhost/data/menu.json": `[{"name":"Cà phê sữa","price":25000}]`,
	})
	var got struct {
		Settings map[string]string `json:"settings"`
		Photos   []map[string]any  `json:"photos"`
		Present  bool              `json:"present"`
		Saved    map[string]any    `json:"saved"`
		Copied   string            `json:"copied"`
		Cleared  int               `json:"cleared"`
		Menu     []map[string]any  `json:"menu"`
		Nested   map[string]any    `json:"nested"`
		Tmp      int               `json:"tmp"`
	}
	body := fileJSON(t, tn, "/example", &got)
	if got.Settings["theme"] != "dark" || !got.Present || got.Copied != "backup/a.png" || got.Cleared != 2 || got.Tmp != 0 {
		t.Fatalf("the example = %s", body)
	}
	if len(got.Menu) != 1 || got.Menu[0]["name"] != "Cà phê sữa" {
		t.Errorf("menu of the code zone = %v", got.Menu)
	}
	if len(got.Photos) != 1 || got.Photos[0]["path"] != "uploads/photos/a.png" || got.Photos[0]["type"] != "file" ||
		got.Photos[0]["extension"] != ".png" || got.Photos[0]["size"] != float64(len("\x89PNG-logo")) {
		t.Errorf("list = %v", got.Photos)
	}
	if got.Saved["name"] != "notes.txt" || got.Saved["path"] != "notes.txt" || got.Saved["size"] != float64(len("Xin chào")) ||
		got.Saved["modifiedAt"] == nil {
		t.Errorf("write() = %v", got.Saved)
	}
	data := filepath.Join(app, ".database", "files")
	for name, want := range map[string]string{
		"settings.json": `{"theme":"dark"}`, "notes.txt": "Xin chào", "backup/a.png": "\x89PNG-logo",
		"uploads/photos/a.png": "\x89PNG-logo",
	} {
		content, err := os.ReadFile(filepath.Join(data, filepath.FromSlash(name)))
		if err != nil || string(content) != want {
			t.Errorf("%s is not under the data zone as written: %q %v", name, content, err)
		}
	}
	nested, err := os.ReadFile(filepath.Join(data, "x", "y", "z.json"))
	if err != nil || got.Nested["size"] != float64(len(nested)) || got.Nested["path"] != "x/y/z.json" {
		t.Errorf("x/y/z.json: FileInfo %v, %d bytes on the disk (%v)", got.Nested, len(nested), err)
	}
	for _, name := range []string{"settings.json", "notes.txt", "backup", "x"} {
		if _, err := os.Stat(filepath.Join(app, "localhost", name)); err == nil {
			t.Errorf("the data zone wrote into the site's folder: %s", name)
		}
	}
}
