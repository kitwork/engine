package file

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/kitwork/engine/value"
)

// Copy and move never replace what is there (mục 8). The rename of the system replaces its target on
// every system, so a file is placed with a hard link, which fails when the target exists, and the name it
// was written under is removed after. Where the disk has no hard links, the target is looked at first and
// then renamed onto: between the two another write could land, which the spec leaves to a later version.
// A folder moves by one rename after the same look.

// kinds refuses a source and a target written for different kinds: a file goes to a file name, a folder
// to a name with a trailing "/".
func kinds(word string, source Path, folder bool, target Path) *Error {
	switch {
	case folder && !target.Directory:
		return failf(value.ConflictCode, `%s: %q là thư mục; đích của một thư mục viết có "/" cuối: %q`, word, Path{Code: source.Code, Names: source.Names, Directory: true}.String(), Path{Names: target.Names, Directory: true}.String())
	case !folder && source.Directory:
		return notFolder(source)
	case !folder && target.Directory:
		return failf(value.ConflictCode, `%s: %q là file; đích của nó là tên một file, không có "/" cuối: %q`, word, source.String(), Path{Names: append(append([]string{}, target.Names...), source.Name())}.String())
	}
	return nil
}

// taken is the failure of a target that is already there.
func taken(word string, target Path) *Error {
	return failf(value.ConflictCode, "%s: %q đã có; %s không ghi đè, muốn thay thì delete() đích trước", word, target.String(), word)
}

// place puts the file written under temporary at target, refusing a target that is there.
func place(root *os.Root, temporary string, target Path, word string) *Error {
	err := root.Link(temporary, osName(target))
	if err == nil {
		_ = root.Remove(temporary)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		_ = root.Remove(temporary)
		return taken(word, target)
	}
	// no hard links on this disk: look, then rename
	if _, statErr := root.Lstat(osName(target)); statErr == nil {
		_ = root.Remove(temporary)
		return taken(word, target)
	}
	if err := root.Rename(temporary, osName(target)); err != nil {
		_ = root.Remove(temporary)
		return failure(word, target, err)
	}
	return nil
}

// Copy copies the file or the folder source of the zone from to target in z, which is the data zone (mục
// 8): the folders target is in are made; a target already there is CONFLICT; a folder is copied whole, at
// most limit entries, past which, at the end of the request, or at a failure of the disk on the way, it is
// PARTIAL with what was copied. Copying out of the code zone is how a file that comes with the code
// becomes data; what the code zone never reaches is never copied.
func (z *Zone) Copy(from *Zone, source, target Path, limit int, stop <-chan struct{}) (*Info, *Error) {
	if z.Code {
		return nil, readOnly(target, "copy() tới")
	}
	if from.denied(source) {
		return nil, deniedRead(source)
	}
	if !from.Code && target.Within(source) {
		return nil, failf(value.InvalidArgumentCode, "copy(): chép %q vào trong chính nó (%q)", source.String(), target.String())
	}
	sourceRoot, failed := from.open(false)
	if failed != nil {
		return nil, failed
	}
	if sourceRoot == nil {
		return nil, failf(value.NotFoundCode, "không có file %q", source.String())
	}
	defer sourceRoot.Close()
	stat, err := sourceRoot.Stat(osName(source))
	if err != nil {
		return nil, failure("chép", source, err)
	}
	if failed := kinds("copy()", source, stat.IsDir(), target); failed != nil {
		return nil, failed
	}
	targetRoot, failed := z.open(true)
	if failed != nil {
		return nil, failed
	}
	defer targetRoot.Close()
	if target.Root() {
		return nil, taken("copy()", target)
	}
	if _, err := targetRoot.Lstat(osName(target)); err == nil {
		return nil, taken("copy()", target)
	}
	if failed := parents(targetRoot, target); failed != nil {
		return nil, failed
	}
	if stat.IsDir() {
		done := 0
		if failed := copyTree(from, sourceRoot, source, targetRoot, target, &done, limit, stop); failed != nil {
			if failed == errStop {
				failed = nil
			}
			// what was copied stays in the target, where a second copy() finds it: CONFLICT, never a
			// copy that goes on from there
			then := ""
			if done > 0 {
				half := Path{Names: target.Names, Directory: true}.String()
				then = fmt.Sprintf("đích %q còn dở và copy() lại sẽ gặp CONFLICT: file.open(%q).clear() rồi delete() rồi chép lại", half, half)
			}
			return nil, partial("copy()", Path{Code: source.Code, Names: source.Names, Directory: true}, done, limit, stop, failed, then)
		}
	} else if failed := copyFile(sourceRoot, source, targetRoot, target); failed != nil {
		return nil, failed
	}
	stat, err = targetRoot.Stat(osName(target))
	if err != nil {
		return nil, failure("chép", target, err)
	}
	return z.info(target, stat), nil
}

// copyFile copies one regular file into a new file at target.
func copyFile(sourceRoot *os.Root, source Path, targetRoot *os.Root, target Path) *Error {
	in, err := sourceRoot.Open(osName(source))
	if err != nil {
		return failure("chép", source, err)
	}
	defer in.Close()
	if stat, err := in.Stat(); err != nil {
		return failure("chép", source, err)
	} else if !stat.Mode().IsRegular() {
		return failf(value.DeniedCode, "%q không phải file thường; kitwork/file chỉ chép file và thư mục", source.String())
	}
	file, name, err := temporary(targetRoot, target)
	if err != nil {
		return failure("chép", target, err)
	}
	if err := fill(targetRoot, file, name, in); err != nil {
		return failure("chép", target, err)
	}
	return place(targetRoot, name, target, "copy()")
}

// copyTree copies the folder source to the new folder target, counting each entry made in done.
func copyTree(from *Zone, sourceRoot *os.Root, source Path, targetRoot *os.Root, target Path, done *int, limit int, stop <-chan struct{}) *Error {
	if *done >= limit || stopped(stop) {
		return errStop
	}
	if err := targetRoot.Mkdir(osName(target), 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return taken("copy()", target)
		}
		return failure("chép", target, err)
	}
	*done++
	folder, err := sourceRoot.Open(osName(source))
	if err != nil {
		return failure("chép", source, err)
	}
	list, err := folder.ReadDir(-1)
	folder.Close()
	if err != nil {
		return failure("chép", source, err)
	}
	for _, entry := range list {
		name := entry.Name()
		if temporaryName(name) || (from.Code && deniedName(name)) {
			continue
		}
		child := source.child(name, false)
		stat, err := sourceRoot.Stat(osName(child))
		if err != nil {
			continue // a link that leads out of the zone, or nowhere: never followed
		}
		if from.Code && from.denied(child) {
			continue
		}
		if stat.IsDir() {
			if failed := copyTree(from, sourceRoot, source.child(name, true), targetRoot, target.child(name, true), done, limit, stop); failed != nil {
				return failed
			}
			continue
		}
		if *done >= limit || stopped(stop) {
			return errStop
		}
		if failed := copyFile(sourceRoot, child, targetRoot, target.child(name, false)); failed != nil {
			return failed
		}
		*done++
	}
	return nil
}

// Move moves the file or the folder source to target, both in z, the data zone (mục 8): the folders
// target is in are made, a target already there is CONFLICT, and the source's handle keeps its path.
func (z *Zone) Move(source, target Path) (*Info, *Error) {
	if z.Code || source.Code {
		return nil, readOnly(source, "move()")
	}
	if source.Root() {
		return nil, failf(value.DeniedCode, "gốc vùng dữ liệu của app không dời được")
	}
	if target.Within(source) {
		return nil, failf(value.InvalidArgumentCode, "move(): dời %q vào trong chính nó (%q)", source.String(), target.String())
	}
	root, failed := z.open(false)
	if failed != nil {
		return nil, failed
	}
	if root == nil {
		return nil, failf(value.NotFoundCode, "không có file %q", source.String())
	}
	defer root.Close()
	stat, err := root.Stat(osName(source))
	if err != nil {
		return nil, failure("dời", source, err)
	}
	if failed := kinds("move()", source, stat.IsDir(), target); failed != nil {
		return nil, failed
	}
	if target.Root() {
		return nil, taken("move()", target)
	}
	if _, err := root.Lstat(osName(target)); err == nil {
		return nil, taken("move()", target)
	}
	if failed := parents(root, target); failed != nil {
		return nil, failed
	}
	if stat.IsDir() {
		if err := root.Rename(osName(source), osName(target)); err != nil {
			return nil, failure("dời", source, err)
		}
	} else {
		switch err := root.Link(osName(source), osName(target)); {
		case err == nil:
			if err := root.Remove(osName(source)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, failure("dời", source, err)
			}
		case errors.Is(err, fs.ErrExist):
			return nil, taken("move()", target)
		case errors.Is(err, syscall.ENOTDIR) || errors.Is(err, fs.ErrNotExist):
			return nil, failure("dời", source, err)
		default:
			// no hard links on this disk: the target was looked at above
			if err := root.Rename(osName(source), osName(target)); err != nil {
				return nil, failure("dời", source, err)
			}
		}
	}
	stat, err = root.Stat(osName(target))
	if err != nil {
		return nil, failure("dời", target, err)
	}
	return z.info(target, stat), nil
}
