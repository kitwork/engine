package work

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/value"
)

type File struct {
	tenant *Tenant
}

func (w *KitWork) File() *File {
	return &File{tenant: w.tenant}
}

func (f *File) resolve(path string) (string, error) {
	baseDir, err := filepath.Abs(f.tenant.resolve())
	if err != nil {
		return "", err
	}
	targetPath, err := filepath.Abs(f.tenant.resolve(path))
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(baseDir, targetPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("security boundary violation: access denied")
	}
	return targetPath, nil
}

// Read reads a file and returns its content as a string
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

// Base64 reads a file and returns its base64 data URI (auto detecting mime type)
func (f *File) Base64(path string) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	b64 := base64.StdEncoding.EncodeToString(content)

	// Detect content type from file extension
	ext := strings.ToLower(filepath.Ext(path))
	var mimeType string
	switch ext {
	case ".png":
		mimeType = "image/png"
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".gif":
		mimeType = "image/gif"
	case ".svg":
		mimeType = "image/svg+xml"
	case ".webp":
		mimeType = "image/webp"
	case ".ico":
		mimeType = "image/x-icon"
	default:
		mimeType = "application/octet-stream"
	}

	return value.New(fmt.Sprintf("data:%s;base64,%s", mimeType, b64))
}

// Write writes data to a file inside the tenant base directory.
// If data is a base64 Data URI, it automatically decodes it to write a binary file.
func (f *File) Write(path string, data value.Value) value.Value {
	fullPath, err := f.resolve(path)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	// Create directories if they do not exist
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	var bytesToWrite []byte
	strVal := data.String()
	if strings.HasPrefix(strVal, "data:") && strings.Contains(strVal, ";base64,") {
		parts := strings.SplitN(strVal, ";base64,", 2)
		if len(parts) == 2 {
			decoded, err := base64.StdEncoding.DecodeString(parts[1])
			if err == nil {
				bytesToWrite = decoded
			} else {
				bytesToWrite = []byte(strVal)
			}
		} else {
			bytesToWrite = []byte(strVal)
		}
	} else {
		bytesToWrite = []byte(data.Text())
	}

	if err := os.WriteFile(fullPath, bytesToWrite, 0644); err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	return value.New(true)
}

// Save is an alias of Write
func (f *File) Save(path string, data value.Value) value.Value {
	return f.Write(path, data)
}
