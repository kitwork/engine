package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRejectsTraversalAndSymlinkParents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if _, err := Resolve(root, "..", filepath.Base(outside), "secret.txt"); err == nil {
		t.Fatal("lexical traversal was accepted")
	}

	link := filepath.Join(root, "external")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Resolve(root, "external", "new.txt"); err == nil {
		t.Fatal("write through an escaping symlink parent was accepted")
	}
}
