// Package file is the machinery behind kitwork/file (docs/KITWORK_FILE.md): a path as a program writes it,
// the two zones a path starts from (the app's data, .database/files/, and its code, read only, written
// "app/…"), and every operation on them through an os.Root of the zone, so a name or a link that leads out
// of the zone is refused by the system, not by comparing strings. It knows nothing of the VM; the module
// the program sees is the work package's. Every failure is an *Error with a code of the closed set and a
// message that names the path as the program wrote it, never the machine's.
package file

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/value"
)

// Error is a failure of kitwork/file: Code is one of the closed set (value.*Code), Message names the path
// as written. Done is what a PARTIAL failure did before it stopped (the entries clear() removed, the
// entries copy() or move() of a folder carried).
type Error struct {
	Code    string
	Message string
	Done    int
}

func (e *Error) Error() string { return e.Message }

// FailureCode is the code value.FailureOfError reads.
func (e *Error) FailureCode() string { return e.Code }

func failf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// codePrefix is the code zone's first name: "app/shop.com/data/menu.json" is shop.com/data/menu.json of
// the app's folder, the word an import of the app's own files uses.
const codePrefix = "app"

// Path is a path of kitwork/file (mục 2): Names from the root of its zone, Code for the code zone (written
// "app/…"), Directory when it was written with a trailing "/", the writer's word that it is a folder.
type Path struct {
	Code      bool
	Names     []string
	Directory bool
}

// Parse reads a path as written. Every rule is the writer's, checked before anything touches the disk: no
// leading "/" (it reads like an address of the site, which it is not), "/" between names and never "\",
// no empty name, no "." or "..", no control character, and no character one system refuses in a name, so
// a path works the same on every machine the app runs on. "app" first is the code zone, and only in that
// spelling: "App/…" is refused, not a folder of the data zone. A name never begins with the engine's
// prefix of a file half written.
func Parse(written string) (Path, *Error) {
	if written == "" {
		return Path{}, failf(value.InvalidArgumentCode, `đường dẫn file không rỗng: file.json("settings.json"), file.list("uploads/")`)
	}
	if strings.HasPrefix(written, "/") {
		return Path{}, failf(value.InvalidArgumentCode, `đường dẫn file viết không có "/" đầu: %q, không phải %q (đường dẫn tính từ gốc vùng, không phải địa chỉ của site)`, strings.TrimLeft(written, "/"), written)
	}
	if strings.Contains(written, `\`) {
		return Path{}, failf(value.InvalidArgumentCode, `đường dẫn file %q: giữa các tên là "/", không bao giờ "\"`, written)
	}
	path := Path{Directory: strings.HasSuffix(written, "/")}
	names := strings.Split(strings.TrimSuffix(written, "/"), "/")
	switch {
	case names[0] == codePrefix:
		path.Code, names = true, names[1:]
		if len(names) == 0 {
			path.Directory = true // "app" and "app/": the app's folder
		}
	case strings.EqualFold(names[0], codePrefix):
		// "App/…" would be a folder of the data zone that a disk without case is "app/" itself (mục 1)
		rest := written[len(names[0]):]
		if rest == "" {
			rest = "/"
		}
		return Path{}, failf(value.InvalidArgumentCode, `đường dẫn file %q: app là tên dành riêng ở đầu đường dẫn, vùng dữ liệu không có thư mục gốc tên đó; code của app viết chữ thường, "app%s"`, written, rest)
	}
	for _, name := range names {
		if err := checkName(written, name); err != nil {
			return Path{}, err
		}
	}
	path.Names = names
	return path, nil
}

// checkName refuses a name of written no zone may hold.
func checkName(written, name string) *Error {
	switch {
	case name == "":
		return failf(value.InvalidArgumentCode, `đường dẫn file %q có một tên rỗng ("a//b"): mỗi đoạn giữa hai "/" là một tên`, written)
	case name == "." || name == "..":
		return failf(value.InvalidArgumentCode, `đường dẫn file %q có %q: đường dẫn tính từ gốc vùng, không đi lên hay đứng yên`, written, name)
	case strings.Contains(name, `\`):
		// a separator on Windows: a name of segment(…) with one would climb out of its folder there
		return failf(value.InvalidArgumentCode, `đường dẫn file %q: giữa các tên là "/", không bao giờ "\"`, written)
	case temporaryName(name):
		return failf(value.InvalidArgumentCode, `đường dẫn file %q: tên bắt đầu bằng %q dành cho file engine đang ghi dở (list() không hiện chúng); chọn tên khác`, written, temporaryPrefix)
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return failf(value.InvalidArgumentCode, "đường dẫn file %q có ký tự điều khiển", written)
		}
		if strings.ContainsRune(`<>:"|?*`, character) {
			return failf(value.InvalidArgumentCode, `đường dẫn file %q có %q: tên file không chứa < > : " | ? *, để app chạy như nhau trên mọi hệ điều hành (không có ổ đĩa, không có luồng phụ)`, written, string(character))
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return failf(value.InvalidArgumentCode, `đường dẫn file %q: tên %q không kết thúc bằng dấu chấm hay khoảng trắng (Windows bỏ chúng, hai tên thành một)`, written, name)
	}
	if reservedName(name) {
		return failf(value.InvalidArgumentCode, `đường dẫn file %q: %q là tên thiết bị của Windows, không dùng làm tên file`, written, name)
	}
	return nil
}

// reservedName reports a device name of Windows (CON, NUL, COM1…), with or without an extension: opened
// as a file there, it is the device.
func reservedName(name string) bool {
	base := strings.ToUpper(name)
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	base = strings.TrimRight(base, " ")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		last := base[3]
		return last >= '0' && last <= '9'
	}
	return false
}

// Segment is the path with names added after its own (segment(…) of a handle, mục 2): each one name, a
// "/" only at its end, which makes the last a folder. Only a folder has names under it.
func (p Path) Segment(names []string) (Path, *Error) {
	if !p.Directory {
		return Path{}, failf(value.InvalidArgumentCode, `segment(…) chỉ có trên thư mục, đường dẫn có "/" cuối: %q là file; chọn con của một thư mục, file.open("uploads/").segment("a.png")`, p.String())
	}
	joined := Path{Code: p.Code, Names: append(make([]string, 0, len(p.Names)+len(names)), p.Names...)}
	for index, name := range names {
		directory := strings.HasSuffix(name, "/")
		bare := strings.TrimSuffix(name, "/")
		if strings.Contains(bare, "/") {
			return Path{}, failf(value.InvalidArgumentCode, `segment(…) nhận một tên mỗi đoạn, đoạn thứ %d là %q: có "/" ở giữa; nhiều tên thì segment("a", "b")`, index+1, name)
		}
		if err := checkName(name, bare); err != nil {
			return Path{}, failf(value.InvalidArgumentCode, "segment(…), đoạn thứ %d: %s", index+1, err.Message)
		}
		joined.Names = append(joined.Names, bare)
		joined.Directory = directory
	}
	return joined, nil
}

// String is the path as a program writes it: "app/" first in the code zone, a "/" last for a folder.
func (p Path) String() string {
	names := strings.Join(p.Names, "/")
	if p.Code {
		if names == "" {
			return codePrefix + "/"
		}
		names = codePrefix + "/" + names
	}
	if p.Directory && names != "" && !strings.HasSuffix(names, "/") {
		names += "/"
	}
	return names
}

// Name is the last name, "" for the root of a zone.
func (p Path) Name() string {
	if len(p.Names) == 0 {
		return ""
	}
	return p.Names[len(p.Names)-1]
}

// Root reports the root of a zone itself.
func (p Path) Root() bool { return len(p.Names) == 0 }

// Within reports whether p is other or a path under it, in the same zone.
func (p Path) Within(other Path) bool {
	if p.Code != other.Code || len(p.Names) < len(other.Names) {
		return false
	}
	for index, name := range other.Names {
		if !strings.EqualFold(p.Names[index], name) {
			return false
		}
	}
	return true
}

// parent is the folder p is in.
func (p Path) parent() Path {
	if len(p.Names) == 0 {
		return p
	}
	return Path{Code: p.Code, Names: p.Names[:len(p.Names)-1], Directory: true}
}

// child is the path of the entry name under the folder p.
func (p Path) child(name string, directory bool) Path {
	return Path{Code: p.Code, Names: append(append([]string{}, p.Names...), name), Directory: directory}
}
