package work

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// open() touches nothing, a path that is not there included, and neither do segment(), name(), path()
// (mục 2, mục 13.1): after them the app has no .database/ at all.
//
// CONTROL: with open() of the module creating what it names (handle.zone().Create(path) before giving
// the handle), "open() made the app's data zone" failed.
func TestFileOpenDoesNoIO(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"open": `router.get((context) => {
  const folder = file.open("x/y/");
  const child = folder.segment("z", 42, "w.txt");
  return context.json({ path: child.path(), name: child.name(), folder: folder.path(), named: folder.name() });
});`,
	}, nil)
	body := fileJSON(t, tn, "/open", nil)
	if body != `{"folder":"x/y/","name":"w.txt","named":"y","path":"x/y/z/42/w.txt"}` {
		t.Fatalf("/open = %s", body)
	}
	if _, err := os.Stat(filepath.Join(app, ".database")); err == nil {
		t.Fatal("open() made the app's data zone")
	}
}

// create() makes a file or a folder by the trailing "/" alone, never by the extension (mục 13.2); again on
// one of the same kind it keeps the content (mục 13.3); on one of the other kind it is CONFLICT.
//
// CONTROL: with Create deciding a folder by the extension (no extension: a folder), "LICENSE is not an
// empty file" and "release.v1/ is not a folder" failed.
func TestFileCreateByTheTrailingSlash(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"create": `router.get((context) => {
  const license = file.open("LICENSE").create();
  const release = file.open("release.v1/").create();
  const nested = file.open("images/products/").create();
  file.open("notes.txt").write("giữ");
  const again = file.open("notes.txt").create();
  const folderAgain = file.open("release.v1/").create();
  return context.json({
    license: [license.type, license.size], release: [release.type, release.path, release.size],
    nested: nested.path, again: [again.type, again.size], folderAgain: folderAgain.type,
    notes: file.text("notes.txt"),
    fileAsFolder: reason(() => file.open("notes.txt/").create()),
    folderAsFile: reason(() => file.open("release.v1").create())
  });
});`,
	}, nil)
	var got struct {
		License      []any      `json:"license"`
		Release      []any      `json:"release"`
		Nested       string     `json:"nested"`
		Again        []any      `json:"again"`
		FolderAgain  string     `json:"folderAgain"`
		Notes        string     `json:"notes"`
		FileAsFolder fileFailed `json:"fileAsFolder"`
		FolderAsFile fileFailed `json:"folderAsFile"`
	}
	body := fileJSON(t, tn, "/create", &got)
	data := filepath.Join(app, ".database", "files")
	if stat, err := os.Stat(filepath.Join(data, "LICENSE")); err != nil || stat.IsDir() || stat.Size() != 0 || got.License[0] != "file" {
		t.Errorf("LICENSE is not an empty file: %v %v (%s)", stat, err, body)
	}
	if stat, err := os.Stat(filepath.Join(data, "release.v1")); err != nil || !stat.IsDir() || got.Release[0] != "directory" || got.Release[1] != "release.v1/" || got.Release[2] != nil {
		t.Errorf("release.v1/ is not a folder: %v %v (%s)", stat, err, body)
	}
	if got.Nested != "images/products/" || got.FolderAgain != "directory" {
		t.Errorf("nested = %q, again = %q", got.Nested, got.FolderAgain)
	}
	if got.Notes != "giữ" || got.Again[1] != float64(len("giữ")) {
		t.Errorf("create() again changed notes.txt: %q %v", got.Notes, got.Again)
	}
	if got.FileAsFolder.Code != "CONFLICT" || got.FileAsFolder.Message != `"notes.txt/" là file, không phải thư mục` {
		t.Errorf("notes.txt/ = %+v", got.FileAsFolder)
	}
	if got.FolderAsFile.Code != "CONFLICT" || got.FolderAsFile.Message != `"release.v1" là thư mục; một thư mục viết có "/" cuối: "release.v1/"` {
		t.Errorf("release.v1 = %+v", got.FolderAsFile)
	}
}

// read() decodes by the extension, text(), bytes() and json() say what to read (mục 4, mục 13.5): JSON
// broken and bytes that are not UTF-8 read as text are INVALID_INPUT, never replaced in silence; a
// folder read as a file is CONFLICT.
//
// CONTROL: with read() taking every extension for text, read(x.json) gave the raw string and the answer
// did not decode into its object.
func TestFileReadDecodesByTheExtension(t *testing.T) {
	tn, _ := fileSite(t, map[string]string{
		"read": `router.get((context) => {
  file.open("x.json").write({ a: [1, "b"] });
  file.open("notes.md").write("# Tiêu đề");
  file.open("a.png").write(file.bytes("x.json"));
  return context.json({
    json: file.read("x.json"), raw: file.text("x.json"), bytes: file.bytes("x.json").length,
    parsed: file.json("x.json").a[1], markdown: file.read("notes.md"), png: typeof file.read("a.png"),
    pngLength: file.read("a.png").length,
    broken: reason(() => file.json("app/localhost/broken.json")),
    badText: reason(() => file.text("app/localhost/bad.txt")),
    folder: reason(() => file.text("app/localhost/")),
    folderNoSlash: reason(() => file.read("app/localhost"))
  });
});`,
	}, map[string]string{
		"localhost/broken.json": `{"a": `,
		"localhost/bad.txt":     "\xff\xfe\x00bad",
	})
	var got struct {
		JSON          map[string]any `json:"json"`
		Raw           string         `json:"raw"`
		Bytes         int            `json:"bytes"`
		Parsed        string         `json:"parsed"`
		Markdown      string         `json:"markdown"`
		PNG           string         `json:"png"`
		PNGLength     int            `json:"pngLength"`
		Broken        fileFailed     `json:"broken"`
		BadText       fileFailed     `json:"badText"`
		Folder        fileFailed     `json:"folder"`
		FolderNoSlash fileFailed     `json:"folderNoSlash"`
	}
	body := fileJSON(t, tn, "/read", &got)
	if raw := `{"a":[1,"b"]}`; got.Raw != raw || got.Bytes != len(raw) || got.Parsed != "b" || got.JSON["a"] == nil {
		t.Errorf("read(x.json) = %v, text %q, bytes %d, json().a[1] %q", got.JSON, got.Raw, got.Bytes, got.Parsed)
	}
	if got.Markdown != "# Tiêu đề" {
		t.Errorf("read(notes.md) = %q", got.Markdown)
	}
	if got.PNG == "string" || got.PNGLength != len(`{"a":[1,"b"]}`) {
		t.Errorf("read(a.png) is %s of %d, want bytes (%s)", got.PNG, got.PNGLength, body)
	}
	if got.Broken.Code != "INVALID_INPUT" || !strings.HasPrefix(got.Broken.Message, "app/localhost/broken.json không phải JSON: ") {
		t.Errorf("broken JSON = %+v", got.Broken)
	}
	if got.BadText.Code != "INVALID_INPUT" || got.BadText.Message != `app/localhost/bad.txt không phải chữ UTF-8; byte đọc bằng file.bytes("app/localhost/bad.txt")` {
		t.Errorf("text(bad.txt) = %+v", got.BadText)
	}
	if got.Folder.Code != "CONFLICT" || got.FolderNoSlash.Code != "CONFLICT" || !strings.Contains(got.Folder.Message, `"app/localhost/" là thư mục, không phải file`) {
		t.Errorf("a folder read as a file = %+v, %+v", got.Folder, got.FolderNoSlash)
	}
}

// list() gives the direct children, not their children, not the engine's files half written; on a file it
// is CONFLICT, on a folder that is not there NOT_FOUND (mục 6, mục 13.6). info() is null when there is
// nothing, exists() false. The spec promises no order; the engine gives them by name, byte for byte, the
// same on every disk (NTFS gives its own order by name, ext4 a hash).
//
// CONTROL: with the temporaryPrefix filter of List removed, the half-written file was listed: "list(a/)
// gives more than its direct children, or not them: [a/.kitwork-half a/Zeta.txt a/b/ a/c/ a/one.txt]";
// with the sort of List removed, NTFS's order came back, Zeta.txt last: "[a/b/ a/c/ a/one.txt a/Zeta.txt]".
func TestFileListGivesDirectChildren(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"list": `router.get((context) => {
  file.open("a/one.txt").write("1");
  file.open("a/Zeta.txt").write("z");
  file.open("a/b/two.txt").write("2");
  file.open("a/c/").create();
  return context.json({
    names: file.list("a/").map((entry) => entry.path),
    types: file.list("a/").map((entry) => entry.type),
    onFile: reason(() => file.list("a/one.txt")),
    missing: reason(() => file.list("none/")),
    info: file.info("none.txt"), exists: file.exists("none.txt"), there: file.exists("a/"),
    folder: file.info("a/b/")
  });
});`,
	}, nil)
	// a write in progress beside them
	if err := os.MkdirAll(filepath.Join(app, ".database", "files", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, ".database", "files", "a", ".kitwork-half"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Names   []string       `json:"names"`
		Types   []string       `json:"types"`
		OnFile  fileFailed     `json:"onFile"`
		Missing fileFailed     `json:"missing"`
		Info    any            `json:"info"`
		Exists  bool           `json:"exists"`
		There   bool           `json:"there"`
		Folder  map[string]any `json:"folder"`
	}
	body := fileJSON(t, tn, "/list", &got)
	if strings.Join(got.Names, ",") != "a/Zeta.txt,a/b/,a/c/,a/one.txt" || strings.Join(got.Types, ",") != "file,directory,directory,file" {
		t.Errorf("list(a/) gives more than its direct children, or not them: %v %v", got.Names, got.Types)
	}
	if got.OnFile.Code != "CONFLICT" || got.OnFile.Message != `"a/one.txt" là file; list() liệt kê một thư mục` {
		t.Errorf("list on a file = %+v", got.OnFile)
	}
	if got.Missing.Code != "NOT_FOUND" || got.Missing.Message != `không có thư mục "none/"` {
		t.Errorf("list of nothing = %+v", got.Missing)
	}
	if got.Info != nil || got.Exists || !got.There || got.Folder["type"] != "directory" || got.Folder["size"] != nil ||
		got.Folder["extension"] != nil || got.Folder["path"] != "a/b/" || got.Folder["name"] != "b" {
		t.Errorf("info/exists = %s", body)
	}
}

// delete() never empties a folder: one that holds anything is CONFLICT with how many, clear() then
// delete() removes it, delete() of nothing is false; clear() past its ceiling is PARTIAL with how many it
// removed (error.progress), and what it removed stays removed (mục 7, mục 13.7, 13.8). The code zone's
// root, app/, is DENIED as all of the code zone; the data zone's root has no spelling a path can take
// (an empty path is INVALID_ARGUMENT), so neither delete() nor clear() reaches it (KITWORK_FILE.md mục
// 16, "Khác spec").
//
// CONTROL: with Delete removing a folder's content on the way (root.RemoveAll, the count of entries not
// asked), delete() of tmp/ answered true; with clearTree not counting against the ceiling, clear() of
// big/ emptied it (3) and was not PARTIAL.
func TestFileDeleteNeverRecursesAndClearEmpties(t *testing.T) {
	saved := fileTreeLimit
	t.Cleanup(func() { fileTreeLimit = saved })
	tn, app := fileSite(t, map[string]string{
		"delete": `router.get((context) => {
  file.open("tmp/a.txt").write("a");
  file.open("tmp/b/c.txt").write("c");
  file.open("empty/").create();
  const full = reason(() => file.open("tmp/").delete());
  const empty = file.open("empty/").delete();
  const missing = file.open("none.txt").delete();
  const cleared = file.open("tmp/").clear();
  const gone = file.open("tmp/").delete();
  file.open("big/1.txt").write("1");
  file.open("big/2.txt").write("2");
  file.open("big/3.txt").write("3");
  return context.json({ full, empty, missing, cleared, gone,
    root: reason(() => file.open("app/").clear()),
    dataRoot: [reason(() => file.open("").clear()), reason(() => file.open("").delete())],
    clearFile: reason(() => file.open("big/1.txt").clear()) });
});`,
		"partial": `router.get((context) => context.json({ first: reason(() => file.open("big/").clear()), left: file.list("big/").length }));`,
	}, nil)
	var got struct {
		Full      fileFailed   `json:"full"`
		Empty     bool         `json:"empty"`
		Missing   bool         `json:"missing"`
		Cleared   int          `json:"cleared"`
		Gone      bool         `json:"gone"`
		Root      fileFailed   `json:"root"`
		DataRoot  []fileFailed `json:"dataRoot"`
		ClearFile fileFailed   `json:"clearFile"`
	}
	body := fileJSON(t, tn, "/delete", &got)
	for _, failed := range got.DataRoot {
		if failed.Code != "INVALID_ARGUMENT" || failed.Message != `đường dẫn file không rỗng: file.json("settings.json"), file.list("uploads/")` {
			t.Errorf("clear()/delete() of the data zone's root = %+v", got.DataRoot)
		}
	}
	if _, err := os.Stat(filepath.Join(app, ".database", "files", "big", "1.txt")); err != nil {
		t.Errorf("the data zone's root was emptied: %v", err)
	}
	if got.Full.Code != "CONFLICT" || got.Full.Message != `thư mục "tmp/" còn 2 mục; delete() không xoá nội dung, file.open("tmp/").clear() trước rồi delete()` {
		t.Errorf("delete() of a folder that holds 2 entries = %+v", got.Full)
	}
	if !got.Empty || got.Missing || got.Cleared != 3 || !got.Gone {
		t.Errorf("delete/clear = %s", body)
	}
	if got.Root.Code != "DENIED" || got.ClearFile.Code != "CONFLICT" {
		t.Errorf("clear() of app/ = %+v, of a file = %+v", got.Root, got.ClearFile)
	}
	if _, err := os.Stat(filepath.Join(app, ".database", "files", "tmp")); err == nil {
		t.Error("tmp/ is still there after clear() then delete()")
	}

	fileTreeLimit = 2
	var partial struct {
		First fileFailed `json:"first"`
		Left  int        `json:"left"`
	}
	fileJSON(t, tn, "/partial", &partial)
	if partial.First.Code != "PARTIAL" || partial.First.Progress != float64(2) || partial.Left != 1 ||
		partial.First.Message != `clear() của "big/" dừng sau 2 mục: trần 2 mục một lần; gọi lại để làm tiếp` {
		t.Errorf("clear() past its ceiling = %+v, %d left", partial.First, partial.Left)
	}
}

// copy() of a folder past its ceiling is PARTIAL with how many entries it made (error.progress, the
// target folder counted) and says what is left (mục 8): the target is half made, a second copy() meets
// it as CONFLICT, and clear() then delete() of it, then a copy within the ceiling, does it whole.
//
// CONTROL: with copyTree not counting against the ceiling (both of its "*done >= limit" checks removed),
// the copy of src/ answered its FileInfo: "copy() past its ceiling = {Code: …}" with no PARTIAL.
func TestFileCopyOfAFolderStopsAtItsCeiling(t *testing.T) {
	saved := fileTreeLimit
	t.Cleanup(func() { fileTreeLimit = saved })
	tn, app := fileSite(t, map[string]string{
		"copy": `router.get((context) => context.json({
  first: reason(() => file.open("src/").copy("dst/")),
  again: reason(() => file.open("src/").copy("dst/")),
  cleared: reason(() => file.open("dst/").clear()),
  removed: reason(() => file.open("dst/").delete())
}));`,
		"whole": `router.get((context) => context.json(file.open("src/").copy("dst/").path));`,
	}, map[string]string{
		".database/files/src/1.txt": "1", ".database/files/src/2.txt": "2", ".database/files/src/3.txt": "3",
	})
	fileTreeLimit = 2
	var got struct {
		First, Again     fileFailed
		Cleared, Removed any
	}
	body := fileJSON(t, tn, "/copy", &got)
	if got.First.Code != "PARTIAL" || got.First.Progress != float64(2) ||
		got.First.Message != `copy() của "src/" dừng sau 2 mục: trần 2 mục một lần; đích "dst/" còn dở và copy() lại sẽ gặp CONFLICT: file.open("dst/").clear() rồi delete() rồi chép lại` {
		t.Errorf("copy() past its ceiling = %+v", got.First)
	}
	if got.Again.Code != "CONFLICT" || got.Cleared != float64(1) || got.Removed != true {
		t.Errorf("after a copy that stopped = %s", body)
	}
	fileTreeLimit = 10
	if whole := fileJSON(t, tn, "/whole", nil); whole != `"dst/"` {
		t.Errorf("copy() within the ceiling = %s", whole)
	}
	if entries, _ := os.ReadDir(filepath.Join(app, ".database", "files", "dst")); len(entries) != 3 {
		t.Errorf("dst/ holds %d entries, want 3", len(entries))
	}
}

// delete() is false only when there is nothing to remove: a file the system refuses to remove (a folder
// the app may not write on Unix, a file held open on Windows) is a failure, never false (mục 7, mục 13.8).
//
// CONTROL: with Delete answering false for any failure of root.Remove, the refused delete read false and
// this test failed.
func TestFileDeleteDoesNotHideARefusal(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"delete": `router.get((context) => context.json(reason(() => file.open("locked/a.txt").delete())));`,
	}, map[string]string{".database/files/locked/a.txt": "a"})
	locked := filepath.Join(app, ".database", "files", "locked")
	if runtime.GOOS == "windows" {
		held, err := os.Open(filepath.Join(locked, "a.txt")) // opened without FILE_SHARE_DELETE
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { held.Close() })
	} else {
		if err := os.Chmod(locked, 0o555); err != nil {
			t.Skipf("chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
	}
	rec := serveGet(tn, "/delete", nil)
	if _, err := os.Stat(filepath.Join(locked, "a.txt")); err != nil {
		t.Skipf("the system let the delete through (running as root?): %s", rec.Body.String())
	}
	var got fileFailed
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || (got.Code != "DENIED" && got.Code != "DATABASE") ||
		!strings.HasPrefix(got.Message, `xoá "locked/a.txt"`) {
		t.Errorf("a refused delete = %s, want a failure naming locked/a.txt", rec.Body.String())
	}
}

// copy() and move() make the folders of the target and never replace it (mục 8, mục 13.9); after move()
// the handle of the source keeps its path (mục 13.16); a folder copies whole; a copy into itself is
// refused; a file of the code zone copies into the data zone.
//
// CONTROL: with Copy's look at the target removed and place() renaming the copy onto it (root.Rename in
// place of root.Link), the second copy replaced backup/deep/a.txt and answered its FileInfo instead of
// CONFLICT. Each of the two alone keeps it (place() alone: TestPlaceNeverReplaces).
func TestFileCopyAndMoveNeverReplace(t *testing.T) {
	tn, _ := fileSite(t, map[string]string{
		"copy": `router.get((context) => {
  const source = file.open("uploads/a.txt");
  source.write("một");
  const first = source.copy("backup/deep/a.txt");
  file.open("other.txt").write("hai");
  const again = reason(() => file.open("other.txt").copy("backup/deep/a.txt"));
  const moved = source.move("archive/2026/a.txt");
  const kept = [source.path(), source.exists(), file.text("archive/2026/a.txt")];
  const onto = reason(() => file.open("other.txt").move("archive/2026/a.txt"));
  const tree = file.open("backup/").copy(file.open("copies/backup/"));
  const intoItself = reason(() => file.open("backup/").copy("backup/inner/"));
  const fromCode = file.open("app/localhost/data/menu.json").copy("menu.json");
  const kind = reason(() => file.open("backup/").copy("flat"));
  const folderMoved = file.open("copies/").move("moved/copies/");
  const folderOnto = reason(() => file.open("backup/").move("moved/"));
  const moveIntoItself = reason(() => file.open("moved/").move("moved/inner/"));
  return context.json({ first: first.path, again, moved: moved.path, kept, onto, tree: tree.path,
    treeText: file.text("moved/copies/backup/deep/a.txt"), intoItself, fromCode: file.json("menu.json").name, kind,
    missing: reason(() => file.open("none.txt").copy("x.txt")),
    folderMoved: [folderMoved.path, folderMoved.type, file.exists("copies/")], folderOnto, moveIntoItself });
});`,
	}, map[string]string{"localhost/data/menu.json": `{"name":"menu"}`})
	var got struct {
		First          string     `json:"first"`
		Again          fileFailed `json:"again"`
		Moved          string     `json:"moved"`
		Kept           []any      `json:"kept"`
		Onto           fileFailed `json:"onto"`
		Tree           string     `json:"tree"`
		TreeText       string     `json:"treeText"`
		IntoItself     fileFailed `json:"intoItself"`
		FromCode       string     `json:"fromCode"`
		Kind           fileFailed `json:"kind"`
		Missing        fileFailed `json:"missing"`
		FolderMoved    []any      `json:"folderMoved"`
		FolderOnto     fileFailed `json:"folderOnto"`
		MoveIntoItself fileFailed `json:"moveIntoItself"`
	}
	body := fileJSON(t, tn, "/copy", &got)
	if len(got.FolderMoved) != 3 || got.FolderMoved[0] != "moved/copies/" || got.FolderMoved[1] != "directory" || got.FolderMoved[2] != false {
		t.Errorf("move() of a folder = %v", got.FolderMoved)
	}
	if got.FolderOnto.Code != "CONFLICT" || got.FolderOnto.Message != `move(): "moved/" đã có; move() không ghi đè, muốn thay thì delete() đích trước` {
		t.Errorf("move() of a folder onto one = %+v", got.FolderOnto)
	}
	if got.MoveIntoItself.Code != "INVALID_ARGUMENT" || got.MoveIntoItself.Message != `move(): dời "moved/" vào trong chính nó ("moved/inner/")` {
		t.Errorf("move() into itself = %+v", got.MoveIntoItself)
	}
	if got.First != "backup/deep/a.txt" || got.Moved != "archive/2026/a.txt" || got.Tree != "copies/backup/" ||
		got.TreeText != "một" || got.FromCode != "menu" {
		t.Errorf("copy/move = %s", body)
	}
	if got.Again.Code != "CONFLICT" || got.Again.Message != `copy(): "backup/deep/a.txt" đã có; copy() không ghi đè, muốn thay thì delete() đích trước` {
		t.Errorf("the second copy = %+v", got.Again)
	}
	if got.Onto.Code != "CONFLICT" || !strings.Contains(got.Onto.Message, `"archive/2026/a.txt" đã có`) {
		t.Errorf("move onto a file = %+v", got.Onto)
	}
	if len(got.Kept) != 3 || got.Kept[0] != "uploads/a.txt" || got.Kept[1] != false || got.Kept[2] != "một" {
		t.Errorf("the source's handle after move() = %v", got.Kept)
	}
	if got.IntoItself.Code != "INVALID_ARGUMENT" || got.Kind.Code != "CONFLICT" || got.Missing.Code != "NOT_FOUND" ||
		got.Missing.Message != `không có file "none.txt"` {
		t.Errorf("into itself = %+v, kind = %+v, missing = %+v", got.IntoItself, got.Kind, got.Missing)
	}
}

// A path never leaves its zone (mục 2, mục 13.10, 13.12): a leading "/", "..", "\", an empty name, a
// drive, a device name of Windows, are INVALID_ARGUMENT before anything is touched; segment() takes one
// name a piece, so a value from outside written into it ("../x", or "..\bob\a.png", which Windows reads
// as three names) never climbs; another app is out of reach from either zone. "App/…" is not a folder of
// the data zone (mục 1: app is reserved first, in any case), and a name never takes the engine's prefix
// of a file half written, which list() hides.
//
// CONTROL: with checkName's case for "." and ".." removed, "../victim/secret.txt" was refused by the rule
// of a name ending in a dot instead, with that rule's words, and "up", "upCode" and "dots" failed; with
// its case for "\" removed, a segment of alice's folder read and wrote bob's: "segment() of alice's folder
// read bob's file" and "uploads/bob/ was written through alice's folder"; with Parse's case for "App"
// removed, "appCase" wrote a data folder App; with the case of the engine's prefix removed, "reserved"
// wrote a file list() does not show.
func TestFilePathsNeverLeaveTheZone(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"paths": `router.get((context) => {
  const id = "../x";
  const other = "..\\bob\\avatar.png";
  return context.json({
    backslashSegment: reason(() => file.open("uploads/alice/").segment(other).write(file.bytes("app/localhost/x.bin"))),
    backslashRead: reason(() => file.open("uploads/alice/").segment("..\\bob\\secret.txt").text()),
    backslashName: reason(() => file.open("uploads/").segment("a\\b")),
    appCase: reason(() => file.open("App/x.txt").write("x")),
    appUpper: reason(() => file.text("APP/localhost/x.bin")),
    reserved: reason(() => file.open("k/.kitwork-notes.txt").write("mine")),
    reservedSegment: reason(() => file.open("k/").segment(".KITWORK-x")),
    leading: reason(() => file.json("/settings.json")),
    up: reason(() => file.text("../victim/secret.txt")),
    upCode: reason(() => file.text("app/../victim/secret.txt")),
    backslash: reason(() => file.text("a\\b.txt")),
    empty: reason(() => file.text("a//b.txt")),
    drive: reason(() => file.text("C:/x.txt")),
    device: reason(() => file.open("nul.txt").write("x")),
    nothing: reason(() => file.text("")),
    slash: reason(() => file.open("uploads/").segment("a/b")),
    dots: reason(() => file.open("uploads/").segment("..")),
    blank: reason(() => file.open("uploads/").segment("")),
    outside: reason(() => file.open("uploads/").segment(id)),
    onFile: reason(() => file.open("a.txt").segment("b")),
    number: file.open("uploads/").segment(7, "a.png").path()
  });
});`,
	}, map[string]string{"localhost/x.bin": "\x00\x01", ".database/files/uploads/bob/secret.txt": "BOB-SECRET"})
	victim := filepath.Join(filepath.Dir(app), "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "secret.txt"), []byte("VICTIM"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	body := fileJSON(t, tn, "/paths", &got)
	if strings.Contains(body, "VICTIM") {
		t.Fatalf("another app's file was read: %s", body)
	}
	if strings.Contains(body, "BOB-SECRET") {
		t.Errorf("segment() of alice's folder read bob's file: %s", body)
	}
	want := map[string]string{
		"backslashRead":    `segment(…), đoạn thứ 1: đường dẫn file "..\\bob\\secret.txt": giữa các tên là "/", không bao giờ "\"`,
		"leading":          `đường dẫn file viết không có "/" đầu: "settings.json", không phải "/settings.json" (đường dẫn tính từ gốc vùng, không phải địa chỉ của site)`,
		"up":               `đường dẫn file "../victim/secret.txt" có "..": đường dẫn tính từ gốc vùng, không đi lên hay đứng yên`,
		"upCode":           `đường dẫn file "app/../victim/secret.txt" có "..": đường dẫn tính từ gốc vùng, không đi lên hay đứng yên`,
		"backslash":        `đường dẫn file "a\\b.txt": giữa các tên là "/", không bao giờ "\"`,
		"empty":            `đường dẫn file "a//b.txt" có một tên rỗng ("a//b"): mỗi đoạn giữa hai "/" là một tên`,
		"nothing":          `đường dẫn file không rỗng: file.json("settings.json"), file.list("uploads/")`,
		"slash":            `segment(…) nhận một tên mỗi đoạn, đoạn thứ 1 là "a/b": có "/" ở giữa; nhiều tên thì segment("a", "b")`,
		"dots":             `segment(…), đoạn thứ 1: đường dẫn file ".." có "..": đường dẫn tính từ gốc vùng, không đi lên hay đứng yên`,
		"blank":            `segment(…) nhận chuỗi không rỗng hay số, đoạn thứ 1 là chuỗi rỗng: dựng đường dẫn bằng từng đoạn, file.open("uploads/").segment(id, "avatar.png")`,
		"outside":          `segment(…) nhận một tên mỗi đoạn, đoạn thứ 1 là "../x": có "/" ở giữa; nhiều tên thì segment("a", "b")`,
		"onFile":           `segment(…) chỉ có trên thư mục, đường dẫn có "/" cuối: "a.txt" là file; chọn con của một thư mục, file.open("uploads/").segment("a.png")`,
		"backslashSegment": `segment(…), đoạn thứ 1: đường dẫn file "..\\bob\\avatar.png": giữa các tên là "/", không bao giờ "\"`,
		"backslashName":    `segment(…), đoạn thứ 1: đường dẫn file "a\\b": giữa các tên là "/", không bao giờ "\"`,
		"appCase":          `đường dẫn file "App/x.txt": app là tên dành riêng ở đầu đường dẫn, vùng dữ liệu không có thư mục gốc tên đó; code của app viết chữ thường, "app/x.txt"`,
		"appUpper":         `đường dẫn file "APP/localhost/x.bin": app là tên dành riêng ở đầu đường dẫn, vùng dữ liệu không có thư mục gốc tên đó; code của app viết chữ thường, "app/localhost/x.bin"`,
		"reserved":         `đường dẫn file "k/.kitwork-notes.txt": tên bắt đầu bằng ".kitwork-" dành cho file engine đang ghi dở (list() không hiện chúng); chọn tên khác`,
		"reservedSegment":  `segment(…), đoạn thứ 1: đường dẫn file ".KITWORK-x": tên bắt đầu bằng ".kitwork-" dành cho file engine đang ghi dở (list() không hiện chúng); chọn tên khác`,
	}
	for name, message := range want {
		var failed fileFailed
		if err := json.Unmarshal(got[name], &failed); err != nil || failed.Code != "INVALID_ARGUMENT" || failed.Message != message {
			t.Errorf("%s = %s, want INVALID_ARGUMENT %q", name, got[name], message)
		}
	}
	for _, name := range []string{"drive", "device"} {
		var failed fileFailed
		if err := json.Unmarshal(got[name], &failed); err != nil || failed.Code != "INVALID_ARGUMENT" {
			t.Errorf("%s = %s, want INVALID_ARGUMENT", name, got[name])
		}
	}
	if string(got["number"]) != `"uploads/7/a.png"` {
		t.Errorf("segment(7, \"a.png\") = %s", got["number"])
	}
	data := filepath.Join(app, ".database", "files")
	if _, err := os.Stat(filepath.Join(data, "uploads", "bob", "avatar.png")); err == nil {
		t.Error("uploads/bob/ was written through alice's folder")
	}
	if entries, _ := os.ReadDir(data); len(entries) != 1 || entries[0].Name() != "uploads" {
		t.Errorf("a refused path wrote into the data zone: %v", entries)
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "uploads")); len(entries) != 1 || entries[0].Name() != "bob" {
		t.Errorf("a refused path wrote into uploads/: %v", entries)
	}
}

// A link inside the data zone that leads out of it, to another app, is refused when an operation runs, by
// the system (os.Root): reading through it and writing through it are DENIED, nothing lands outside, and
// context.file through it answers 404 (mục 2, mục 9, mục 13.10, 13.14).
//
// CONTROL: with Zone.Read opening the machine path (os.Open of App, Folder and the names joined) instead of
// the zone's os.Root, the link was followed and "read" answered the victim's file.
func TestFileLinkOutOfTheZoneIsDenied(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"link": `router.get((context) => context.json({
  read: reason(() => file.text("external/secret.txt")),
  write: reason(() => file.open("external/planted.txt").write("x")),
  list: reason(() => file.list("external/"))
}));`,
		"serve": `router.get((context) => context.file(file.open("external/secret.txt")));`,
	}, map[string]string{".database/files/own.txt": "own"})
	victim := filepath.Join(filepath.Dir(app), "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "secret.txt"), []byte("VICTIM"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkFolder(t, victim, filepath.Join(app, ".database", "files", "external"))
	var got struct{ Read, Write, List fileFailed }
	body := fileJSON(t, tn, "/link", &got)
	if strings.Contains(body, "VICTIM") {
		t.Fatalf("a link out of the zone was followed: %s", body)
	}
	if got.Read.Code != "DENIED" || got.Write.Code != "DENIED" || got.List.Code != "DENIED" {
		t.Errorf("through a link out = %s, want DENIED for each", body)
	}
	if _, err := os.Stat(filepath.Join(victim, "planted.txt")); err == nil {
		t.Error("a write through a link landed in another app")
	}
	if served := serveGet(tn, "/serve", nil); served.Code != 404 || strings.Contains(served.Body.String(), "VICTIM") {
		t.Errorf("context.file through a link out = %d %.200q, want 404", served.Code, served.Body.String())
	}
	if strings.Contains(body, filepath.Dir(app)) {
		t.Errorf("a message names the machine's path: %s", body)
	}
}

// The code zone, app/…, reads only (mục 1, mục 13.11): every change on it is DENIED with the spec's
// words; .env, .env.* and .database/ are never read through it, whatever their case, and list() does not
// show them; a file of it copies into the data zone, and a folder of it copies without them: the site's
// folder leaves its .env.local behind, the app's whole folder its .env and .database/.
//
// CONTROL: with Zone.denied answering false (the deny of .env and .database/ removed), app/.env read its
// secret: "the code zone read an env file"; with the two skips of copyTree for what the code zone never
// reaches removed, the copy of app/localhost/ carried .env.local into the data zone ("a folder copied out
// of the code zone carried …: dump/.env.local").
func TestFileCodeZoneCopiesWithoutWhatItNeverReaches(t *testing.T) {
	saved := fileTreeLimit
	t.Cleanup(func() { fileTreeLimit = saved })
	fileTreeLimit = 200 // a copy of app/ that walked into .database/files would stop here, not run on
	tn, app := fileSite(t, map[string]string{
		"dump": `router.get((context) => context.json({
  site: file.open("app/localhost/").copy("dump/").path,
  whole: reason(() => file.open("app/").copy("whole/").path),
  siteNames: file.list("dump/").map((entry) => entry.name),
  wholeNames: file.list("whole/").map((entry) => entry.name)
}));`,
	}, map[string]string{
		".env": "SECRET=1", ".env.production": "SECRET=3", "localhost/.env.local": "SECRET=2", "localhost/data.txt": "dữ liệu",
		".database/files/own.txt": "own",
	})
	var got struct {
		Site       string   `json:"site"`
		Whole      any      `json:"whole"`
		SiteNames  []string `json:"siteNames"`
		WholeNames []string `json:"wholeNames"`
	}
	body := fileJSON(t, tn, "/dump", &got)
	if got.Site != "dump/" || got.Whole != "whole/" || strings.Join(got.SiteNames, ",") != "data.txt,dump,router.work" ||
		strings.Join(got.WholeNames, ",") != "localhost" {
		t.Errorf("a folder copied out of the code zone = %s", body)
	}
	data := filepath.Join(app, ".database", "files")
	for _, name := range []string{"dump/.env.local", "whole/.env", "whole/.env.production", "whole/.database", "whole/localhost/.env.local"} {
		if _, err := os.Lstat(filepath.Join(data, filepath.FromSlash(name))); err == nil {
			t.Errorf("a folder copied out of the code zone carried what the code zone never reaches: %s", name)
		}
	}
}

// The code zone, app/…, reads only (mục 1, mục 13.11): every change on it is DENIED with the spec's
// words; .env, .env.* and .database/ are never read through it, whatever their case, and list() does not
// show them; a file of it copies into the data zone.
//
// CONTROL: with Zone.denied answering false (the deny of .env and .database/ removed), app/.env read its
// secret: "the code zone read an env file".
func TestFileCodeZoneReadsOnly(t *testing.T) {
	tn, _ := fileSite(t, map[string]string{
		"code": `router.get((context) => context.json({
  write: reason(() => file.open("app/localhost/x.txt").write("x")),
  create: reason(() => file.open("app/localhost/new/").create()),
  copyTo: reason(() => file.open("own.txt").copy("app/localhost/own.txt")),
  move: reason(() => file.open("app/localhost/data.txt").move("data.txt")),
  delete: reason(() => file.open("app/localhost/data.txt").delete()),
  clear: reason(() => file.open("app/localhost/").clear()),
  env: reason(() => file.text("app/.env")),
  envUpper: reason(() => file.text("app/.ENV")),
  envLocal: reason(() => file.text("app/localhost/.env.local")),
  database: reason(() => file.text("app/.database/files/own.txt")),
  databaseList: reason(() => file.list("app/.database/")),
  envExists: reason(() => file.exists("app/.env")),
  listed: file.list("app/").map((entry) => entry.name),
  copied: file.open("app/localhost/data.txt").copy("data-copy.txt").size,
  read: file.text("app/localhost/data.txt")
}));`,
	}, map[string]string{
		".env": "SECRET=1", "localhost/.env.local": "SECRET=2", "localhost/data.txt": "dữ liệu",
		".database/files/own.txt": "own",
	})
	var got map[string]json.RawMessage
	body := fileJSON(t, tn, "/code", &got)
	if strings.Contains(body, "SECRET") {
		t.Fatalf("the code zone read an env file: %s", body)
	}
	readOnly := map[string]string{
		"write":  `write() "app/localhost/x.txt": code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`,
		"create": `create() "app/localhost/new/": code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`,
		"copyTo": `copy() tới "app/localhost/own.txt": code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`,
		"move":   `move() "app/localhost/data.txt": code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`,
		"delete": `delete() "app/localhost/data.txt": code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`,
		"clear":  `clear() "app/localhost/": code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`,
	}
	for name, message := range readOnly {
		var failed fileFailed
		if err := json.Unmarshal(got[name], &failed); err != nil || failed.Code != "DENIED" || failed.Message != message {
			t.Errorf("%s = %s, want DENIED %q", name, got[name], message)
		}
	}
	for _, name := range []string{"env", "envUpper", "envLocal", "database", "databaseList", "envExists"} {
		var failed fileFailed
		if err := json.Unmarshal(got[name], &failed); err != nil || failed.Code != "DENIED" ||
			!strings.Contains(failed.Message, ".env, .env.* và .database/ không bao giờ đọc được qua app/") {
			t.Errorf("%s = %s, want DENIED", name, got[name])
		}
	}
	var listed []string
	json.Unmarshal(got["listed"], &listed)
	if strings.Join(listed, ",") != "localhost" {
		t.Errorf(`list("app/") = %v, want only the site's folder`, listed)
	}
	if string(got["copied"]) != fmt.Sprint(len("dữ liệu")) || string(got["read"]) != `"dữ liệu"` {
		t.Errorf("copy out of the code zone = %s, read = %s", got["copied"], got["read"])
	}
}

// .env and .database/ are refused by the names the disk holds, not only as written (mục 1, mục 2): on
// Windows the short name of each (ENV~1, DATABA~1) passes a check of names as written and reaches the same
// file, and a link in the site's folder to either (a symbolic link where the system makes one, a junction
// on Windows) reads as another name. Each is DENIED for text, exists and list, and context.file of it is
// 404; nothing of what they hold reaches the answer.
//
// CONTROL: with the check of Zone.denied on the names the disk holds removed (its Boundary.Relative
// block), app/ENV~1 read "SECRET=1" and app/DATABA~1/files/own.txt "OWN-DATA", and context.file of it
// answered 200: "the code zone reached what it never reaches by another name".
func TestFileCodeZoneDeniesByTheNamesOnTheDisk(t *testing.T) {
	tn, app := fileSite(t, map[string]string{
		"read": `router.get((context) => context.json({
  text: reason(() => file.text(context.query("p"))),
  exists: reason(() => file.exists(context.query("p"))),
  list: reason(() => file.list(context.query("folder")))
}));`,
		"serve": `router.get((context) => context.file(file.open(context.query("p"))));`,
	}, map[string]string{".env": "SECRET=1", ".database/files/own.txt": "OWN-DATA"})

	type spelling struct{ file, folder string }
	var spellings []spelling
	if runtime.GOOS == "windows" {
		env, database := shortName(t, app, ".env"), shortName(t, app, ".database")
		if env != "" && database != "" {
			spellings = append(spellings, spelling{"app/" + env, "app/" + database + "/"}, spelling{"app/" + database + "/files/own.txt", "app/" + database + "/files/"})
		}
	}
	if os.Symlink(filepath.Join("..", ".env"), filepath.Join(app, "localhost", "envlink")) == nil &&
		os.Symlink(filepath.Join("..", ".database"), filepath.Join(app, "localhost", "dblink")) == nil {
		spellings = append(spellings, spelling{"app/localhost/envlink", "app/localhost/dblink/"}, spelling{"app/localhost/dblink/files/own.txt", "app/localhost/dblink/files/"})
	} else if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(app, "localhost", "dbjunction"), filepath.Join(app, ".database")).CombinedOutput(); err == nil {
			spellings = append(spellings, spelling{"app/localhost/dbjunction/files/own.txt", "app/localhost/dbjunction/files/"})
		} else {
			t.Logf("no junction: %v %s", err, out)
		}
	}
	if len(spellings) == 0 {
		t.Skip("this disk gives neither short names nor links")
	}
	for _, each := range spellings {
		var got map[string]json.RawMessage
		body := fileJSON(t, tn, "/read?p="+url.QueryEscape(each.file)+"&folder="+url.QueryEscape(each.folder), &got)
		if strings.Contains(body, "SECRET") || strings.Contains(body, "OWN-DATA") || strings.Contains(body, "own.txt\"") {
			t.Errorf("the code zone reached what it never reaches by another name, %s: %s", each.file, body)
		}
		for _, name := range []string{"text", "exists", "list"} {
			var failed fileFailed
			if err := json.Unmarshal(got[name], &failed); err != nil || failed.Code != "DENIED" {
				t.Errorf("%s of %s = %s, want DENIED", name, each.file, got[name])
			}
		}
		if served := serveGet(tn, "/serve?p="+url.QueryEscape(each.file), nil); served.Code != http.StatusNotFound ||
			strings.Contains(served.Body.String(), "SECRET") || strings.Contains(served.Body.String(), "OWN-DATA") {
			t.Errorf("context.file(%s) = %d %.200q, want 404", each.file, served.Code, served.Body.String())
		}
	}
}

// shortName is the 8.3 name Windows gives long within folder (ENV~1 for .env), "" when the disk keeps none:
// the line of `dir /x` for long, whose field before the name is the short one.
func shortName(t *testing.T, folder, long string) string {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "dir", "/x", "/a", folder).CombinedOutput()
	if err != nil {
		t.Logf("dir /x: %v %s", err, out)
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[len(fields)-1] == long && strings.Contains(fields[len(fields)-2], "~") {
			return fields[len(fields)-2]
		}
	}
	return ""
}

// linkFolder makes link lead to the folder target: a symbolic link, or on Windows without the right to
// make one, a junction (mklink /J needs no privilege). It skips the test when the system makes neither.
func linkFolder(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if junctionErr == nil {
			return
		}
		t.Skipf("neither a symlink (%v) nor a junction (%v: %s)", err, junctionErr, out)
	}
	t.Skipf("symlink unavailable: %v", err)
}
