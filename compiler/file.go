package compiler

import "fmt"

// kitwork/file (KITWORK_FILE.md): one module, one import, import * as file from "kitwork/file"; reading is
// called directly (file.json(path), file.list("uploads/")), a change opens a handle first
// (file.open(path).write(data)). The words of before (file.write, file.save, file.base64), the changes at
// the module's level the spec never has (file.create, file.copy, file.move, file.delete, file.remove),
// file.directory, file.folder, file.path, any other word the module has not, and a word imported by name
// (import { json } from "kitwork/file") are refused here, where they are written, in the words the module
// uses when it runs (work/file.go) for a name the compiler could not follow. file.env passes: it is the
// manifest's (application.work, and import { file } from "kitwork" of an app.kitwork.js before it), and the
// module refuses it in an app.

// fileWords are the words of kitwork/file a program calls; env is the manifest's.
var fileWords = map[string]bool{
	"open": true, "read": true, "text": true, "bytes": true, "json": true, "list": true, "info": true, "exists": true,
	"env": true,
}

// FileImportOne is the error of a word imported by name off kitwork/file (mục 0).
const FileImportOne = `kitwork/file import một cách: import * as file from "kitwork/file", rồi file.json(…), file.open(…) (KITWORK_FILE.md mục 0)`

// FileEnvInApp is the error of file.env in an app's code: env is read there by name, the manifest alone
// names files of env (KITWORK_ENVIRONMENT.md mục 7).
const FileEnvInApp = `file.env chỉ có trong application.work (manifest); code trong app đọc biến môi trường bằng environment.KEY của import { environment } from "kitwork" (KITWORK_ENVIRONMENT.md mục 7)`

// FileRemovedWord is the error of a word of kitwork/file that the spec replaced or never has at the
// module's level, "" for any other word.
func FileRemovedWord(word string) string {
	switch word {
	case "write":
		return `file.write(path, data) đã bỏ: đổi thì open() trước, file.open(path).write(data) (KITWORK_FILE.md mục 3, 17)`
	case "save":
		return `file.save(path, data) đã bỏ: file.open(path).write(data) (KITWORK_FILE.md mục 17)`
	case "base64":
		return `file.base64(path) đã bỏ: ảnh trả cho trình duyệt bằng context.file(path), byte đọc bằng file.bytes(path) (KITWORK_FILE.md mục 17)`
	case "create":
		return `file.create(path) không có: đổi thì open() trước, file.open(path).create() (KITWORK_FILE.md mục 3)`
	case "copy":
		return `file.copy(a, b) không có: đổi thì open() trước, file.open(a).copy(b) (KITWORK_FILE.md mục 3)`
	case "move":
		return `file.move(a, b) không có: đổi thì open() trước, file.open(a).move(b) (KITWORK_FILE.md mục 3)`
	case "delete":
		return `file.delete(path) không có: đổi thì open() trước, file.open(path).delete() (KITWORK_FILE.md mục 3)`
	case "remove":
		return `remove() không có: xoá cả cây viết hai bước, file.open("tmp/").clear() rồi file.open("tmp/").delete() (KITWORK_FILE.md mục 7)`
	case "directory", "folder":
		return fmt.Sprintf(`file.%s() không có: thư mục là đường dẫn có "/" cuối, file.open("uploads/"), file.list("uploads/") (KITWORK_FILE.md mục 2)`, word)
	case "path":
		return `file.path() không có: đường dẫn của một handle là file.open("notes.txt").path() (KITWORK_FILE.md mục 2)`
	}
	return ""
}

// FileUnknownWord is the error of a word kitwork/file has not.
func FileUnknownWord(word string) string {
	if removed := FileRemovedWord(word); removed != "" {
		return removed
	}
	return fmt.Sprintf(`file.%s không có: kitwork/file có open, read, text, bytes, json, list, info, exists; đổi thì open() trước, file.open(path).write(data) (KITWORK_FILE.md mục 3)`, word)
}

// markFile marks a declared variable as the module kitwork/file itself (import * as file from
// "kitwork/file", import { file } from "kitwork").
func (r *resolver) markFile(node *VarStatement, name *Identifier, declared *binding) {
	if declared == nil || node.Pattern != nil {
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok {
		declared.fileModule = (node.DestructMode == DestructNone && module == "file") ||
			(node.DestructMode == DestructObject && module == "" && name.Value == "file")
	}
}

// fileDeclaration refuses a word imported by name off kitwork/file (import { json } from "kitwork/file",
// import { write as w } from "kitwork/file"), and a word the module has not taken apart from it
// (const { write } = file).
func (r *resolver) fileDeclaration(node *VarStatement) {
	if node.Pattern != nil {
		namespace, isName := node.Value.(*Identifier)
		if !isName || node.Pattern.Array {
			return
		}
		if found := r.lookup(namespace.Value); found == nil || !found.fileModule {
			return
		}
		for _, element := range node.Pattern.Elements {
			if !fileWords[element.Key] {
				r.fail(fmt.Errorf("%s: %s", r.where(element.KeyToken), FileUnknownWord(element.Key)))
				return
			}
		}
		return
	}
	if module, ok := kitworkModulePath(node.Value); ok && module == "file" && node.DestructMode == DestructObject && len(node.Names) > 0 {
		r.fail(fmt.Errorf("%s: %s", r.where(node.Names[0].Token), FileImportOne))
		return
	}
	if member, isMember := node.Value.(*MemberExpression); isMember && member.Property != nil {
		if module, ok := kitworkModulePath(member.Object); ok && module == "file" {
			r.fail(fmt.Errorf("%s: %s", r.where(member.Property.Token), FileImportOne))
		}
	}
}

// fileMember refuses, where it is written, a word read off kitwork/file that it has not (file.write,
// file.base64, file.delete).
func (r *resolver) fileMember(object Expression, property *Identifier) bool {
	if property == nil || fileWords[property.Value] {
		return false
	}
	isModule := false
	if module, ok := kitworkModulePath(object); ok {
		isModule = module == "file"
	} else if name, isName := object.(*Identifier); isName {
		found := r.lookup(name.Value)
		isModule = found != nil && found.fileModule
	}
	if !isModule {
		return false
	}
	r.fail(fmt.Errorf("%s: %s", r.where(property.Token), FileUnknownWord(property.Value)))
	return true
}
