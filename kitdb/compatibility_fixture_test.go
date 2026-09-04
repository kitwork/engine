package kitdb

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// This is a compressed byte-for-byte format-v3 database produced by the first
// KitDB 1.0 release candidate. It has a fixed identity, transaction 7, and the
// rows alpha=one, middle=two, zulu=three. Never regenerate it in place.
const kitDBV1MainFixture = "H4sIAAAAAAAC//L2DHFx8jUwZmZwYACB7MySlCTdMkPdtMyKktKiVAY8oK/u4Few/mCQ/gas+hmhatmhdEWYiRBILE4Awm+Ait+D8ll1JoWAaGYGwuD8z5V/GEbBKBgFo2AUjIJRMApGwSgYBaNgFIyCUUAQMLJCO9uJOQUZifl5qYxsUIHczJSUnNSS8nxGFlC/nIGBoao0p7Qkoyg1lQHaWTeFGgJSvl9dnQ+kiAVmFkg1ZHTBDefoAq7RAVjvH10e3WJTKF8TySEwfTsa42MBAQAA//906oDE3hAAAA=="

func TestKitDBV1FrozenMainFixture(t *testing.T) {
	compressed, err := base64.StdEncoding.DecodeString(kitDBV1MainFixture)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("decode frozen fixture = (%v, close=%v)", readErr, closeErr)
	}
	path := filepath.Join(t.TempDir(), "kitdb-v1.kitdb")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	database, err := OpenWithOptions(path, OpenOptions{VerifyOnOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if database.ID() != "6b697464622d76312d66697874757265" {
		_ = database.Close()
		t.Fatalf("fixture database identity = %s", database.ID())
	}
	stats, err := database.Stats()
	if err != nil || stats.MainFormatVersion != 3 ||
		stats.LastTransaction != 7 || stats.MainRecords != 3 {
		_ = database.Close()
		t.Fatalf("fixture stats = (%#v, %v)", stats, err)
	}
	for key, want := range map[string]string{
		"alpha": "one", "middle": "two", "zulu": "three",
	} {
		value, found, err := database.Get([]byte(key))
		if err != nil || !found || string(value) != want {
			_ = database.Close()
			t.Fatalf("fixture %s = (%q, %t, %v), want %q", key, value, found, err, want)
		}
	}
	if transaction, err := database.Checkpoint(); err != nil || transaction != 7 {
		_ = database.Close()
		t.Fatalf("fixture checkpoint = (%d, %v)", transaction, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWithOptions(path, OpenOptions{VerifyOnOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if value, found, err := reopened.Get([]byte("middle")); err != nil || !found || string(value) != "two" {
		_ = reopened.Close()
		t.Fatalf("reopened fixture middle = (%q, %t, %v)", value, found, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}
