package collection_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/capabilities"
	collectioncap "github.com/kitwork/engine/capabilities/collection"
	"github.com/kitwork/engine/value"
)

type mockScope struct {
	appID  string
	domain string
	root   string
}

func (m *mockScope) AppID() string  { return m.appID }
func (m *mockScope) Domain() string { return m.domain }
func (m *mockScope) ResolvePath(paths ...string) string {
	if len(paths) == 0 {
		return m.root
	}
	return filepath.Join(append([]string{m.root}, paths...)...)
}
func (m *mockScope) DB(name string) *sql.DB { return nil }

func TestCollectionCapability(t *testing.T) {
	dir := t.TempDir()
	postsDir := filepath.Join(dir, "_collection", "posts")
	os.MkdirAll(postsDir, 0755)

	os.WriteFile(filepath.Join(postsDir, "hello.md"), []byte("---\ntitle: Hello World\n---\nHello body"), 0644)

	scope := &mockScope{appID: "app1", domain: "example.com", root: dir}
	val, ok := capabilities.DefaultRegistry.Get("collection", scope)
	if !ok {
		t.Fatal("Collection capability not registered")
	}

	mgr, ok := val.V.(*collectioncap.Manager)
	if !ok {
		t.Fatalf("Expected *collectioncap.Manager, got %T", val.V)
	}

	resNoArgs := mgr.Open()
	if resNoArgs.K != value.Invalid {
		t.Errorf("Open without args should return value.Invalid, got key %v", resNoArgs.K)
	}

	resPosts := mgr.Open(value.NewString("posts"))
	if resPosts.K == value.Invalid {
		t.Errorf("Open('posts') failed: %v", resPosts.V)
	}
}
