package work

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	filehelper "github.com/kitwork/engine/utilities/file"
	"github.com/kitwork/engine/value"
)

type File struct {
	tenant *Tenant
}

func (w *KitWork) File() *File {
	return &File{tenant: w.tenant}
}

// fileFailure is a file the program asked for, at path, that failed: one that is not there
// INVALID_ARGUMENT, one it may not touch DENIED, any other failure of the disk DATABASE (the storage
// layer). The message names the file as the program wrote it, never where the app lives on the server:
// a site may hand error.message to a browser (14/10/2026).
func fileFailure(path string, err error) value.Value {
	message := err.Error()
	var failed *fs.PathError
	if errors.As(err, &failed) {
		message = failed.Op + " " + path + ": " + failed.Err.Error()
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	case errors.Is(err, fs.ErrPermission):
		return value.InvalidFailure(value.DeniedCode, message)
	}
	return value.InvalidFailure(value.DatabaseCode, message)
}

// outsideFailure is a path outside the app's folder (or one the boundary could not read), DENIED,
// named as the program wrote it.
func outsideFailure(path string) value.Value {
	return value.InvalidFailure(value.DeniedCode, "file: đường dẫn "+strconv.Quote(path)+" nằm ngoài thư mục của app")
}

func (f *File) resolve(path string) (string, error) {
	return filehelper.ResolvePath(f.tenant.resolve(), path)
}

func (f *File) Read(path string) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		// a path outside the app's folder
		return outsideFailure(path)
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return fileFailure(path, err)
	}
	return value.New(string(content))
}

func (f *File) Base64(path string) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		// a path outside the app's folder
		return outsideFailure(path)
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return fileFailure(path, err)
	}

	ext := filepath.Ext(path)
	dataURI := filehelper.Base64DataURI(content, ext)
	return value.New(dataURI)
}

func (f *File) Write(path string, data value.Value) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		// a path outside the app's folder
		return outsideFailure(path)
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fileFailure(filepath.ToSlash(filepath.Dir(path)), err)
	}

	var bytesToWrite []byte
	strVal := data.String()
	if decoded, ok := filehelper.DecodeDataURI(strVal); ok {
		bytesToWrite = decoded
	} else {
		bytesToWrite = []byte(data.Text())
	}

	if err := os.WriteFile(fullPath, bytesToWrite, 0644); err != nil {
		return fileFailure(path, err)
	}

	return value.New(true)
}

func (f *File) Save(path string, data value.Value) value.Value {
	return f.Write(path, data)
}
