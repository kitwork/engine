package core

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
)

// A site with an open stream is not idle (KITWORK_SSE.md mục 4): a stream is one request that lasts, and
// the cleanup that evicts an idle site would cut it. Once the stream closes, the site is idle again and
// goes.
//
// CONTROL: with evictIdle not asking OpenStreams, the pass evicted the site, closing its stream: "the idle
// pass evicted the site of an open stream (kept false, 0 open after it)".
func TestIdleSiteWithAnOpenStreamIsNotEvicted(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "import * as router from \"kitwork/router\";\nimport { memory } from \"kitwork/storage\";\n" +
		"router.get((context) => context.sse.reconnect(\"1s\").subscribe(memory(\"x\")));\n"
	if err := os.WriteFile(filepath.Join(directory, work.UIRouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := New(root, 0, false, "")
	server := httptest.NewServer(engine)
	t.Cleanup(func() { server.Close(); engine.Close() })
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	request.Host = "localhost"
	stream, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stream.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "retry: 1000\n" {
		t.Fatalf("first line = %q %v", line, err)
	}
	engine.mu.RLock()
	cached := engine.cache["localhost"]
	engine.mu.RUnlock()
	tenant := cached.current()
	waitLifecycleCondition(t, "the open stream", func() bool { return tenant.OpenStreams() == 1 })

	idle := time.Now().Add(engine.idleTimeout + time.Hour)
	engine.evictIdle(idle)
	engine.mu.RLock()
	_, kept := engine.cache["localhost"]
	engine.mu.RUnlock()
	if !kept || tenant.OpenStreams() != 1 {
		t.Fatalf("the idle pass evicted the site of an open stream (kept %v, %d open after it)", kept, tenant.OpenStreams())
	}

	_ = stream.Body.Close()
	waitLifecycleCondition(t, "the stream closed", func() bool { return tenant.OpenStreams() == 0 })
	engine.evictIdle(idle)
	engine.mu.RLock()
	_, kept = engine.cache["localhost"]
	engine.mu.RUnlock()
	if kept {
		t.Fatal("the site stayed after its stream closed")
	}
}
