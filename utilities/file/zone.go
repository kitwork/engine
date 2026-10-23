package file

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kitwork/engine/utilities/safepath"
	"github.com/kitwork/engine/value"
)

// Zone is the folder the names of a Path start from (mục 1). Every operation opens an os.Root of the app's
// folder and, through it, of the zone's folder, so a name that leads out, a link included (a symlink, or
// on Windows a junction), is refused by the system at the moment the operation runs, the zone's own folder
// as much as any name under it.
type Zone struct {
	// App is the app's folder on the machine.
	App string
	// Folder is the zone's folder inside the app: .database/files for the data zone (made at the first
	// write), "" for the code zone, the app's folder itself.
	Folder string
	// Boundary is the app's folder, links resolved: the names the code zone never reaches are checked on
	// the path the disk holds, not only as written.
	Boundary *safepath.Boundary
	// Code is the code zone: read only, and .env, .env.* and .database/ are never reached through it.
	Code bool
}

// Info is what info() and list() tell of an entry (mục 6).
type Info struct {
	// Path is the entry as a program writes it again: a "/" last for a folder.
	Path      Path
	Directory bool
	Size      int64
	// Created is zero where the place keeps no time of creation (Linux, mục 11).
	Created  time.Time
	Modified time.Time
}

// temporaryPrefix begins the name of a file the engine writes before renaming it into place; list()
// never shows one, and a path never names one (checkName), so nothing a program writes is hidden.
const temporaryPrefix = ".kitwork-"

// temporaryName reports a name of the engine's files half written, whatever its case.
func temporaryName(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), temporaryPrefix)
}

// deniedName reports a name the code zone never reaches, whatever its case: .env, .env.*, .database.
func deniedName(name string) bool {
	lower := strings.ToLower(name)
	return lower == ".env" || strings.HasPrefix(lower, ".env.") || lower == ".database"
}

func osName(p Path) string {
	if len(p.Names) == 0 {
		return "."
	}
	return filepath.Join(p.Names...)
}

// readOnly is the failure of a change asked of the code zone (mục 1).
func readOnly(p Path, word string) *Error {
	return failf(value.DeniedCode, `%s %q: code của app chỉ đọc; dữ liệu ghi vào vùng dữ liệu, file.open("…") không có app/`, word, p.String())
}

// deniedRead is the failure of a path of the code zone that reaches what it never reaches.
func deniedRead(p Path) *Error {
	return failf(value.DeniedCode, `%q: .env, .env.* và .database/ không bao giờ đọc được qua app/; biến môi trường đọc bằng environment.KEY, dữ liệu của app ở file.open("…") không có app/`, p.String())
}

// denied reports a path of the code zone that reaches .env, .env.* or .database/: by its names as written,
// in any case, and by the names the disk holds once links are resolved (and, on Windows, a short name read
// as its long one), so neither a link nor another spelling of a name gets past it.
func (z *Zone) denied(p Path) bool {
	if !z.Code {
		return false
	}
	for _, name := range p.Names {
		if deniedName(name) {
			return true
		}
	}
	if z.Boundary == nil {
		return false
	}
	relative, inside, err := z.Boundary.Relative(filepath.Join(z.App, z.Folder, osName(p)))
	if err != nil || !inside {
		return true
	}
	for _, name := range strings.Split(filepath.ToSlash(relative), "/") {
		if deniedName(name) {
			return true
		}
	}
	return false
}

// open opens the zone's root through the app's: nil with no failure when the data zone has never been
// written (it holds nothing). create makes it first.
func (z *Zone) open(create bool) (*os.Root, *Error) {
	app, err := os.OpenRoot(z.App)
	if err != nil {
		return nil, failf(value.DatabaseCode, "không mở được thư mục của app: %s", systemReason(err))
	}
	if z.Folder == "" {
		return app, nil
	}
	defer app.Close() // the zone's root stays open on its own
	root, err := app.OpenRoot(z.Folder)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		if !create {
			return nil, nil
		}
		if err := app.MkdirAll(z.Folder, 0o755); err != nil {
			return nil, z.rootFailure(err)
		}
		root, err = app.OpenRoot(z.Folder)
	}
	if err != nil {
		return nil, z.rootFailure(err)
	}
	return root, nil
}

// rootFailure is the failure of reaching the zone's folder.
func (z *Zone) rootFailure(err error) *Error {
	if escapes(err) {
		return failf(value.DeniedCode, "vùng dữ liệu của app dẫn ra ngoài thư mục của app qua một liên kết; không đọc, không ghi ở đó (KITWORK_FILE.md mục 2)")
	}
	return failf(value.DatabaseCode, "không mở được vùng dữ liệu của app: %s", systemReason(err))
}

// failure is a failure of the system on p, told in the words of the closed set: NOT_FOUND, CONFLICT,
// DENIED (a link out of the zone, no right), DATABASE for any other, with the system's reason and never a
// path of the machine.
func failure(action string, p Path, err error) *Error {
	written := p.String()
	switch {
	case escapes(err):
		return failf(value.DeniedCode, "%s %q: đường dẫn dẫn ra ngoài vùng qua một liên kết", action, written)
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return failf(value.NotFoundCode, "không có file %q", written)
	case errors.Is(err, fs.ErrExist):
		return failf(value.ConflictCode, "%q đã có", written)
	case errors.Is(err, fs.ErrPermission):
		return failf(value.DeniedCode, "%s %q: hệ điều hành không cho (%s)", action, written, systemReason(err))
	}
	return failf(value.DatabaseCode, "%s %q hỏng: %s", action, written, systemReason(err))
}

// escapes reports the failure os.Root gives a name that leads out of it.
func escapes(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}

// systemReason is why the system failed, without the path it names.
func systemReason(err error) string {
	var path *fs.PathError
	var link *os.LinkError
	var call *os.SyscallError
	switch {
	case errors.As(err, &path):
		return path.Err.Error()
	case errors.As(err, &link):
		return link.Err.Error()
	case errors.As(err, &call):
		return call.Err.Error()
	}
	var reader interface{ ReadFailure() string }
	if errors.As(err, &reader) {
		return reader.ReadFailure()
	}
	return "lỗi của nơi lưu"
}

func (z *Zone) info(p Path, stat fs.FileInfo) *Info {
	return &Info{
		Path:      Path{Code: p.Code, Names: p.Names, Directory: stat.IsDir()},
		Directory: stat.IsDir(),
		Size:      stat.Size(),
		Created:   createdAt(stat),
		Modified:  stat.ModTime(),
	}
}

// notFolder is the failure of a path written as a folder ("images/") that is a file.
func notFolder(p Path) *Error {
	return failf(value.ConflictCode, "%q là file, không phải thư mục", p.String())
}

// ── reading ────────────────────────────────────────────────────────────────────────────────────────

// Stat is the entry at p, nil when there is none. A path written as a folder that is a file is CONFLICT;
// a path the code zone never reaches is DENIED, never "none" (mục 6).
func (z *Zone) Stat(p Path) (*Info, *Error) {
	if z.denied(p) {
		return nil, deniedRead(p)
	}
	root, failed := z.open(false)
	if failed != nil || root == nil {
		return nil, failed
	}
	defer root.Close()
	stat, err := root.Stat(osName(p))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil, nil
		}
		return nil, failure("đọc", p, err)
	}
	if p.Directory && !stat.IsDir() {
		return nil, notFolder(p)
	}
	return z.info(p, stat), nil
}

// TooLarge is the failure of a read past the ceiling of what a read brings into the VM's memory (mục 4).
func TooLarge(p Path, size, limit int64) *Error {
	return failf(value.TooLargeCode, "đọc %q vào bộ nhớ tối đa %d byte, file dài %d byte; trả file cho trình duyệt thì dùng context.file(file.open(%q))", p.String(), limit, size, p.String())
}

// Read is the content of the file at p, at most limit bytes: TOO_LARGE past it, without reading further.
func (z *Zone) Read(p Path, limit int64) ([]byte, *Error) {
	if z.denied(p) {
		return nil, deniedRead(p)
	}
	root, failed := z.open(false)
	if failed != nil {
		return nil, failed
	}
	if root == nil {
		return nil, failf(value.NotFoundCode, "không có file %q", p.String())
	}
	defer root.Close()
	file, err := root.Open(osName(p))
	if err != nil {
		return nil, failure("đọc", p, err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, failure("đọc", p, err)
	}
	switch {
	case stat.IsDir():
		folder := Path{Code: p.Code, Names: p.Names, Directory: true}
		return nil, failf(value.ConflictCode, "%q là thư mục, không phải file; con của nó đọc bằng file.list(%q)", folder.String(), folder.String())
	case p.Directory:
		return nil, notFolder(p)
	case !stat.Mode().IsRegular():
		return nil, failf(value.DeniedCode, "%q không phải file thường; kitwork/file chỉ đọc file và thư mục", p.String())
	case stat.Size() > limit:
		return nil, TooLarge(p, stat.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, failure("đọc", p, err)
	}
	if int64(len(data)) > limit {
		return nil, TooLarge(p, int64(len(data)), limit)
	}
	return data, nil
}

// Open opens the regular file at p to be served from the disk (context.file, mục 9): the caller reads it
// with the system's own copy and closes it. Anything else, a folder, a file the zone never reaches, a link
// out of it, fails.
func (z *Zone) Open(p Path) (*os.File, fs.FileInfo, *Error) {
	if p.Directory || p.Root() {
		return nil, nil, notFolder(p)
	}
	if z.denied(p) {
		return nil, nil, deniedRead(p)
	}
	root, failed := z.open(false)
	if failed != nil {
		return nil, nil, failed
	}
	if root == nil {
		return nil, nil, failf(value.NotFoundCode, "không có file %q", p.String())
	}
	defer root.Close() // the file opened stays open
	file, err := root.Open(osName(p))
	if err != nil {
		return nil, nil, failure("đọc", p, err)
	}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		file.Close()
		return nil, nil, failf(value.NotFoundCode, "không có file %q", p.String())
	}
	return file, stat, nil
}

// List is the entries right under the folder at p (mục 6): not the engine's files half written, and in the
// code zone not .env, .env.* or .database/. A link is listed as what it leads to; one that leads out of the
// zone, or nowhere, is not listed. They come by name, byte for byte: the spec promises no order, and the
// disk's own (a hash on ext4, by name on NTFS) would differ from one machine to the next.
func (z *Zone) List(p Path) ([]Info, *Error) {
	if z.denied(p) {
		return nil, deniedRead(p)
	}
	root, failed := z.open(false)
	if failed != nil {
		return nil, failed
	}
	if root == nil {
		return nil, failf(value.NotFoundCode, "không có thư mục %q", Path{Code: p.Code, Names: p.Names, Directory: true}.String())
	}
	defer root.Close()
	folder, err := root.Open(osName(p))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil, failf(value.NotFoundCode, "không có thư mục %q", Path{Code: p.Code, Names: p.Names, Directory: true}.String())
		}
		return nil, failure("liệt kê", p, err)
	}
	defer folder.Close()
	stat, err := folder.Stat()
	if err != nil {
		return nil, failure("liệt kê", p, err)
	}
	if !stat.IsDir() {
		return nil, failf(value.ConflictCode, "%q là file; list() liệt kê một thư mục", Path{Code: p.Code, Names: p.Names}.String())
	}
	entries, err := folder.ReadDir(-1)
	if err != nil {
		return nil, failure("liệt kê", p, err)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	list := make([]Info, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if temporaryName(name) || (z.Code && deniedName(name)) {
			continue
		}
		child := Path{Code: p.Code, Names: append(append([]string{}, p.Names...), name)}
		stat, err := root.Stat(osName(child))
		if err != nil {
			continue // a link that leads out of the zone, or nowhere
		}
		list = append(list, *z.info(child, stat))
	}
	return list, nil
}

// ── changing ───────────────────────────────────────────────────────────────────────────────────────

// parents makes the folders p is in, and refuses a name on the way that is a file.
func parents(root *os.Root, p Path) *Error {
	for index := 1; index < len(p.Names); index++ {
		folder := Path{Code: p.Code, Names: p.Names[:index], Directory: true}
		stat, err := root.Stat(osName(folder))
		switch {
		case err == nil && stat.IsDir():
			continue
		case err == nil:
			return notFolder(folder)
		case errors.Is(err, fs.ErrNotExist):
			if err := root.Mkdir(osName(folder), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return failure("tạo", folder, err)
			}
		default:
			return failure("tạo", folder, err)
		}
	}
	return nil
}

// temporary opens a new file beside the place it will be renamed to: in the folder of p.
func temporary(root *os.Root, p Path) (*os.File, string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, "", err
	}
	name := filepath.Join(osName(p.parent()), temporaryPrefix+hex.EncodeToString(suffix[:]))
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	return file, name, err
}

// fill writes source into the file made by temporary, to the disk, and closes it; on failure the file is
// gone.
func fill(root *os.Root, file *os.File, name string, source io.Reader) error {
	_, copyErr := io.Copy(file, source)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = root.Remove(name)
		return err
	}
	return nil
}

// Write makes or replaces the file at p with what source gives, and the folders it is in (mục 5). The
// bytes go to a file beside it, renamed over p once whole: a reader at the same time sees the old content
// or the new, never half of it, and a write that fails leaves the old content as it was.
func (z *Zone) Write(p Path, source io.Reader) (*Info, *Error) {
	if z.Code {
		return nil, readOnly(p, "write()")
	}
	if p.Root() || p.Directory {
		return nil, failf(value.ConflictCode, `%q là thư mục; write() ghi một file, đường dẫn không có "/" cuối: file.open("notes.txt").write(…)`, p.String())
	}
	root, failed := z.open(true)
	if failed != nil {
		return nil, failed
	}
	defer root.Close()
	if failed := parents(root, p); failed != nil {
		return nil, failed
	}
	if stat, err := root.Lstat(osName(p)); err == nil && stat.IsDir() {
		return nil, failf(value.ConflictCode, "%q là thư mục, không phải file; write() không ghi lên một thư mục", Path{Names: p.Names, Directory: true}.String())
	}
	file, name, err := temporary(root, p)
	if err != nil {
		return nil, failure("ghi", p, err)
	}
	if err := fill(root, file, name, source); err != nil {
		return nil, failure("ghi", p, err)
	}
	if err := root.Rename(name, osName(p)); err != nil {
		_ = root.Remove(name)
		return nil, failure("ghi", p, err)
	}
	stat, err := root.Stat(osName(p))
	if err != nil {
		return nil, failure("ghi", p, err)
	}
	return z.info(p, stat), nil
}

// Create makes the file or the folder p names, by its trailing "/", and the folders it is in (mục 5). One
// already there of the same kind is given back as it is, its content kept; one of the other kind is
// CONFLICT.
func (z *Zone) Create(p Path) (*Info, *Error) {
	if z.Code {
		return nil, readOnly(p, "create()")
	}
	root, failed := z.open(true)
	if failed != nil {
		return nil, failed
	}
	defer root.Close()
	if p.Root() {
		stat, err := root.Stat(".")
		if err != nil {
			return nil, failure("tạo", p, err)
		}
		return z.info(p, stat), nil
	}
	if info, failed := z.existing(root, p); info != nil || failed != nil {
		return info, failed
	}
	if failed := parents(root, p); failed != nil {
		return nil, failed
	}
	var err error
	if p.Directory {
		err = root.Mkdir(osName(p), 0o755)
	} else {
		var file *os.File
		if file, err = root.OpenFile(osName(p), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
			err = file.Close()
		}
	}
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, failure("tạo", p, err)
	}
	if info, failed := z.existing(root, p); info != nil || failed != nil {
		return info, failed
	}
	return nil, failf(value.DatabaseCode, "tạo %q hỏng: không thấy nó sau khi tạo", p.String())
}

// existing is the entry at p when there is one of the kind p names, CONFLICT when it is of the other kind,
// nothing when there is none.
func (z *Zone) existing(root *os.Root, p Path) (*Info, *Error) {
	stat, err := root.Stat(osName(p))
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return nil, nil
	case err != nil:
		return nil, failure("tạo", p, err)
	case p.Directory && !stat.IsDir():
		return nil, notFolder(p)
	case !p.Directory && stat.IsDir():
		return nil, failf(value.ConflictCode, `%q là thư mục; một thư mục viết có "/" cuối: %q`, p.String(), Path{Code: p.Code, Names: p.Names, Directory: true}.String())
	}
	return z.info(p, stat), nil
}

// Delete removes the file or the empty folder at p (mục 7): false when there is nothing there; a folder
// that holds anything is CONFLICT, never emptied on the way. The root of the zone is DENIED.
func (z *Zone) Delete(p Path) (bool, *Error) {
	if z.Code {
		return false, readOnly(p, "delete()")
	}
	if p.Root() {
		return false, failf(value.DeniedCode, "gốc vùng dữ liệu của app không xoá được")
	}
	root, failed := z.open(false)
	if failed != nil || root == nil {
		return false, failed
	}
	defer root.Close()
	stat, err := root.Lstat(osName(p))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		return false, failure("xoá", p, err)
	}
	if p.Directory && !stat.IsDir() {
		return false, notFolder(p)
	}
	if stat.IsDir() {
		count, failed := entries(root, p)
		if failed != nil {
			return false, failed
		}
		if count > 0 {
			folder := Path{Names: p.Names, Directory: true}
			return false, failf(value.ConflictCode, "thư mục %q còn %d mục; delete() không xoá nội dung, file.open(%q).clear() trước rồi delete()", folder.String(), count, folder.String())
		}
	}
	if err := root.Remove(osName(p)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, failure("xoá", p, err)
	}
	return true, nil
}

// entries counts what the folder at p holds.
func entries(root *os.Root, p Path) (int, *Error) {
	folder, err := root.Open(osName(p))
	if err != nil {
		return 0, failure("xoá", p, err)
	}
	defer folder.Close()
	names, err := folder.ReadDir(-1)
	if err != nil {
		return 0, failure("xoá", p, err)
	}
	return len(names), nil
}

// partial is the failure of a walk of a tree that stopped after done entries (mục 7, mục 8): past the
// ceiling of one call, at the end of the request, or at a failure of the disk. then, when not "", is what
// the program does next, told after why.
func partial(word string, p Path, done, limit int, stop <-chan struct{}, cause *Error, then string) *Error {
	var why string
	switch {
	case cause != nil:
		why = cause.Message
	case stopped(stop):
		why = "request đã xong"
	default:
		why = fmt.Sprintf("trần %d mục một lần", limit)
	}
	if then != "" {
		why += "; " + then
	}
	return &Error{Code: value.PartialCode, Message: fmt.Sprintf("%s của %q dừng sau %d mục: %s", word, p.String(), done, why), Done: done}
}

func stopped(stop <-chan struct{}) bool {
	if stop == nil {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// Clear removes everything inside the folder at p and keeps the folder (mục 7): how many entries it
// removed, nothing when there is no folder. A link inside is removed as a link, never followed. It stops
// past limit entries, or when stop closes: PARTIAL with what it removed; what is removed stays removed.
func (z *Zone) Clear(p Path, limit int, stop <-chan struct{}) (int, *Error) {
	if z.Code {
		return 0, readOnly(p, "clear()")
	}
	if p.Root() {
		return 0, failf(value.DeniedCode, "gốc vùng dữ liệu của app không làm rỗng được; clear() một thư mục trong nó")
	}
	root, failed := z.open(false)
	if failed != nil || root == nil {
		return 0, failed
	}
	defer root.Close()
	stat, err := root.Stat(osName(p))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return 0, nil
		}
		return 0, failure("clear()", p, err)
	}
	if !stat.IsDir() {
		return 0, failf(value.ConflictCode, "%q là file; clear() làm rỗng một thư mục", Path{Names: p.Names}.String())
	}
	removed := 0
	if failed := clearTree(root, p, &removed, limit, stop); failed != nil {
		then := ""
		if failed == errStop {
			// what is removed stays removed: a second clear() goes on from there
			failed, then = nil, "gọi lại để làm tiếp"
		}
		return removed, partial("clear()", Path{Names: p.Names, Directory: true}, removed, limit, stop, failed, then)
	}
	return removed, nil
}

// errStop is a walk that reached its ceiling or its end of request: the caller tells which.
var errStop = &Error{Code: value.PartialCode}

func clearTree(root *os.Root, p Path, removed *int, limit int, stop <-chan struct{}) *Error {
	folder, err := root.Open(osName(p))
	if err != nil {
		return failure("clear()", p, err)
	}
	list, err := folder.ReadDir(-1)
	folder.Close()
	if err != nil {
		return failure("clear()", p, err)
	}
	for _, entry := range list {
		child := p.child(entry.Name(), entry.IsDir())
		if entry.IsDir() {
			if failed := clearTree(root, child, removed, limit, stop); failed != nil {
				return failed
			}
		}
		if *removed >= limit || stopped(stop) {
			return errStop
		}
		if err := root.Remove(osName(child)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return failure("xoá", child, err)
		}
		*removed++
	}
	return nil
}
