package http

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	stdhttp "net/http"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

// Outbound response caching: the same .cache()/.persist() a route speaks (KITWORK_HTTP.md mục 7), on a
// GET only, after the method:
//
//	http.get(url).cache("5m").send()               // RAM, read-through
//	http.get(url).persist("1d").send()             // disk: survives restarts
//	http.get(url).cache("5m").persist("1d").send() // both tiers
//
// Only an ok answer is stored. STALE ON FAILURE: when the live request gets no answer or a 5xx and a
// persisted copy exists, even an expired one, that copy is the result with `stale: true`, so a page
// never breaks because a third party is down.
//
// The stores are INJECTED by the host per site (NewModule); this package stays pure. With no store
// injected, .cache()/.persist() store nothing.

// Snapshot is the storable form of an answer: status, the body's bytes and the upstream's media type,
// which a cached result gives back as its one header.
type Snapshot struct {
	Status      int
	Body        []byte
	ContentType string
}

// ResponseStore is one storage tier. Load returns a FRESH snapshot only; LoadStale may return an
// expired-but-present snapshot (disk tier) for fail-open serving; a RAM tier just returns false.
type ResponseStore interface {
	Load(key string) (Snapshot, bool)
	LoadStale(key string) (Snapshot, bool)
	Save(key string, s Snapshot, ttl time.Duration)
}

// ttlOf reads the expiry of .cache()/.persist() as a route reads one (KITWORK_ROUTER.md mục 9): no
// argument is no expiry (RAM until the app is rebuilt), a number is milliseconds, a string a duration
// ("30s", "5m", "1h", "7d"). Anything else, and a string that does not read, is the program's mistake.
func ttlOf(modifier string, args []value.Value) (time.Duration, value.Value) {
	if len(args) == 0 {
		return 0, value.Value{}
	}
	if len(args) > 1 {
		return 0, value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf(".%s() nhận một hạn: .%s(\"5m\")", modifier, modifier))
	}
	v := args[0]
	switch {
	case v.K == value.Number && v.N > 0:
		return time.Duration(v.N) * time.Millisecond, value.Value{}
	case v.K == value.String:
		if d, err := parseDuration(v.String()); err == nil && d > 0 {
			return d, value.Value{}
		}
	}
	return 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(
		".%s(): không đọc được hạn %s: viết như \"30s\", \"5m\", \"1h\", \"7d\" hay một số mili giây; hạn theo lịch (\"hourly\", \"daily\") chưa có ở http",
		modifier, written(v)))
}

// requestKey identifies a GET by its final URL and the headers it carries (names sorted, so the order
// they were set in is irrelevant).
func requestKey(url string, header stdhttp.Header) string {
	hash := sha256.New()
	hash.Write([]byte(url))
	if len(header) > 0 {
		names := make([]string, 0, len(header))
		for name := range header {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			hash.Write([]byte("|" + name + ":" + strings.Join(header[name], ",")))
		}
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

// storedOutcome rebuilds an answer from a snapshot, flagged with where it came from.
func storedOutcome(s Snapshot, stale bool) Outcome {
	var header stdhttp.Header
	if s.ContentType != "" {
		header = stdhttp.Header{"Content-Type": {s.ContentType}}
	} else {
		header = stdhttp.Header{}
	}
	return Outcome{Status: s.Status, Header: header, Body: s.Body, Cached: true, Stale: stale}
}
