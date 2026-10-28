package work

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The page told when its data changes (28/10/2026): a tab of a page of router.view that holds its data in the
// browser listens to the page while it is seen, GET /todo?kitwork-listen=1, past the folder's guards and the
// page's. The stream (SSE) carries no data, only the stamp of the data a job of the page left (nextViewStamp):
//
//	data: 1793174400123456
//
// A job that changes the page's data, a var's aside, tells every tab listening to the page its stamp, while it
// holds the page's lock; a tab that holds data read before asks for what changed (kitwork-refresh), with its
// own cookies and past the guards, and takes only what changed (call.js). A tab that connects, or connects
// again after the stream was cut, is told the last stamp at once: what changed while it was not listening.
// The view's function does not run for a listener, and nothing of the page goes but when it changed.
//
// What is told is a page's own jobs: another page that writes the same data, or a write from outside the
// engine, is seen when the tab asks again (seen again, a job). A native app's shell holds no stream: its
// listener is answered 204, and the browser does not ask again.

// ViewListenParameter asks to listen to a page: GET /todo?kitwork-listen=1.
const ViewListenParameter = "kitwork-listen"

// viewListenRetry is how long a browser waits before it connects again to a stream that was cut.
var viewListenRetry = 3 * time.Second

// takeViewListen takes kitwork-listen out of a GET's URL before anything reads it, and reports whether it was
// there.
func takeViewListen(r *http.Request) bool {
	_, listen := takeFromQuery(r, ViewListenParameter)[ViewListenParameter]
	return listen
}

// viewListeners are the tabs listening to one page, each a channel of one stamp, and the last stamp told.
type viewListeners struct {
	mu   sync.Mutex
	tabs map[chan int64]struct{}
	last int64
}

func (t *Tenant) viewListenersOf(leaf *FolderRouter) *viewListeners {
	l, _ := t.viewListen.LoadOrStore(viewPageKey(leaf), &viewListeners{tabs: map[chan int64]struct{}{}})
	return l.(*viewListeners)
}

// listen adds a tab and answers its channel and the last stamp told.
func (l *viewListeners) listen() (chan int64, int64) {
	tab := make(chan int64, 1)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tabs[tab] = struct{}{}
	return tab, l.last
}

func (l *viewListeners) leave(tab chan int64) {
	l.mu.Lock()
	delete(l.tabs, tab)
	l.mu.Unlock()
}

// tellViewChanged tells the tabs listening to the page the stamp of the data a job left. A tab not read yet
// keeps the newest stamp only: one ask covers every change before it.
func (t *Tenant) tellViewChanged(leaf *FolderRouter, stamp int64) {
	l := t.viewListenersOf(leaf)
	l.mu.Lock()
	defer l.mu.Unlock()
	if stamp > l.last {
		l.last = stamp
	}
	for tab := range l.tabs {
		newest := stamp
		select {
		case waiting := <-tab:
			if waiting > newest {
				newest = waiting
			}
		default:
		}
		tab <- newest // the one sender, under l.mu, after the channel was emptied: never waits
	}
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

// serveViewListen holds the stream of a tab listening to its page until the browser goes, the generation is
// replaced or the server stops. It counts with the app's streams (stream.go): one past the ceiling is 503.
func (t *Tenant) serveViewListen(w http.ResponseWriter, reqRouter *Router, leaf *FolderRouter) {
	if reqRouter.request.Header.Get("X-Kitwork-Native") != "" {
		// a native app's shell answers a request whole, it holds no stream: 204, and the browser does not ask again
		w.WriteHeader(http.StatusNoContent)
		return
	}
	app, err := t.storage()
	if err != nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	if !app.streams.enter() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Service Unavailable: too many open streams", http.StatusServiceUnavailable)
		return
	}
	defer app.streams.leave()
	stream := &eventStream{done: make(chan struct{}), reconnect: viewListenRetry}
	defer stream.finish()
	if !t.addStream(stream) {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	defer t.removeStream(stream)
	tabs := t.viewListenersOf(leaf)
	tab, last := tabs.listen()
	defer tabs.leave(tab)

	streamHeaders(w, reqRouter)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	if !stream.open(w) {
		return
	}
	if last > 0 && !stream.write(viewChangedFrame(last)) {
		return
	}
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	gone := reqRouter.requestScope.Context().Done()
	for {
		select {
		case <-gone:
			return
		case <-stream.done:
			return
		case stamp := <-tab:
			if !stream.write(viewChangedFrame(stamp)) {
				return
			}
		case <-heartbeat.C:
			if !stream.write([]byte(": heartbeat\n\n")) {
				return
			}
		}
	}
}

func viewChangedFrame(stamp int64) []byte {
	return []byte("data: " + strconv.FormatInt(stamp, 10) + "\n\n")
}
