package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/kitwork/engine/value"
)

type Response struct {
	Status int
	Body   value.Value
	Error  string
	Cached bool // served from the .cache()/.persist() tiers, not the network
	Stale  bool // served from an EXPIRED persisted copy because the live request failed
}

func (r Response) Ok() bool {
	return r.Status >= 200 && r.Status < 300
}

func (r Response) JSON() value.Value {
	var jsonData any
	if err := json.Unmarshal(r.Body.Bytes(), &jsonData); err == nil {
		return value.New(jsonData)
	}
	return value.New(nil)
}

func (r Response) Text() string {
	return r.Body.String()
}

func (r Response) Base64() string {
	b := r.Body.Bytes()
	b64 := base64.StdEncoding.EncodeToString(b)

	var mimeType string
	if len(b) >= 4 && bytes.HasPrefix(b, []byte("\x89PNG")) {
		mimeType = "image/png"
	} else if len(b) >= 3 && bytes.HasPrefix(b, []byte("\xff\xd8\xff")) {
		mimeType = "image/jpeg"
	} else if len(b) >= 6 && (bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a"))) {
		mimeType = "image/gif"
	} else if bytes.Contains(b, []byte("<svg")) {
		mimeType = "image/svg+xml"
	}

	if mimeType != "" {
		return fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	}
	return b64
}
