package work

// request.form(name) (KITWORK_ROUTER §5, 20/10/2026): a field of the form the request's BODY carries,
// urlencoded (application/x-www-form-urlencoded) or multipart (multipart/form-data), never a value of
// the URL's query, which request.query reads. "" when the body has no such field, and for a body that
// is not a form, or not a well formed one. The body is read under the ceiling every body read has,
// 1 MiB (or the host's, app.web({ maxBody }), when it is lower): past it the request answers 413 and
// nothing more is read. A file of a multipart form is not a field: its name reads "" (the upload API is
// still open, §14).
//
// request.formValue stays what it was for every site: Go's FormValue, the body's field or else the
// query's, read without this ceiling (legacy apps read their login forms with it). A body formValue or
// formParams read first is past the ceiling all the same for request.form: 413.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/kitwork/engine/value"
)

// Form is request.form(name): the first value of the body's field name, "" when the body has none.
func (r *Request) Form(name string) value.Value {
	fields, err := r.readForm()
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			return value.InvalidFailure(value.InvalidInputCode, formTooLarge())
		}
		return bodyFailure(err, formLimit())
	}
	return value.New(fields.Get(name))
}

// formLimit is the ceiling of request.form's body: DefaultBodyLimit, or the host's ceiling when it is
// lower (readBody never reads past MaxBodyBytes).
func formLimit() int64 {
	return min(DefaultBodyLimit, MaxBodyBytes)
}

// formTooLarge is the failure of a form past its ceiling, naming the ceiling that refused it.
func formTooLarge() string {
	if MaxBodyBytes < DefaultBodyLimit {
		return fmt.Sprintf("thân request vượt trần %d byte của host (app.web({ maxBody })), thấp hơn trần %d byte của request.form(…)", MaxBodyBytes, DefaultBodyLimit)
	}
	return fmt.Sprintf("thân request vượt trần %d byte của request.form(…)", DefaultBodyLimit)
}

// readForm reads the body's form once and keeps its fields: no fields (nil) for a body that is not a
// form. A body request.formValue read first is gone from the request (Go's ParseForm read it): its
// fields are that parse's PostForm, the body's alone, kept only when that body is within the ceiling
// (goFormSize) and when that parse read it to its end (goStoppedShort).
func (r *Request) readForm() (url.Values, error) {
	if r.formRead {
		return r.formFields, nil
	}
	req := r.request()
	if req == nil {
		r.formRead = true
		return nil, nil
	}
	if !r.bodyRead && goReadTheForm(req) {
		if goFormSize(req) > formLimit() || goStoppedShort(req) {
			return nil, r.tooLarge()
		}
		r.formRead, r.formFields = true, req.PostForm
		return r.formFields, nil
	}
	body, err := r.readBody(formLimit())
	if err != nil {
		return nil, err
	}
	r.formRead, r.formFields = true, formOfBody(req.Header.Get("Content-Type"), body)
	return r.formFields, nil
}

// goReadTheForm reports whether Go's own form parse (request.formValue, request.formParams) read the
// body: a multipart form, or a POST, PUT or PATCH, the methods whose body ParseForm reads.
func goReadTheForm(req *http.Request) bool {
	if req.PostForm == nil {
		return false
	}
	if req.MultipartForm != nil {
		return true
	}
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// goFormSize is the size of the body Go's own form parse read: its Content-Length when the request sent
// one, else the size of what the parse kept (each field's name and value, each file of a multipart
// form), which is never more than what was read.
func goFormSize(req *http.Request) int64 {
	if req.ContentLength >= 0 {
		return req.ContentLength
	}
	var size int64
	for name, values := range req.PostForm {
		for _, field := range values {
			size += int64(len(name) + len(field))
		}
	}
	if req.MultipartForm != nil {
		for _, files := range req.MultipartForm.File {
			for _, file := range files {
				size += file.Size
			}
		}
	}
	return size
}

// goStoppedShort reports whether Go's own form parse stopped before the end of a body that came without
// its length, keeping none of its fields: the host's ceiling stopped it (the body's reader, the
// server's http.MaxBytesHandler, keeps answering *http.MaxBytesError), or an urlencoded body went on
// past Go's own 10 MB. A body that said its length needs no asking (goFormSize reads the length), and a
// multipart form Go read whole may leave bytes past its closing boundary, which are not a sign.
func goStoppedShort(req *http.Request) bool {
	if req.ContentLength >= 0 || req.Body == nil || req.MultipartForm != nil {
		return false
	}
	var next [1]byte
	read, err := req.Body.Read(next[:])
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return true
	}
	mediaType, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	return read > 0 && mediaType == "application/x-www-form-urlencoded"
}

// formOfBody reads a body as the form its media type names. nil for any other media type, a multipart
// type without its boundary, or a body that does not read as its type: a broken form has no fields,
// not the ones read before the break.
func formOfBody(contentType string, body []byte) url.Values {
	if len(body) == 0 {
		return nil
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil
	}
	switch mediaType {
	case "application/x-www-form-urlencoded":
		fields, err := url.ParseQuery(string(body))
		if err != nil {
			return nil
		}
		return fields
	case "multipart/form-data":
		boundary := parameters["boundary"]
		if boundary == "" {
			return nil
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		fields := url.Values{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return fields
			}
			if err != nil {
				return nil
			}
			name := part.FormName()
			if name == "" || part.FileName() != "" {
				continue // not a field: no name, or a file
			}
			text, err := io.ReadAll(part)
			if err != nil {
				return nil
			}
			fields.Add(name, string(text))
		}
	}
	return nil
}
