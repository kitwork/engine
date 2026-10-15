package work

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A browser's own screenshot flag (--screenshot) draws a scrolled page wrongly: it shows neither what
// is fixed to the screen nor the content at the scroll position (tried 13/10/2026 on Edge: a page
// scrolled to its middle comes out the ground of <html> alone). What the screen shows after a scroll is
// read through the DevTools protocol instead, as a test harness drives a browser: Page.captureScreenshot
// of the composited screen. This is the least of that protocol over its WebSocket, with the standard
// library only: one page, commands in turn, their answers matched by id, events skipped.

// devtools is one page of a headless browser driven through the DevTools protocol.
type devtools struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
	next   int
}

var devtoolsListening = regexp.MustCompile(`DevTools listening on ws://([^/\s]+)/`)

// openDevtools starts a headless browser on url, the window width by height, in the light scheme with
// blink's settings, and connects to its page. The browser ends with the test.
func openDevtools(t *testing.T, browser, url string, width, height int, settings string) *devtools {
	t.Helper()
	blink := "preferredColorScheme=1"
	if settings != "" {
		blink += "," + settings
	}
	arguments := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--hide-scrollbars",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync", "--no-first-run",
		"--remote-debugging-port=0", "--remote-allow-origins=*", fmt.Sprintf("--window-size=%d,%d", width, height),
		"--user-data-dir=" + filepath.Join(t.TempDir(), "profile"), "--blink-settings=" + blink, url}
	if runtime.GOOS == "windows" {
		arguments = append(arguments, "--disable-breakpad", "--disable-crashpad-for-testing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	command := exec.CommandContext(ctx, browser, arguments...)
	stderr, err := command.StderrPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start %s: %v", browser, err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		cancel()
	})
	found := make(chan string, 1)
	go func() {
		lines := bufio.NewScanner(stderr)
		sent := false
		for lines.Scan() {
			if m := devtoolsListening.FindStringSubmatch(lines.Text()); m != nil && !sent {
				found <- m[1]
				sent = true
			}
		}
	}()
	var host string
	select {
	case host = <-found:
	case <-time.After(20 * time.Second):
		t.Fatal("the browser never said where its DevTools listen")
	}
	// the page's own socket, from the list of targets (the page opens a moment after the browser)
	var socket string
	for try := 0; try < 50 && socket == ""; try++ {
		response, err := http.Get("http://" + host + "/json/list")
		if err == nil {
			var targets []struct{ Type, URL, WebSocketDebuggerURL string }
			_ = json.NewDecoder(response.Body).Decode(&targets)
			response.Body.Close()
			for _, target := range targets {
				if target.Type == "page" && strings.HasPrefix(target.URL, "http") {
					socket = target.WebSocketDebuggerURL
				}
			}
		}
		if socket == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if socket == "" {
		t.Fatal("the browser has no page to drive")
	}
	path := socket[strings.Index(socket, host)+len(host):]
	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, host, base64.StdEncoding.EncodeToString(key))
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, " 101 ") {
		t.Fatalf("the page's socket answered %q: %v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	return &devtools{t: t, conn: conn, reader: reader}
}

// call sends one command and returns its result, skipping the events between.
func (d *devtools) call(method string, params map[string]any) json.RawMessage {
	d.t.Helper()
	result, problem := d.try(method, params)
	if problem != "" {
		d.t.Fatalf("%s: %s", method, problem)
	}
	return result
}

// try is call that hands back the protocol's refusal instead of failing the test.
func (d *devtools) try(method string, params map[string]any) (json.RawMessage, string) {
	d.t.Helper()
	d.next++
	message, _ := json.Marshal(map[string]any{"id": d.next, "method": method, "params": params})
	d.write(message)
	_ = d.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		var answer struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct{ Message string }
		}
		if err := json.Unmarshal(d.read(), &answer); err != nil {
			d.t.Fatalf("%s: %v", method, err)
		}
		if answer.ID != d.next {
			continue // an event, or nothing of ours
		}
		if answer.Error != nil {
			return nil, answer.Error.Message
		}
		return answer.Result, ""
	}
}

// loaded waits until the page has loaded and drawn two frames, and returns what settle (a script whose
// promise says where the page is) says then. The page may still be navigating when the browser lists
// it (from its first blank document): a script the navigation interrupts is tried again.
func (d *devtools) loaded(settle string) string {
	d.t.Helper()
	script := `new Promise(function (loaded) { if (document.readyState === "complete") loaded(); else addEventListener("load", function () { loaded(); }); }).then(function () { return ` + settle + `; })`
	for try := 0; ; try++ {
		result, problem := d.try("Runtime.evaluate", map[string]any{"expression": script, "awaitPromise": true, "returnByValue": true})
		if problem == "" {
			var out struct {
				Result struct {
					Value any `json:"value"`
				} `json:"result"`
			}
			_ = json.Unmarshal(result, &out)
			if out.Result.Value != nil && location(out.Result.Value) {
				return fmt.Sprint(out.Result.Value)
			}
		}
		if try == 50 {
			d.t.Fatalf("the page never loaded: %s", problem)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// location reports whether a value a settle script returned comes from a page that has one: the blank
// document before the navigation has no scroll height to speak of.
func location(v any) bool {
	text, ok := v.(string)
	return ok && !strings.HasSuffix(text, " of 0")
}

// evaluate runs a script in the page, waiting for the promise it returns, and returns the value it
// settles with, as text ("" for none).
func (d *devtools) evaluate(script string) string {
	d.t.Helper()
	var out struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		Exception *struct{ Text string } `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(d.call("Runtime.evaluate", map[string]any{"expression": script, "awaitPromise": true, "returnByValue": true}), &out); err != nil {
		d.t.Fatal(err)
	}
	if out.Exception != nil {
		d.t.Fatalf("the script threw: %s\n%s", out.Exception.Text, script)
	}
	if out.Result.Value == nil {
		return ""
	}
	return fmt.Sprint(out.Result.Value)
}

// screenshot is the screen as the browser composited it, decoded.
func (d *devtools) screenshot() image.Image {
	d.t.Helper()
	var shot struct{ Data string }
	if err := json.Unmarshal(d.call("Page.captureScreenshot", map[string]any{"format": "png"}), &shot); err != nil {
		d.t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		d.t.Fatal(err)
	}
	picture, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		d.t.Fatal(err)
	}
	return picture
}

// write sends one text frame, masked as a client must.
func (d *devtools) write(payload []byte) {
	header := []byte{0x81}
	switch n := len(payload); {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n < 1<<16:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		header = append(header, 0x80|127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if _, err := d.conn.Write(append(append(header, mask...), masked...)); err != nil {
		d.t.Fatal(err)
	}
}

// read returns the next whole text message, its fragments joined; control frames are skipped.
func (d *devtools) read() []byte {
	var message []byte
	for {
		head := make([]byte, 2)
		if _, err := io.ReadFull(d.reader, head); err != nil {
			d.t.Fatalf("the page's socket: %v", err)
		}
		final, opcode := head[0]&0x80 != 0, head[0]&0x0f
		length := uint64(head[1] & 0x7f)
		switch length {
		case 126:
			extended := make([]byte, 2)
			_, _ = io.ReadFull(d.reader, extended)
			length = uint64(binary.BigEndian.Uint16(extended))
		case 127:
			extended := make([]byte, 8)
			_, _ = io.ReadFull(d.reader, extended)
			length = binary.BigEndian.Uint64(extended)
		}
		var mask []byte
		if head[1]&0x80 != 0 {
			mask = make([]byte, 4)
			_, _ = io.ReadFull(d.reader, mask)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(d.reader, payload); err != nil {
			d.t.Fatalf("the page's socket: %v", err)
		}
		if mask != nil {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x8:
			d.t.Fatal("the page's socket closed")
		case 0x9, 0xa:
			continue // ping, pong
		}
		message = append(message, payload...)
		if final {
			return message
		}
	}
}
