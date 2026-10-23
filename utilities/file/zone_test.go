package file

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/safepath"
	"github.com/kitwork/engine/value"
)

// zoneOf is the data zone of an app folder made for the test.
func zoneOf(t *testing.T) (*Zone, string) {
	t.Helper()
	app := t.TempDir()
	boundary, err := safepath.NewBoundary(app)
	if err != nil {
		t.Fatal(err)
	}
	return &Zone{App: app, Folder: filepath.Join(".database", "files"), Boundary: boundary}, app
}

func mustParse(t *testing.T, written string) Path {
	t.Helper()
	path, failed := Parse(written)
	if failed != nil {
		t.Fatalf("Parse(%q): %v", written, failed)
	}
	return path
}

// failingReader gives some bytes, then fails: a write cut midway (the disk, the client gone).
type failingReader struct{ given bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.given {
		r.given = true
		return copy(p, "HALF OF THE NEW"), nil
	}
	return 0, errors.New("the client went away")
}

// A write that fails midway leaves the old content as it was and nothing half written beside it: the
// bytes go to a file of their own renamed over the target only once whole (KITWORK_FILE.md mục 5, mục
// 13.15). The failure is DATABASE, naming the path as written.
//
// CONTROL: with Write opening the target itself (root.OpenFile with O_TRUNC, then io.Copy) in place of
// temporary, fill and Rename, "the old content was replaced by half of the new" failed.
func TestWriteIsWholeOrNothing(t *testing.T) {
	zone, app := zoneOf(t)
	data := filepath.Join(app, zone.Folder)
	path := mustParse(t, "notes/a.bin")
	if _, failed := zone.Write(path, strings.NewReader("OLD CONTENT")); failed != nil {
		t.Fatal(failed)
	}
	_, failed := zone.Write(path, &failingReader{})
	if failed == nil || failed.Code != value.DatabaseCode || !strings.HasPrefix(failed.Message, `ghi "notes/a.bin" hỏng: `) {
		t.Fatalf("a write cut midway = %v", failed)
	}
	content, err := os.ReadFile(filepath.Join(data, "notes", "a.bin"))
	if err != nil || string(content) != "OLD CONTENT" {
		t.Fatalf("the old content was replaced by half of the new: %q %v", content, err)
	}
	entries, _ := os.ReadDir(filepath.Join(data, "notes"))
	if len(entries) != 1 {
		t.Fatalf("something half written stayed beside the file: %v", entries)
	}
}

// A path is checked as written before the disk is touched (mục 2): "app/" is the code zone, in that
// spelling only (mục 1: no root folder of the data zone is named app, in any case), a trailing "/" a
// folder, and every name one a zone may hold on every system, never the engine's own prefix of a file
// half written. segment(…) holds each name to the same rules: a "\" in one would climb out of its folder
// on Windows, where it separates names.
//
// CONTROL: with checkName's case for "." and ".." removed, "a/../b" parsed; with its case for "\" removed,
// segment("..\\bob\\a.png") gave a path, and with the case of Parse for "App" removed, "App/x" parsed into
// the data zone.
func TestParseReadsThePathAsWritten(t *testing.T) {
	for written, want := range map[string]string{
		"uploads/a.png":  "uploads/a.png",
		"images/":        "images/",
		"release.v1/":    "release.v1/",
		"LICENSE":        "LICENSE",
		"app/shop.com/x": "app/shop.com/x",
		"app":            "app/",
		"app/":           "app/",
	} {
		path, failed := Parse(written)
		if failed != nil || path.String() != want {
			t.Errorf("Parse(%q) = %q %v, want %q", written, path.String(), failed, want)
		}
	}
	if path := mustParse(t, "app/shop.com/data/menu.json"); !path.Code || strings.Join(path.Names, "|") != "shop.com|data|menu.json" || path.Directory {
		t.Errorf("app/… = %+v", path)
	}
	if _, failed := Parse("a/../b"); failed == nil || failed.Message != `đường dẫn file "a/../b" có "..": đường dẫn tính từ gốc vùng, không đi lên hay đứng yên` {
		t.Errorf(`Parse("a/../b") = %v`, failed)
	}
	for _, written := range []string{"", "/a", "a/../b", "..", "./a", "a//b", `a\b`, "C:/a", "a:b", "nul", "COM1.txt",
		"a.", "a ", "a\x00b", "a\nb", "a?b", "a*b", `a"b`, "a|b", "a<b", "App/x", "APP", "aPP/", ".kitwork-x",
		"a/.Kitwork-y.txt", "app/a/.kitwork-z"} {
		if _, failed := Parse(written); failed == nil || failed.Code != value.InvalidArgumentCode {
			t.Errorf("Parse(%q) = %v, want INVALID_ARGUMENT", written, failed)
		}
	}
	if _, failed := Parse("App/shop.com/menu.json"); failed == nil || failed.Message != `đường dẫn file "App/shop.com/menu.json": app là tên dành riêng ở đầu đường dẫn, vùng dữ liệu không có thư mục gốc tên đó; code của app viết chữ thường, "app/shop.com/menu.json"` {
		t.Errorf(`Parse("App/…") = %v`, failed)
	}
	for _, name := range []string{`..\bob\a.png`, `a\b`, "..", ".kitwork-x", "a:b"} {
		if _, failed := mustParse(t, "uploads/alice/").Segment([]string{name}); failed == nil || failed.Code != value.InvalidArgumentCode {
			t.Errorf("segment(%q) = %v, want INVALID_ARGUMENT", name, failed)
		}
	}
	if _, failed := mustParse(t, "uploads/alice/").Segment([]string{`..\bob\a.png`}); failed == nil ||
		failed.Message != `segment(…), đoạn thứ 1: đường dẫn file "..\\bob\\a.png": giữa các tên là "/", không bao giờ "\"` {
		t.Errorf(`segment("..\\bob\\a.png") = %v`, failed)
	}
	// names of other zones are free: "application/" and "apps.json" are not the code zone
	for written, want := range map[string]string{"application/": "application/", "apps.json": "apps.json", "my.kitwork-x": "my.kitwork-x"} {
		if path, failed := Parse(written); failed != nil || path.Code || path.String() != want {
			t.Errorf("Parse(%q) = %+v %v", written, path, failed)
		}
	}
}

// copy() never replaces what is there (mục 8): a second copy onto the same target is CONFLICT and the
// first stays; a move onto it is CONFLICT and keeps its source.
//
// CONTROL: Copy guards twice, by its look at the target and by place()'s hard link, so either alone keeps
// this green (place() alone: TestPlaceNeverReplaces). With both taken out (the look removed and place()
// renaming, root.Rename for root.Link), the second copy replaced the first: "the second copy = <nil>, want
// CONFLICT".
func TestCopyNeverReplaces(t *testing.T) {
	zone, app := zoneOf(t)
	data := filepath.Join(app, zone.Folder)
	one, two, target := mustParse(t, "one.txt"), mustParse(t, "two.txt"), mustParse(t, "backup/x.txt")
	zone.Write(one, strings.NewReader("1"))
	zone.Write(two, strings.NewReader("2"))
	if _, failed := zone.Copy(zone, one, target, 10, nil); failed != nil {
		t.Fatal(failed)
	}
	if _, failed := zone.Copy(zone, two, target, 10, nil); failed == nil || failed.Code != value.ConflictCode {
		t.Fatalf("the second copy = %v, want CONFLICT", failed)
	}
	if content, _ := os.ReadFile(filepath.Join(data, "backup", "x.txt")); string(content) != "1" {
		t.Fatalf("the target became %q", content)
	}
	if _, failed := zone.Move(two, target); failed == nil || failed.Code != value.ConflictCode {
		t.Fatalf("move onto it = %v, want CONFLICT", failed)
	}
	if _, err := os.Stat(filepath.Join(data, "two.txt")); err != nil {
		t.Fatalf("a refused move removed its source: %v", err)
	}
}

// clear() and copy() of a folder stop when the request they run for ends (mục 7, mục 8): PARTIAL with what
// they did, the reason "request đã xong", and what to do next; a copy that made part of its target says
// that a second copy() meets it, and how to start again.
//
// CONTROL: with the stopped(stop) checks of clearTree and copyTree removed, Clear emptied the folder and
// Copy copied it whole: "clear() after the request ended = 2, <nil>".
func TestTreeWalksStopWhenTheRequestEnds(t *testing.T) {
	zone, app := zoneOf(t)
	data := filepath.Join(app, zone.Folder)
	for _, name := range []string{"src/a.txt", "src/b.txt"} {
		if _, failed := zone.Write(mustParse(t, name), strings.NewReader(name)); failed != nil {
			t.Fatal(failed)
		}
	}
	ended := make(chan struct{})
	close(ended)

	removed, failed := zone.Clear(mustParse(t, "src/"), 100, ended)
	if failed == nil || failed.Code != value.PartialCode || removed != 0 || failed.Done != 0 ||
		failed.Message != `clear() của "src/" dừng sau 0 mục: request đã xong; gọi lại để làm tiếp` {
		t.Errorf("clear() after the request ended = %d, %v", removed, failed)
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "src")); len(entries) != 2 {
		t.Errorf("clear() after the request ended removed %d entries", 2-len(entries))
	}

	// a ceiling of one: the target folder is made, then the walk stops, which a request ending midway
	// does as well
	_, failed = zone.Copy(zone, mustParse(t, "src/"), mustParse(t, "dst/"), 1, nil)
	if failed == nil || failed.Code != value.PartialCode || failed.Done != 1 ||
		failed.Message != `copy() của "src/" dừng sau 1 mục: trần 1 mục một lần; đích "dst/" còn dở và copy() lại sẽ gặp CONFLICT: file.open("dst/").clear() rồi delete() rồi chép lại` {
		t.Errorf("copy() past its ceiling = %v", failed)
	}
	_, failed = zone.Copy(zone, mustParse(t, "src/"), mustParse(t, "other/"), 100, ended)
	if failed == nil || failed.Code != value.PartialCode || failed.Done != 0 ||
		!strings.HasPrefix(failed.Message, `copy() của "src/" dừng sau 0 mục: request đã xong`) {
		t.Errorf("copy() after the request ended = %v", failed)
	}
	if _, err := os.Stat(filepath.Join(data, "other", "a.txt")); err == nil {
		t.Error("copy() after the request ended copied a.txt")
	}
}

// place() never replaces the target, even one that came between the look and the write: the file is put
// in place with a hard link, which the system refuses over an existing name (mục 8).
//
// CONTROL: with place() renaming over the target (root.Rename for root.Link), the target became the new
// file and place() returned no failure.
func TestPlaceNeverReplaces(t *testing.T) {
	zone, app := zoneOf(t)
	if _, failed := zone.Write(mustParse(t, "x.txt"), strings.NewReader("FIRST")); failed != nil {
		t.Fatal(failed)
	}
	root, failed := zone.open(false)
	if failed != nil {
		t.Fatal(failed)
	}
	defer root.Close()
	file, name, err := temporary(root, mustParse(t, "x.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fill(root, file, name, strings.NewReader("SECOND")); err != nil {
		t.Fatal(err)
	}
	if failed := place(root, name, mustParse(t, "x.txt"), "copy()"); failed == nil || failed.Code != value.ConflictCode {
		t.Fatalf("place() over an existing file = %v, want CONFLICT", failed)
	}
	content, _ := os.ReadFile(filepath.Join(app, zone.Folder, "x.txt"))
	entries, _ := os.ReadDir(filepath.Join(app, zone.Folder))
	if string(content) != "FIRST" || len(entries) != 1 {
		t.Fatalf("the target became %q, the zone holds %d entries", content, len(entries))
	}
}

// The data zone's root that leads out of the app's folder through a link is refused before anything is
// read or written (mục 2): the zone is opened through the app's os.Root, which refuses a symlink or a
// junction out of it (on Windows the junction is what safepath's EvalSymlinks does not follow).
//
// CONTROL: with open() opening the zone's folder by its machine path (os.OpenRoot of App joined with
// Folder, after safepath.Contains of it), the junction was taken for a folder of the app and the write
// landed outside: "write through a zone that leads out = <nil>".
func TestZoneRootOutOfTheAppIsDenied(t *testing.T) {
	zone, app := zoneOf(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(app, ".database"), 0o755); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(app, zone.Folder)
	if err := os.Symlink(outside, folder); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		// Windows without the right to make a symlink: a junction needs none
		if out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", folder, outside).CombinedOutput(); junctionErr != nil {
			t.Skipf("neither a symlink (%v) nor a junction (%v: %s)", err, junctionErr, out)
		}
	}
	if _, failed := zone.Write(mustParse(t, "a.txt"), strings.NewReader("x")); failed == nil || failed.Code != value.DeniedCode {
		t.Fatalf("write through a zone that leads out = %v, want DENIED", failed)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("something was written outside the app: %v", entries)
	}
}
