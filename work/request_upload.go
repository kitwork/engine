package work

// request.file(tên) and request.files(tên) (KITWORK_FILE.md mục 9, 22/10/2026): the files of a multipart
// body, each an upload { name, size, type } that file.open(…).write(upload) copies to the app's data
// without its bytes entering the VM. The body is parsed once, as Go's multipart parse does with a small
// part kept in memory: past it, each file goes to a file of the system, which the server removes when the
// request ends. The host's ceiling (MaxBodyBytes, app.web({ maxBody })) bounds the body; the route's own
// ceilings for uploads (size, count, media types) are still open (KITWORK_ROUTER.md mục 5).
//
// request.form(tên) read after request.file takes the fields of that parse, counting only their bytes
// against its 1 MiB: a file beside them is the upload's, not the form's. Read before it, request.form
// reads the body as it always has, file and all, under its 1 MiB.

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/kitwork/engine/value"
)

// uploadMemory is how much of a multipart body's files the parse keeps in memory.
const uploadMemory = 64 << 10

// removedSaveFile is the failure of request.saveFile and context.saveFile, which saved an upload into the
// app's own folder until 22/10/2026.
const removedSaveFile = `request.saveFile(tên, đích) đã bỏ: lưu một upload bằng file.open("uploads/").segment(id, "avatar.png").write(request.file("avatar")), vào vùng dữ liệu của app (KITWORK_FILE.md mục 9, 17)`

// SaveFile is request.saveFile, removed: reading it fails with what to write instead.
func (r *Request) SaveFile() value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, removedSaveFile)
}

// SaveFile is context.saveFile, removed as the request's.
func (c *Context) SaveFile() value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, removedSaveFile)
}

// File is request.file(tên): the first upload of the field, null when the body has none.
func (r *Request) File(name string) value.Value {
	uploads, failure := r.uploadsOf(name)
	if failure.IsError {
		return failure
	}
	if len(uploads) == 0 {
		return value.Value{K: value.Nil}
	}
	return uploads[0]
}

// Files is request.files(tên): every upload of the field, [] when the body has none.
func (r *Request) Files(name string) value.Value {
	uploads, failure := r.uploadsOf(name)
	if failure.IsError {
		return failure
	}
	if uploads == nil {
		uploads = []value.Value{}
	}
	return value.New(uploads)
}

// uploadsOf is the uploads of the field name. A failure of the host is told, never taken for "no file was
// sent": a body past the host's ceiling, one of more parts than Go's parse reads (both 413), a file of the
// system the parse could not write (the temp folder full or gone, DATABASE). Only a body that is not
// well-formed multipart, a client that went away midway among them, has no upload.
func (r *Request) uploadsOf(name string) ([]value.Value, value.Value) {
	form, err := r.uploads()
	if err != nil {
		var system *fs.PathError
		switch {
		case errors.Is(err, errBodyTooLarge):
			return nil, value.InvalidFailure(value.InvalidInputCode, fmt.Sprintf("thân request vượt trần %d byte của host (app.web({ maxBody })); upload không lớn hơn trần đó", MaxBodyBytes))
		case errors.Is(err, multipart.ErrMessageTooLarge):
			return nil, value.InvalidFailure(value.InvalidInputCode, "thân multipart vượt giới hạn của bộ đọc: quá nhiều phần, quá nhiều header, hay các trường chữ quá lớn; gửi ít trường hơn, file lớn đi trong một phần file")
		case errors.As(err, &system):
			return nil, value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("không lưu được upload ra file tạm của hệ thống: %v", system.Err))
		}
		return nil, value.Value{K: value.Nil} // a body that is not well-formed multipart has no upload
	}
	if form == nil {
		return nil, value.Value{K: value.Nil}
	}
	var uploads []value.Value
	for _, header := range form.File[name] {
		uploads = append(uploads, value.Value{K: value.Struct, V: &fileUpload{field: name, header: header}})
	}
	return uploads, value.Value{K: value.Nil}
}

// uploads parses the body's multipart form once: nil for a body of another media type.
func (r *Request) uploads() (*multipart.Form, error) {
	if r.uploadRead {
		return r.uploadForm, r.uploadErr
	}
	r.uploadRead = true
	req := r.request()
	if req == nil {
		return nil, nil
	}
	if mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || mediaType != "multipart/form-data" {
		return nil, nil
	}
	if req.MultipartForm == nil {
		if req.ContentLength > MaxBodyBytes {
			r.uploadErr = r.tooLarge()
			return nil, r.uploadErr
		}
		if req.Body != nil {
			req.Body = http.MaxBytesReader(nil, req.Body, MaxBodyBytes)
		}
		if err := req.ParseMultipartForm(uploadMemory); err != nil {
			var maxBytes *http.MaxBytesError
			switch {
			case errors.As(err, &maxBytes):
				r.uploadErr = r.tooLarge()
			case errors.Is(err, multipart.ErrMessageTooLarge):
				r.tooLarge() // 413 as a body past its ceiling; its own words in uploadsOf
				r.uploadErr = err
			default:
				r.uploadErr = err
			}
			return nil, r.uploadErr
		}
		if r.router != nil && r.router.requestScope != nil {
			// the server removes the files of the parse when the request ends; a host that serves
			// without it (a test, a self-call) has the request's scope do it
			form := req.MultipartForm
			r.router.requestScope.AddCleanup(func() { _ = form.RemoveAll() })
		}
	}
	r.uploadForm = req.MultipartForm
	return r.uploadForm, nil
}

// uploadFields are the fields of a multipart form request.file parsed, for request.form: only their bytes
// count against its ceiling.
func (r *Request) uploadFields() (url.Values, bool, error) {
	if !r.uploadRead || r.uploadForm == nil {
		return nil, false, nil
	}
	fields := url.Values(r.uploadForm.Value)
	size := int64(0)
	for name, values := range fields {
		for _, field := range values {
			size += int64(len(name) + len(field))
		}
	}
	if size > formLimit() {
		return nil, true, r.tooLarge()
	}
	return fields, true, nil
}
