package work

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The tabs told when the data they show changes (28/10/2026): one channel per tab, GET /_kitwork/changes, kept
// while the tab is seen and across the pages it moves to. It carries no data, only the stamp of a change
// (nextViewStamp):
//
//	event: connection        data: 6f1c…       the channel's own name, said first
//	data: 1793174400123456                     something the tab listens to changed then
//	event: renew                               the tab's tickets run out: ask again for the page
//
// A page of router.view that holds its data in the browser is given, as it is drawn, asked again or answers
// a job, a ticket for each key of data its run read: the page itself, and each key of storage (memory, disk,
// datastore and their segments) it read. The tab hands its tickets to its channel
// (POST /_kitwork/changes?connection=…, { tickets }); a key written from anywhere — a job of any page, a job
// of the queue, a schedule — tells every channel holding a ticket for it. The tab, told a stamp newer than
// the data it holds, asks for what changed with its cookies, past the page's guards (kitwork-refresh).
//
// The channel is answered before the route tree (work.go): it leases no VM, holds no request's run, only a
// stream and the marks of its keys.
//
// A ticket is the server's: it names its key by a mark only the server can make (the key itself never
// reaches the browser), it runs out ten minutes after it was made, and it holds for the cookies it was made
// with, so a tab that signed out holds tickets for nothing. A browser can only hold a ticket for data a page
// let it read. A native app's shell holds no stream: its channel is answered 204.

const (
	changesPath = "/_kitwork/changes"
	// ticketLife is how long a ticket holds; a tab is told to renew before
	ticketLife = 10 * time.Minute
	// changesBodyLimit bounds a channel's tickets
	changesBodyLimit = 64 << 10
)

// viewChangesRetry is how long a browser waits before it connects again to a channel that was cut.
var viewChangesRetry = 3 * time.Second

// changeHub is an app's channels and the keys they hold tickets for, by the key's mark. Storage is the app's,
// so a key written by one site of the app tells a tab of another.
type changeHub struct {
	secret []byte

	mu       sync.Mutex
	channels map[string]*changeChannel
	keys     map[string]map[*changeChannel]struct{}
	last     map[string]int64
}

// changeChannel is one tab's channel: the stamps it is told, one waiting at most, and the marks of the keys
// it holds tickets for with when each runs out.
type changeChannel struct {
	id    string
	stamp chan int64
	keys  map[string]time.Time
}

func newChangeHub() *changeHub {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &changeHub{secret: secret, channels: map[string]*changeChannel{}, keys: map[string]map[*changeChannel]struct{}{}, last: map[string]int64{}}
}

// changes is the hub of t's app.
func (t *Tenant) changes() *changeHub {
	app, err := t.storage()
	if err != nil {
		return nil
	}
	app.changesOnce.Do(func() { app.changeHub = newChangeHub() })
	return app.changeHub
}

// mark names a key among the app's without saying it: what a ticket carries, and what a write tells.
func (h *changeHub) mark(key string) string {
	m := hmac.New(sha256.New, h.secret)
	m.Write([]byte("key\x00" + key))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12])
}

// cookiesMark is what a ticket holds for: the cookies of the request it was made for.
func cookiesMark(r *http.Request) string {
	if r == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(r.Header.Get("Cookie")))
	return hex.EncodeToString(sum[:8])
}

func (h *changeHub) seal(mark, expires, cookies string) string {
	m := hmac.New(sha256.New, h.secret)
	m.Write([]byte(mark + "." + expires + "." + cookies))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12])
}

// tickets makes a ticket for each key, for the cookies of r: mark.expires.seal.
func (h *changeHub) tickets(r *http.Request, keys []string) []string {
	expires := strconv.FormatInt(time.Now().Add(ticketLife).Unix(), 36)
	cookies := cookiesMark(r)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		mark := h.mark(key)
		out = append(out, mark+"."+expires+"."+h.seal(mark, expires, cookies))
	}
	return out
}

// read answers the mark of a ticket and when it runs out, when it is the server's, holds for these cookies and
// has not run out.
func (h *changeHub) read(ticket, cookies string, now time.Time) (string, time.Time, bool) {
	mark, rest, ok := strings.Cut(ticket, ".")
	if !ok {
		return "", time.Time{}, false
	}
	expires, sealed, ok := strings.Cut(rest, ".")
	if !ok || !hmac.Equal([]byte(sealed), []byte(h.seal(mark, expires, cookies))) {
		return "", time.Time{}, false
	}
	seconds, err := strconv.ParseInt(expires, 36, 64)
	if err != nil || !now.Before(time.Unix(seconds, 0)) {
		return "", time.Time{}, false
	}
	return mark, time.Unix(seconds, 0), true
}

func (h *changeHub) open() *changeChannel {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	c := &changeChannel{id: hex.EncodeToString(id), stamp: make(chan int64, 1), keys: map[string]time.Time{}}
	h.mu.Lock()
	h.channels[c.id] = c
	h.mu.Unlock()
	return c
}

func (h *changeHub) close(c *changeChannel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.channels, c.id)
	for mark := range c.keys {
		h.drop(mark, c)
	}
}

func (h *changeHub) drop(mark string, c *changeChannel) {
	if held := h.keys[mark]; held != nil {
		delete(held, c)
		if len(held) == 0 {
			delete(h.keys, mark)
		}
	}
}

// hold makes the channel id hold the tickets given, in place of those it held, and answers the last stamp
// told for any of them; found is false for a channel that is not open.
func (h *changeHub) hold(id string, tickets []string, cookies string) (last int64, taken int, found bool) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.channels[id]
	if c == nil {
		return 0, 0, false
	}
	for mark := range c.keys {
		h.drop(mark, c)
	}
	c.keys = map[string]time.Time{}
	for _, ticket := range tickets {
		mark, expires, ok := h.read(ticket, cookies, now)
		if !ok {
			continue
		}
		c.keys[mark] = expires
		if h.keys[mark] == nil {
			h.keys[mark] = map[*changeChannel]struct{}{}
		}
		h.keys[mark][c] = struct{}{}
		if h.last[mark] > last {
			last = h.last[mark]
		}
		taken++
	}
	return last, taken, true
}

// tell tells every channel holding a ticket for key, not run out, the stamp of the change. A channel not read
// yet keeps the newest stamp only: one ask covers every change before it.
func (h *changeHub) tell(key string, stamp int64) {
	if h == nil {
		return
	}
	mark := h.mark(key)
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if stamp > h.last[mark] {
		h.last[mark] = stamp
	}
	for c := range h.keys[mark] {
		if !now.Before(c.keys[mark]) {
			continue
		}
		newest := stamp
		select {
		case waiting := <-c.stamp:
			if waiting > newest {
				newest = waiting
			}
		default:
		}
		c.stamp <- newest // the one sender, under h.mu, after the channel was emptied: never waits
	}
}

// runsOut is when the first ticket of the channel runs out, zero when it holds none.
func (h *changeHub) runsOut(c *changeChannel) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	var first time.Time
	for _, expires := range c.keys {
		if first.IsZero() || expires.Before(first) {
			first = expires
		}
	}
	return first
}

// ── the keys a page reads ──────────────────────────────────────────────────────────────────────────

// viewPageChangeKey is the key of a page itself: what its jobs change, whatever they write.
func (t *Tenant) viewPageChangeKey(leaf *FolderRouter) string {
	return "page\x00" + t.Domain() + "\x00" + viewPageKey(leaf)
}

func storageChangeKey(place string, encoded string) string {
	return "storage\x00" + place + "\x00" + encoded
}

// readBy notes that the run of reqRouter read a key of storage, when it is a page's run.
func (r *Router) readBy(key string) {
	if r != nil && r.reads != nil {
		r.reads[key] = true
	}
}

// viewTickets are the tickets of the page's run: the page and every key it read.
func (t *Tenant) viewTickets(reqRouter *Router, leaf *FolderRouter) []string {
	hub := t.changes()
	if hub == nil {
		return nil
	}
	keys := []string{t.viewPageChangeKey(leaf)}
	for key := range reqRouter.reads {
		keys = append(keys, key)
	}
	return hub.tickets(reqRouter.request, keys)
}

// answerTickets puts in an answer the tickets of the run, unless the page holds tickets for the same keys made
// lately (held: the marks it sent, sorted, call.js): the answer of a ticked box stays small.
func (t *Tenant) answerTickets(answer map[string]any, reqRouter *Router, leaf *FolderRouter, held string) {
	tickets := t.viewTickets(reqRouter, leaf)
	if held != "" && ticketMarks(tickets) == held {
		return
	}
	answer["tickets"] = tickets
}

// ticketMarks are the marks of tickets, sorted, one space between.
func ticketMarks(tickets []string) string {
	marks := make([]string, len(tickets))
	for i, ticket := range tickets {
		marks[i], _, _ = strings.Cut(ticket, ".")
	}
	sort.Strings(marks)
	return strings.Join(marks, " ")
}

// tellViewChanged tells the tabs holding a ticket for the page that a job changed its data.
func (t *Tenant) tellViewChanged(leaf *FolderRouter, stamp int64) {
	t.changes().tell(t.viewPageChangeKey(leaf), stamp)
}

// viewDataChanged reports whether a name of a page's data, a var's aside, differs between two runs.
func viewDataChanged(before, after map[string]any, vars map[string]bool) bool {
	for name, now := range after {
		if vars[name] {
			continue
		}
		if was, had := before[name]; !had || revOf(was) != revOf(now) {
			return true
		}
	}
	for name := range before {
		if _, kept := after[name]; !kept && !vars[name] {
			return true
		}
	}
	return false
}

// ── the channel ────────────────────────────────────────────────────────────────────────────────────

// serveChangesIf answers /_kitwork/changes: GET opens a tab's channel, POST hands it its tickets.
func (t *Tenant) serveChangesIf(w http.ResponseWriter, r *http.Request, done <-chan struct{}) bool {
	if r == nil || r.URL.Path != changesPath {
		return false
	}
	switch r.Method {
	case http.MethodGet:
		t.serveChanges(w, r, done)
	case http.MethodPost:
		t.holdTickets(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
	return true
}

func (t *Tenant) serveChanges(w http.ResponseWriter, r *http.Request, done <-chan struct{}) {
	if r.Header.Get("X-Kitwork-Native") != "" {
		// a native app's shell answers a request whole, it holds no stream: 204, and the browser does not ask again
		w.WriteHeader(http.StatusNoContent)
		return
	}
	hub := t.changes()
	app, err := t.storage()
	if hub == nil || err != nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	if !app.streams.enter() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Service Unavailable: too many open streams", http.StatusServiceUnavailable)
		return
	}
	defer app.streams.leave()
	stream := &eventStream{done: make(chan struct{}), reconnect: viewChangesRetry}
	defer stream.finish()
	if !t.addStream(stream) {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	defer t.removeStream(stream)
	channel := hub.open()
	defer hub.close(channel)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	if !stream.open(w) || !stream.write([]byte("event: connection\ndata: "+channel.id+"\n\n")) {
		return
	}
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	renewed := time.Time{}
	for {
		select {
		case <-done:
			return
		case <-stream.done:
			return
		case stamp := <-channel.stamp:
			if !stream.write([]byte("data: " + strconv.FormatInt(stamp, 10) + "\n\n")) {
				return
			}
		case now := <-heartbeat.C:
			frame := ": heartbeat\n\n"
			// the tab's tickets run out within a beat: it asks again for its page, and holds new ones
			if first := hub.runsOut(channel); !first.IsZero() && first.Sub(now) < 2*streamHeartbeat && !first.Equal(renewed) {
				frame, renewed = "event: renew\ndata: \n\n", first
			}
			if !stream.write([]byte(frame)) {
				return
			}
		}
	}
}

// holdTickets is the POST of a tab handing its channel the tickets of the page it shows now: { "last": n } the
// last stamp told for any of them, 404 for a channel that is not open (the tab opens another).
func (t *Tenant) holdTickets(w http.ResponseWriter, r *http.Request) {
	if err := crossOriginRefusal(r); err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	hub := t.changes()
	if hub == nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	var sent struct {
		Tickets []string `json:"tickets"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, changesBodyLimit+1))
	if err != nil || len(body) > changesBodyLimit || json.Unmarshal(body, &sent) != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	last, taken, found := hub.hold(r.URL.Query().Get("connection"), sent.Tickets, cookiesMark(r))
	if !found {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]int64{"last": last, "taken": int64(taken)})
}
