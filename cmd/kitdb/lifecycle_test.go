package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestOperatorFolderAttachDetach(t *testing.T) {
	root := t.TempDir()
	runAndDecode(t, "init-root", root)
	path := filepath.Join(root, "shop", "data.kitdb")
	db, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	runAndDecode(t, "attach", root, "shop")
	runAndDecode(t, "detach", root, "shop")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("detach deleted data")
	}
	runAndDecode(t, "attach", root, "shop")
	var output bytes.Buffer
	if err := run(context.Background(), []string{"register-database", root, "alias", "shop"}, &output); err == nil {
		t.Fatal("CLI registered an alias")
	}
	if err := run(context.Background(), []string{"attach", root, "../shop"}, &output); err == nil {
		t.Fatal("CLI accepted unsafe folder")
	}
}
