package file_test

import (
	"os"
	"path/filepath"
	"testing"

	filehelper "github.com/kitwork/engine/helpers/file"
)

func TestResolvePathSecurity(t *testing.T) {
	baseDir, err := os.MkdirTemp("", "file_sec_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(baseDir)

	// Valid path
	valid, err := filehelper.ResolvePath(baseDir, "images/logo.png")
	if err != nil {
		t.Errorf("ResolvePath valid failed: %v", err)
	}
	if !filepath.IsAbs(valid) {
		t.Errorf("Expected absolute path, got %q", valid)
	}

	// Security violation (directory traversal)
	_, err = filehelper.ResolvePath(baseDir, "../secret.txt")
	if err == nil {
		t.Error("Expected error on directory traversal, got nil")
	}
}

func TestMIMEAndDataURI(t *testing.T) {
	mime := filehelper.DetectMIME(".png")
	if mime != "image/png" {
		t.Errorf("Expected image/png, got %s", mime)
	}

	dataURI := filehelper.Base64DataURI([]byte("hello"), ".png")
	decoded, ok := filehelper.DecodeDataURI(dataURI)
	if !ok {
		t.Fatal("DecodeDataURI failed")
	}
	if string(decoded) != "hello" {
		t.Errorf("Expected 'hello', got %s", string(decoded))
	}
}
