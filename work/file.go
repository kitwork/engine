package work

import (
	"os"
	"path/filepath"

	filehelper "github.com/kitwork/engine/utilities/file"
	"github.com/kitwork/engine/value"
)

type File struct {
	tenant *Tenant
}

func (w *KitWork) File() *File {
	return &File{tenant: w.tenant}
}

func (f *File) resolve(path string) (string, error) {
	return filehelper.ResolvePath(f.tenant.resolve(), path)
}

func (f *File) Read(path string) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(string(content))
}

func (f *File) Base64(path string) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	ext := filepath.Ext(path)
	dataURI := filehelper.Base64DataURI(content, ext)
	return value.New(dataURI)
}

func (f *File) Write(path string, data value.Value) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	var bytesToWrite []byte
	strVal := data.String()
	if decoded, ok := filehelper.DecodeDataURI(strVal); ok {
		bytesToWrite = decoded
	} else {
		bytesToWrite = []byte(data.Text())
	}

	if err := os.WriteFile(fullPath, bytesToWrite, 0644); err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	return value.New(true)
}

func (f *File) Save(path string, data value.Value) value.Value {
	return f.Write(path, data)
}
