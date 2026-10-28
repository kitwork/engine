package work

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// The media type is the edit .type() before the answer (20/10/2026: { type } of an answer is gone):
// context.type("image/png").file(bytes) for bytes the handler made; response.image(bytes) is gone.
func TestAnswerMediaTypeIsTheTypeEdit(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import * as router from "kitwork/router";`,
		"qr/router.kitwork.js": "import * as router from \"kitwork/router\";\nimport { qrcode } from \"kitwork\";\n" +
			"router.get(({ response }) => response.header(\"X-Qr\", \"1\").type(\"image/png\").file(qrcode.data(\"kitwork\").png()));\n",
		"shortcut/router.kitwork.js": "import * as router from \"kitwork/router\";\nimport { qrcode } from \"kitwork\";\n" +
			"router.get((context) => context.type(\"image/png\").file(qrcode.data(\"kitwork\").png()));\n",
		"csv/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.status(201).type(\"text/csv; charset=utf-8\").text(\"a,b\"));\n",
		"send/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.type(\"application/xml\").send(\"<x/>\"));\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, path := range []string{"/qr", "/shortcut"} {
		r := serveGet(tn, path, nil)
		if r.Code != http.StatusOK || r.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(r.Body.String(), "\x89PNG") {
			t.Fatalf("%s: %d type=%q body=%.8q", path, r.Code, r.Header().Get("Content-Type"), r.Body.String())
		}
	}
	if r := serveGet(tn, "/csv", nil); r.Code != http.StatusCreated || r.Header().Get("Content-Type") != "text/csv; charset=utf-8" || r.Body.String() != "a,b" {
		t.Fatalf("/csv: %d type=%q %q", r.Code, r.Header().Get("Content-Type"), r.Body.String())
	}
	if r := serveGet(tn, "/send", nil); r.Header().Get("Content-Type") != "application/xml" || r.Body.String() != "<x/>" {
		t.Fatalf("/send: %d type=%q %q", r.Code, r.Header().Get("Content-Type"), r.Body.String())
	}
	// .image() on a response is the head image, not an answer
	r := &Response{}
	if r.Image(value.New("/og.png")); r.IsSend() {
		t.Fatalf("response.image answered: kind=%q", r.Kind())
	}
}
