package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	filehelper "github.com/kitwork/engine/utilities/file"
	"github.com/kitwork/engine/utilities/safepath"
	"github.com/kitwork/engine/value"
)

// ── kitwork/file (KITWORK_FILE.md) ─────────────────────────────────────────────────────────────────
//
//	import * as file from "kitwork/file";
//	const settings = file.json("settings.json");            // read: call directly
//	file.open("notes.txt").write("Xin chào");               // change: open() first
//	file.open("uploads/photos/").segment("a.png").copy("backup/a.png");
//	const menu = file.json("app/shop.com/data/menu.json");  // the app's code: read only, through app/
//
// Two zones (mục 1): the app's data, <app>/.database/files/, made at the first write, never served, never
// deployed; and its code, the app's folder, read only, written "app/…", where .env, .env.* and .database/
// are never reached. A path names neither the app nor the machine: the engine picks the folders of the
// caller's app. The machinery (paths, the os.Root of a zone, writes renamed into place, copies that never
// replace) is utilities/file; here are the words a program uses, the values it gets, and the failures of
// the closed set it reads with safe().
//
// open() touches nothing and a handle never changes (segment() makes another); every member of the module
// and of a handle is a function, read or called, so reading handle.delete deletes nothing. A change at the
// top level of a file fails: a file only declares while it loads.

// The ceilings of kitwork/file. The spec leaves the numbers open (mục 15.2); vars so tests can lower them.
var (
	// fileReadLimit is the most a read brings into the VM's memory (mục 4): past it TOO_LARGE, and the file
	// is served with context.file instead.
	fileReadLimit int64 = 8 << 20
	// fileTreeLimit is the most entries clear(), and copy() of a folder, do in one call (mục 7, mục 8).
	fileTreeLimit = 10_000
)

// fileModule is kitwork/file of one app.
type fileModule struct {
	tenant *Tenant
}

// File is the module: import * as file from "kitwork/file".
func (w *KitWork) File() value.Value {
	return value.Value{K: value.Proxy, V: &fileModule{tenant: w.tenant}}
}

// fileModuleWords are what a program calls on the module.
var fileModuleWords = map[string]bool{
	"open": true, "read": true, "text": true, "bytes": true, "json": true, "list": true, "info": true, "exists": true,
}

// fileModuleWord is the failure of a word the module has not: the old spellings name the new, file.env
// names environment.KEY.
func fileModuleWord(word string) value.Value {
	if fileModuleWords[word] {
		return value.Value{K: value.Nil}
	}
	if word == "env" {
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.FileEnvInApp)
	}
	return value.InvalidFailure(value.InvalidArgumentCode, compiler.FileUnknownWord(word))
}

func (m *fileModule) OnGet(key string) value.Value {
	if failure := fileModuleWord(key); failure.IsError {
		return failure
	}
	// read by name (const { json } = file): called alone it is still handed its caller
	return value.NewCallerFunction(func(caller any, args []value.Value) value.Value {
		vm, _ := caller.(*runtime.VM)
		return m.call(vm, key, args)
	})
}

func (m *fileModule) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

// OnInvoke is a call by a host, which lends no VM.
func (m *fileModule) OnInvoke(method string, args ...value.Value) value.Value {
	return m.call(nil, method, args)
}

// InvokeFrom is file.<method>(…) written in a program: caller is the VM that runs it.
func (m *fileModule) InvokeFrom(caller *runtime.VM, method string, args []value.Value) (value.Value, bool) {
	return m.call(caller, method, args), true
}

func (m *fileModule) call(caller *runtime.VM, method string, args []value.Value) value.Value {
	if failure := fileModuleWord(method); failure.IsError {
		return failure
	}
	if len(args) != 1 || args[0].K != value.String {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`file.%s(đường_dẫn) nhận một đường dẫn là chuỗi: file.%s("%s")`, method, method, fileExample(method)))
	}
	path, failed := filehelper.Parse(args[0].Text())
	if failed != nil {
		return fileFailure(failed)
	}
	handle := &fileHandle{tenant: m.tenant, path: path}
	if method == "open" {
		return value.Value{K: value.Proxy, V: handle}
	}
	return handle.call(caller, method, nil)
}

// fileExample is a path a word is written with, for a message.
func fileExample(word string) string {
	switch word {
	case "json":
		return "settings.json"
	case "list":
		return "uploads/"
	case "bytes":
		return "uploads/a.png"
	}
	return "notes.txt"
}

// ── the handle ─────────────────────────────────────────────────────────────────────────────────────

// fileHandle is a path of the caller's app (mục 2): its logical path and the app it belongs to. It never
// changes, and opening it touched nothing.
type fileHandle struct {
	tenant *Tenant
	path   filehelper.Path
}

// fileHandleWords are the words of a handle.
var fileHandleWords = map[string]bool{
	"segment": true, "name": true, "path": true,
	"read": true, "text": true, "bytes": true, "json": true, "list": true, "info": true, "exists": true,
	"create": true, "write": true, "copy": true, "move": true, "delete": true, "clear": true,
}

// fileChanges are the words that change the disk: none of them runs while a file loads.
var fileChanges = map[string]bool{"create": true, "write": true, "copy": true, "move": true, "delete": true, "clear": true}

func (h *fileHandle) OnGet(key string) value.Value {
	if !fileHandleWords[key] {
		return h.unknown(key)
	}
	return value.NewCallerFunction(func(caller any, args []value.Value) value.Value {
		vm, _ := caller.(*runtime.VM)
		return h.call(vm, key, args)
	})
}

func (h *fileHandle) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (h *fileHandle) OnInvoke(method string, args ...value.Value) value.Value {
	return h.call(nil, method, args)
}

func (h *fileHandle) InvokeFrom(caller *runtime.VM, method string, args []value.Value) (value.Value, bool) {
	return h.call(caller, method, args), true
}

// spelling is the handle as a program writes it, for a message.
func (h *fileHandle) spelling() string {
	return fmt.Sprintf("file.open(%q)", h.path.String())
}

func (h *fileHandle) unknown(word string) value.Value {
	if word == "remove" {
		return value.InvalidFailure(value.InvalidArgumentCode, compiler.FileRemovedWord("remove"))
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.%s không có: handle có segment, name, path, read, text, bytes, json, list, info, exists, create, write, copy, move, delete, clear (KITWORK_FILE.md mục 3)", h.spelling(), word))
}

func (h *fileHandle) noArguments(word string, args []value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Nil}
	}
	if word == "name" {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.name() không nhận đối số; chọn con bằng segment(…), file.open(\"uploads/\").segment(\"a.png\") (KITWORK_FILE.md mục 17)", h.spelling()))
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.%s() không nhận đối số", h.spelling(), word))
}

func (h *fileHandle) call(caller *runtime.VM, method string, args []value.Value) value.Value {
	if !fileHandleWords[method] {
		return h.unknown(method)
	}
	if fileChanges[method] && caller != nil {
		if _, loading := loadingVMs.Load(caller); loading {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.%s ở cấp file: lúc nạp, file chỉ khai; file.open(…) ở cấp file thì được, còn %s gọi trong một handler, một job hay một lịch", h.spelling(), method, method))
		}
	}
	switch method {
	case "segment":
		return h.segment(args)
	case "copy":
		return h.copy(caller, args)
	case "move":
		return h.move(args)
	case "write":
		return h.write(args)
	}
	if failure := h.noArguments(method, args); failure.IsError {
		return failure
	}
	switch method {
	case "name":
		return value.New(h.path.Name())
	case "path":
		return value.New(h.path.String())
	case "read", "text", "bytes", "json":
		return h.read(method)
	case "list":
		return h.list()
	case "info":
		return h.info()
	case "exists":
		info, failed := h.zone().Stat(h.path)
		if failed != nil {
			return fileFailure(failed)
		}
		return value.New(info != nil)
	case "create":
		info, failed := h.zone().Create(h.path)
		if failed != nil {
			return fileFailure(failed)
		}
		return fileInfoValue(info)
	case "delete":
		removed, failed := h.zone().Delete(h.path)
		if failed != nil {
			return fileFailure(failed)
		}
		return value.New(removed)
	case "clear":
		removed, failed := h.zone().Clear(h.path, fileTreeLimit, fileStop(caller))
		if failed != nil {
			return fileFailure(failed)
		}
		return value.New(removed)
	}
	return h.unknown(method)
}

// segment(…) is the handle of a path under this folder: each argument one name (KITWORK_FILE.md mục 2),
// as segment() of storage reads them, so a value from outside never leaves the folder.
func (h *fileHandle) segment(args []value.Value) value.Value {
	names, failure := segmentWords(args, `file.open("uploads/").segment(id, "avatar.png")`, `dựng đường dẫn bằng từng đoạn, file.open("uploads/").segment(id, "avatar.png")`)
	if failure.IsError {
		return failure
	}
	path, failed := h.path.Segment(names)
	if failed != nil {
		return fileFailure(failed)
	}
	return value.Value{K: value.Proxy, V: &fileHandle{tenant: h.tenant, path: path}}
}

// zone is where the handle's path starts: the app's code for "app/…", its data for any other.
func (h *fileHandle) zone() *filehelper.Zone {
	return h.tenant.fileZone(h.path.Code)
}

// fileZone is the code zone or the data zone of t's app.
func (t *Tenant) fileZone(code bool) *filehelper.Zone {
	boundary := t.appBoundary
	if boundary == nil {
		boundary, _ = safepath.NewBoundary(t.resolveApp())
	}
	if code {
		return &filehelper.Zone{App: t.resolveApp(), Boundary: boundary, Code: true}
	}
	return &filehelper.Zone{App: t.resolveApp(), Folder: filepath.Join(".database", "files"), Boundary: boundary}
}

// read is read(), text(), bytes(), json() (mục 4): the content, decoded by the extension for read().
func (h *fileHandle) read(word string) value.Value {
	data, failed := h.zone().Read(h.path, fileReadLimit)
	if failed != nil {
		if failed.Code == value.NotFoundCode {
			failed.Message += h.codeHint(word)
		}
		return fileFailure(failed)
	}
	kind := filehelper.KindOf(h.path.Name())
	switch {
	case word == "bytes" || (word == "read" && kind == filehelper.Binary):
		return value.New(data)
	case word == "json" || (word == "read" && kind == filehelper.JSON):
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return value.InvalidFailure(value.InvalidInputCode, fmt.Sprintf("%s không phải JSON: %v", h.path.String(), err))
		}
		return value.New(decoded)
	}
	if !utf8.Valid(data) {
		return value.InvalidFailure(value.InvalidInputCode, fmt.Sprintf("%s không phải chữ UTF-8; byte đọc bằng file.bytes(%q)", h.path.String(), h.path.String()))
	}
	return value.New(string(data))
}

// codeHint is told after NOT_FOUND of a path of the data zone that the site's own folder has: until
// 22/10/2026 file.read read the site's folder, and its files are read through app/ now (mục 17). The
// site's file is looked at through the code zone, by its rules: a path it refuses (.env, .database/, a link
// out) is never hinted, so the hint neither points to a read that fails nor tells what the site holds.
func (h *fileHandle) codeHint(word string) string {
	if h.path.Code || h.path.Root() {
		return ""
	}
	site, err := filepath.Rel(h.tenant.resolveApp(), h.tenant.resolve())
	if err != nil || site == "." || strings.HasPrefix(site, "..") {
		return ""
	}
	names := append(strings.Split(filepath.ToSlash(site), "/"), h.path.Names...)
	candidate := filehelper.Path{Code: true, Names: names}
	if info, failed := h.tenant.fileZone(true).Stat(candidate); failed != nil || info == nil || info.Directory {
		return ""
	}
	return fmt.Sprintf("; file đi kèm code của site đọc qua tiền tố app/: file.%s(%q) (KITWORK_FILE.md mục 1)", word, candidate.String())
}

func (h *fileHandle) list() value.Value {
	entries, failed := h.zone().List(h.path)
	if failed != nil {
		return fileFailure(failed)
	}
	items := make([]value.Value, len(entries))
	for index := range entries {
		items[index] = fileInfoValue(&entries[index])
	}
	return value.New(items)
}

func (h *fileHandle) info() value.Value {
	info, failed := h.zone().Stat(h.path)
	if failed != nil {
		return fileFailure(failed)
	}
	if info == nil {
		return value.Value{K: value.Nil}
	}
	return fileInfoValue(info)
}

// write(dữ_liệu) makes or replaces the file with data that fits its extension (mục 5): a value of JSON for
// .json, a string for an extension of text, bytes or an upload for any other. Nothing is turned into
// another kind on the way: a "data:…" string written to .png is refused, not decoded.
func (h *fileHandle) write(args []value.Value) value.Value {
	if len(args) != 1 {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.write(dữ_liệu) nhận một giá trị: chuỗi, một giá trị JSON, bytes hay một upload theo đuôi của file", h.spelling()))
	}
	if h.path.Code {
		return fileFailure(&filehelper.Error{Code: value.DeniedCode, Message: fmt.Sprintf(`write() %q: code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`, h.path.String())})
	}
	if h.path.Directory || h.path.Root() {
		return value.InvalidFailure(value.ConflictCode, fmt.Sprintf(`%q là thư mục; write() ghi một file, đường dẫn không có "/" cuối: file.open("notes.txt").write(…)`, h.path.String()))
	}
	source, closeSource, failure := h.source(args[0])
	if failure.IsError {
		return failure
	}
	defer closeSource()
	info, failed := h.zone().Write(h.path, source)
	if failed != nil {
		return fileFailure(failed)
	}
	return fileInfoValue(info)
}

// source is what write() writes, read from data as the extension says.
func (h *fileHandle) source(data value.Value) (io.Reader, func(), value.Value) {
	none := func() {}
	written := h.path.String()
	switch filehelper.KindOf(h.path.Name()) {
	case filehelper.JSON:
		if data.K == value.Bytes || data.K == value.Proxy || data.K == value.Struct {
			return nil, none, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%q nhận một giá trị JSON (object, mảng, chuỗi, số, boolean, null), không nhận %s; bytes và upload ghi vào đuôi nhị phân hay không đuôi", written, fileKindName(data)))
		}
		if where := notJSON(data); where != "" {
			return nil, none, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%q nhận một giá trị viết được thành JSON; %s", written, where))
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, none, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%q: không viết được thành JSON (%v)", written, err))
		}
		return bytes.NewReader(encoded), none, value.Value{K: value.Nil}
	case filehelper.Text:
		if data.K != value.String {
			hint := ""
			if data.K == value.Map || data.K == value.Array {
				hint = "; object và mảng ghi vào .json, hay JSON.stringify(…) trước"
			}
			return nil, none, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%q nhận chuỗi (đuôi chữ), không nhận %s%s", written, fileKindName(data), hint))
		}
		if !utf8.ValidString(data.Text()) {
			// what text() would refuse to read back: a string of bytes from outside that are not UTF-8
			return nil, none, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%q nhận chữ UTF-8; chuỗi này có byte không phải UTF-8 (dữ liệu từ ngoài kiểm trước khi ghi, hay ghi bytes vào đuôi nhị phân)", written))
		}
		return strings.NewReader(data.Text()), none, value.Value{K: value.Nil}
	}
	switch {
	case data.K == value.Bytes:
		return bytes.NewReader(data.Bytes()), none, value.Value{K: value.Nil}
	case data.K == value.Struct:
		if upload, ok := data.V.(*fileUpload); ok {
			opened, err := upload.header.Open()
			if err != nil {
				return nil, none, value.InvalidFailure(value.InvalidInputCode, fmt.Sprintf("không đọc được upload %q của request", upload.field))
			}
			return opened, func() { opened.Close() }, value.Value{K: value.Nil}
		}
	case data.K == value.String && strings.HasPrefix(data.Text(), "data:"):
		return nil, none, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`%q nhận bytes hay một upload (đuôi nhị phân hay không đuôi); một chuỗi "data:…" không được giải mã ngầm, giải mã là việc tường minh của người viết (KITWORK_FILE.md mục 5)`, written))
	}
	return nil, none, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%q nhận bytes hay một upload (đuôi nhị phân hay không đuôi), không nhận %s; chữ ghi vào đuôi chữ (.txt, .md, .csv…), giá trị JSON vào .json", written, fileKindName(data)))
}

// fileKindName names the kind of a value for a message.
func fileKindName(data value.Value) string {
	switch data.V.(type) {
	case *fileUpload:
		return "một upload"
	case *fileHandle:
		return "một handle của file"
	}
	return value.KindName(data)
}

// notJSON is where in data a value lies that JSON cannot write, "" when there is none. The walk keeps its
// path as every walk over a value does (value.Path): a value that holds itself, or one nested past
// value.MaxDepth, ends it with that reason, never in Go's stack, which no recover() catches.
func notJSON(data value.Value) string {
	var path value.Path
	path.Root(data)
	var at []string
	return notJSONWalk(&path, data, &at)
}

// notJSONWalk is notJSON at data, held where at says (".key", "[0]"), the text of which is written only for
// a failure.
func notJSONWalk(path *value.Path, data value.Value, at *[]string) string {
	switch data.K {
	case value.Nil, value.Bool:
		return ""
	case value.String:
		if !utf8.ValidString(data.Text()) {
			return fmt.Sprintf("%s là chuỗi có byte không phải UTF-8", fileAt(*at))
		}
		return ""
	case value.Number:
		if math.IsNaN(data.N) || math.IsInf(data.N, 0) {
			return fmt.Sprintf("%s là %v, JSON không có số đó", fileAt(*at), data.N)
		}
		return ""
	case value.Array:
		for index, item := range data.Array() {
			if failed := path.Index(item, index); failed != nil {
				return failed.Error()
			}
			*at = append(*at, "["+strconv.Itoa(index)+"]")
			where := notJSONWalk(path, item, at)
			*at = (*at)[:len(*at)-1]
			path.Leave()
			if where != "" {
				return where
			}
		}
		return ""
	case value.Map:
		for key, item := range data.Map() {
			if failed := path.Key(item, key); failed != nil {
				return failed.Error()
			}
			*at = append(*at, "."+key)
			where := notJSONWalk(path, item, at)
			*at = (*at)[:len(*at)-1]
			path.Leave()
			if where != "" {
				return where
			}
		}
		return ""
	}
	return fmt.Sprintf("%s là %s", fileAt(*at), fileKindName(data))
}

func fileAt(at []string) string {
	if len(at) == 0 {
		return "giá trị"
	}
	return "giá trị ở " + strings.TrimPrefix(strings.Join(at, ""), ".")
}

// target is the destination of copy() and move(): a path of the data zone, or a handle.
func (h *fileHandle) target(word string, args []value.Value) (filehelper.Path, value.Value) {
	if len(args) == 1 {
		switch {
		case args[0].K == value.String:
			path, failed := filehelper.Parse(args[0].Text())
			if failed != nil {
				return filehelper.Path{}, fileFailure(failed)
			}
			return path, value.Value{K: value.Nil}
		case args[0].K == value.Proxy:
			if other, ok := args[0].V.(*fileHandle); ok {
				return other.path, value.Value{K: value.Nil}
			}
		}
	}
	return filehelper.Path{}, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`%s.%s(đích) nhận một đường dẫn trong vùng dữ liệu hay một handle: %s(%q)`, h.spelling(), word, word, "backup/"+h.path.Name()))
}

// copy(đích) copies the file or the folder (mục 8); from the code zone into the data zone too.
func (h *fileHandle) copy(caller *runtime.VM, args []value.Value) value.Value {
	target, failure := h.target("copy", args)
	if failure.IsError {
		return failure
	}
	data := h.tenant.fileZone(false)
	if target.Code {
		return fileFailure(&filehelper.Error{Code: value.DeniedCode, Message: fmt.Sprintf(`copy() tới %q: code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`, target.String())})
	}
	info, failed := data.Copy(h.zone(), h.path, target, fileTreeLimit, fileStop(caller))
	if failed != nil {
		return fileFailure(failed)
	}
	return fileInfoValue(info)
}

// move(đích) moves the file or the folder inside the data zone (mục 8); this handle keeps its path.
func (h *fileHandle) move(args []value.Value) value.Value {
	target, failure := h.target("move", args)
	if failure.IsError {
		return failure
	}
	if target.Code {
		return fileFailure(&filehelper.Error{Code: value.DeniedCode, Message: fmt.Sprintf(`move() tới %q: code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`, target.String())})
	}
	info, failed := h.tenant.fileZone(false).Move(h.path, target)
	if failed != nil {
		return fileFailure(failed)
	}
	return fileInfoValue(info)
}

// fileStop is closed when the request caller runs for is over.
func fileStop(caller *runtime.VM) <-chan struct{} {
	if caller == nil || caller.Context == nil {
		return nil
	}
	return caller.Context.Done()
}

// fileInfoValue is a FileInfo as a program reads it (mục 6).
func fileInfoValue(info *filehelper.Info) value.Value {
	entry := map[string]value.Value{
		"name":       value.New(info.Path.Name()),
		"path":       value.New(info.Path.String()),
		"type":       value.New("file"),
		"size":       value.New(float64(info.Size)),
		"extension":  value.Value{K: value.Nil},
		"createdAt":  value.Value{K: value.Nil},
		"modifiedAt": value.New(info.Modified.UTC().Format(time.RFC3339)),
	}
	if info.Directory {
		entry["type"], entry["size"] = value.New("directory"), value.Value{K: value.Nil}
	} else if extension := filehelper.Extension(info.Path.Name()); extension != "" {
		entry["extension"] = value.New(extension)
	}
	if !info.Created.IsZero() {
		entry["createdAt"] = value.New(info.Created.UTC().Format(time.RFC3339))
	}
	if info.Path.Root() {
		entry["name"] = value.New("")
	}
	return value.Value{K: value.Map, V: entry}
}

// fileFailure is a failure of utilities/file as a program reads it: PARTIAL carries error.progress, the
// number of entries done.
func fileFailure(failed *filehelper.Error) value.Value {
	if failed.Code == value.PartialCode {
		return value.PartialFailure(failed.Message, value.New(float64(failed.Done)))
	}
	return value.InvalidFailure(failed.Code, failed.Message)
}

// ── upload and answer (mục 9) ───────────────────────────────────────────────────────────────────────

// fileUpload is a file of the request's multipart body: request.file(tên). Its name, size and type are
// what the client said, never proof (mục 9): name is the last name of what it sent, never a path to write
// to. Its bytes stay where the multipart parse put them (a file of the system past a small part kept in
// memory) until write() copies them.
type fileUpload struct {
	field  string
	header *multipart.FileHeader
}

// Name is upload.name.
func (u *fileUpload) Name() value.Value { return value.New(u.header.Filename) }

// Size is upload.size, in bytes.
func (u *fileUpload) Size() value.Value { return value.New(float64(u.header.Size)) }

// Type is upload.type, the media type the client claimed.
func (u *fileUpload) Type() value.Value { return value.New(u.header.Header.Get("Content-Type")) }

// MissingMember is any other word of an upload.
func (u *fileUpload) MissingMember(name string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("upload.%s không có: upload có name, size, type; lưu bằng file.open(\"uploads/\").segment(id, \"a.png\").write(upload) (KITWORK_FILE.md mục 9)", name))
}

// serveFile answers with the file of handle, served from the disk (mục 9): Range, ETag and Last-Modified
// are the system's, the bytes never pass through the VM. A path out of the zone, one the code zone never
// reaches, a folder, a file that is not there: 404, which never tells which.
func serveFile(w http.ResponseWriter, request *http.Request, handle *fileHandle, response *Response) {
	file, stat, failed := handle.zone().Open(handle.path)
	if failed != nil {
		http.NotFound(w, request)
		return
	}
	defer file.Close()
	mediaType := response.mediaType("")
	if mediaType == "" {
		mediaType = mime.TypeByExtension(filehelper.Extension(handle.path.Name()))
	}
	if mediaType == "" {
		// no extension, or one without a type, is bytes (mục 4): never a type guessed from the content,
		// which would serve an upload that looks like HTML as a page of the site
		mediaType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, stat.ModTime().UnixNano(), stat.Size()))
	http.ServeContent(w, request, stat.Name(), stat.ModTime(), file)
}
