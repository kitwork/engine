package render

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestPreparedRenderUsesImmutableTemplateSnapshot(t *testing.T) {
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		filename := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.kitwork.html", `<html><body>{{ @page }}</body></html>`)
	write("page.kitwork.html", `<main>v1 {{ include shared/note }}</main>`)
	write("shared/note.html", `<span>{{ message }}</span>`)

	snapshot, err := NewSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	prepared := New(Config{
		Base:      root,
		Directory: ".",
		Source:    snapshot,
	}).Prepare()
	if err := prepared.PreparationError(); err != nil {
		t.Fatal(err)
	}
	data := value.New(map[string]any{"message": "hello"})
	if output := prepared.Bind(data).String(); !strings.Contains(output, "v1") ||
		!strings.Contains(output, "<span>hello</span>") {
		t.Fatalf("prepared output = %q", output)
	}

	write("page.kitwork.html", `<main>v2</main>`)
	write("shared/note.html", `<span>changed</span>`)
	for i := 0; i < 32; i++ {
		if output := prepared.Bind(data).String(); !strings.Contains(output, "v1") ||
			strings.Contains(output, "v2") || strings.Contains(output, "changed") {
			t.Fatalf("disk edit changed prepared output: %q", output)
		}
	}

	var wg sync.WaitGroup
	results := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- prepared.Bind(data).String()
		}()
	}
	wg.Wait()
	close(results)
	for output := range results {
		if !strings.Contains(output, "<span>hello</span>") {
			t.Fatalf("concurrent prepared output = %q", output)
		}
	}

	snapshot.Close()
	if output := prepared.Bind(data).String(); !strings.Contains(output, "<span>hello</span>") {
		t.Fatalf("prepared AST retained a filesystem dependency: %q", output)
	}
}

func TestPrepareCapturesMalformedTemplate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "index.kitwork.html"),
		[]byte(`{{ if }}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	prepared := New(Config{Base: root, Directory: "."}).Prepare()
	if err := prepared.PreparationError(); err == nil {
		t.Fatal("malformed template did not produce a preparation error")
	}
}
