// Package compress adds transport compression to any http.Handler.
//
// It exists because the engine was shipping pages raw: a real Kitwork site measured 174 KB on the
// wire. HTML of that shape gzips 6-8x, so most of those bytes were avoidable. The render itself
// takes microseconds — the transfer takes hundreds of milliseconds on an ordinary connection, so
// this is the largest single lever on what a visitor actually experiences.
//
// Three things it must not break, which is most of the code below:
//
//   - STREAMING. text/event-stream is a live connection; buffering it to compress would hold
//     events until the buffer fills, i.e. break SSE entirely. Any handler that flushes is treated
//     as streaming and passed through untouched.
//   - ALREADY-COMPRESSED BODIES. Images, woff2, video and archives are compressed formats. Running
//     gzip over them burns CPU to make them very slightly larger.
//   - TINY BODIES. Below about a kilobyte the gzip header and trailer cost more than the saving,
//     and a redirect or a 204 has nothing to compress at all.
//
// The content type is only known once the handler writes, so the decision is made at the first
// write rather than up front.
package compress

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// minSize is the body size below which compressing loses. A gzip stream costs ~20 bytes of header
// and trailer plus the deflate block overhead; under a kilobyte that is most of the "saving".
const minSize = 1024

var gzipPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// Middleware compresses responses for clients that advertise gzip. It is a no-op for everything
// described in the package comment, so it is safe to wrap the whole handler once.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Announce that the response varies by encoding even when this particular one is not
		// compressed: a shared cache must not hand a gzipped body to a client that cannot read it.
		w.Header().Add("Vary", "Accept-Encoding")

		cw := &compressWriter{ResponseWriter: w}
		defer cw.finish()
		next.ServeHTTP(cw, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if name, _, _ := strings.Cut(strings.TrimSpace(enc), ";"); name == "gzip" {
			return true
		}
	}
	return false
}

// compressWriter defers the compress/passthrough decision until it has seen the status, the
// headers, and enough of the body to know whether compressing is worth it.
type compressWriter struct {
	http.ResponseWriter

	status      int
	wroteHeader bool
	decided     bool
	gz          *gzip.Writer
	buf         []byte
}

func (c *compressWriter) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.status = status
	c.wroteHeader = true
	// The header write is deliberately NOT forwarded yet: compressing removes Content-Length and
	// adds Content-Encoding, and neither can be changed after the status line goes out.
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	if c.decided {
		if c.gz != nil {
			return c.gz.Write(p)
		}
		return c.ResponseWriter.Write(p)
	}

	// Hold the opening bytes until the body is either big enough to be worth compressing or the
	// handler is done. len(buf) is bounded by minSize plus one write.
	c.buf = append(c.buf, p...)
	if len(c.buf) < minSize {
		return len(p), nil
	}
	if err := c.decide(true); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush means the handler wants bytes on the wire NOW: SSE and any progressive response reach
// here. Committing with bigEnough=false is what makes that safe — it settles on the identity
// encoding, so nothing is ever held back waiting for a compression window to fill, which would be
// indistinguishable from the stream having stalled.
func (c *compressWriter) Flush() {
	if !c.decided {
		_ = c.decide(false)
	}
	if c.gz != nil {
		_ = c.gz.Flush()
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// decide commits to compressing or not, sends the header, and drains whatever was buffered. It runs
// exactly once. bigEnough is false when the handler finished or FLUSHED before reaching minSize —
// both mean "send what we have as-is", which is why a flushing handler is never compressed.
func (c *compressWriter) decide(bigEnough bool) error {
	if c.decided {
		return nil
	}
	c.decided = true

	if bigEnough && shouldCompress(c.ResponseWriter.Header()) {
		h := c.ResponseWriter.Header()
		h.Set("Content-Encoding", "gzip")
		// The declared length belongs to the identity encoding; keeping it would describe the
		// compressed body incorrectly and truncate the response.
		h.Del("Content-Length")
		c.ResponseWriter.WriteHeader(c.status)

		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(c.ResponseWriter)
		c.gz = gz
		if len(c.buf) > 0 {
			if _, err := gz.Write(c.buf); err != nil {
				c.buf = nil
				return err
			}
		}
		c.buf = nil
		return nil
	}

	c.ResponseWriter.WriteHeader(c.status)
	if len(c.buf) > 0 {
		_, err := c.ResponseWriter.Write(c.buf)
		c.buf = nil
		return err
	}
	c.buf = nil
	return nil
}

// finish flushes and returns the compressor. Runs even for a handler that wrote nothing, so the
// status still reaches the client.
func (c *compressWriter) finish() {
	if !c.decided {
		_ = c.decide(false) // body never reached minSize: send it as-is
	}
	if c.gz != nil {
		_ = c.gz.Close()
		gzipPool.Put(c.gz)
		c.gz = nil
	}
}

// Unwrap lets http.ResponseController reach the underlying writer for deadlines and hijacking.
func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// shouldCompress reads the response headers the handler has set.
func shouldCompress(h http.Header) bool {
	if h.Get("Content-Encoding") != "" {
		return false // the handler compressed it already
	}
	mediaType, _, _ := strings.Cut(h.Get("Content-Type"), ";")
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if mediaType == "" {
		return false // nothing declared: do not guess at a body we cannot classify
	}
	if mediaType == "text/event-stream" {
		return false // a live stream, even if it never flushes
	}
	return compressible(mediaType)
}

// compressible reports whether a media type carries data that gzip actually shrinks. Text-shaped
// types are listed positively rather than excluding binary ones, so an unknown type is left alone
// instead of being compressed on a guess.
func compressible(mediaType string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json",
		"application/javascript",
		"application/x-javascript",
		"application/xml",
		"application/xhtml+xml",
		"application/rss+xml",
		"application/atom+xml",
		"application/manifest+json",
		"application/ld+json",
		"image/svg+xml": // SVG is markup, unlike every other image type
		return true
	}
	return false
}
