package work

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The history keeps app.history({ days }) days (KITWORK_KITDB.md câu 13.6): the writer of an app's file anchors it once
// a day of commits is in no anchor and takes out what no moment of the days kept needs, the copies at() made of it
// included. A moment before what is kept is refused in words that say how long the engine keeps and where that is
// said; a later moment reads as before; history() holds the kept commits alone.
//
// CONTROL (03/11/2026): with ErrPruned said as any failure of at(), "at the first insert = … want the error that says
// how long the history keeps"; with the copies of at() left behind, "the copy at() made of revision 2 is still on
// disk".
func TestManagedDatabaseHistoryRetention(t *testing.T) {
	SetHistoryDays(1)
	t.Cleanup(func() { SetHistoryDays(0) })
	managed := newManagedApp(t, map[string]string{".shared/schema/shop.work": writeShopSchema, "shop.example/router.work": historyShopRoute})
	tenant := managed.site("shop.example")
	post := func(email, item, amount string) {
		t.Helper()
		if status, body := managedPost(tenant, "/", url.Values{"email": {email}, "item": {item}, "amount": {amount}}); status != http.StatusOK {
			t.Fatalf("insert %s = %d %s", email, status, body)
		}
	}
	var answer map[string]any
	post("a@x.vn", "Cà phê", "35000")
	getJSON(t, tenant, "/?do=update&email=a@x.vn", &answer)
	post("b@x.vn", "Trà đào", "40000")
	path := filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db")
	_, moments := historyOf(t, path)
	at := func(revision uint64) string {
		return "/?do=at&at=" + url.QueryEscape(moments[revision].Format(time.RFC3339Nano))
	}
	getJSON(t, tenant, at(2), &answer)
	if _, err := os.Stat(filepath.Join(path+".at", "00000000000000000002.db")); err != nil {
		t.Fatalf("at() of revision 2 made no copy: %v", err)
	}

	absolute, _ := filepath.Abs(path)
	historyWriters.Lock()
	entry := historyWriters.files[filepath.Clean(absolute)]
	historyWriters.Unlock()
	if entry == nil {
		t.Fatal("the file has no writer")
	}
	entry.retain(context.Background(), moments[4].Add(25*time.Hour))

	var refused struct {
		OK    bool `json:"ok"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	getJSON(t, tenant, "/?do=before&at="+url.QueryEscape(moments[2].Format(time.RFC3339Nano)), &refused)
	if refused.OK || !strings.Contains(refused.Error.Message, "lịch sử của database chỉ còn từ") ||
		!strings.Contains(refused.Error.Message, "engine giữ 1 ngày (app.history({ days }) trong application.work, mặc định 30)") {
		t.Fatalf("at the first insert = %+v, want the error that says how long the history keeps", refused)
	}
	if _, err := os.Stat(filepath.Join(path+".at", "00000000000000000002.db")); err == nil {
		t.Fatal("the copy at() made of revision 2 is still on disk")
	}
	var read struct {
		Rows  []map[string]any `json:"rows"`
		Count float64          `json:"count"`
	}
	getJSON(t, tenant, at(4), &read)
	if read.Count != 2 {
		t.Fatalf("at revision 4 = %v, want both orders", read.Rows)
	}
	var changes []map[string]any
	getJSON(t, tenant, "/?do=history&email=a@x.vn", &changes)
	if len(changes) != 0 {
		t.Fatalf("history of a@x.vn = %v, want nothing: its insert and update are older than what is kept", changes)
	}
	getJSON(t, tenant, "/?do=history&email=b@x.vn", &changes)
	if len(changes) != 1 || changes[0]["before"] != nil {
		t.Fatalf("history of b@x.vn = %v, want its insert, the commit the kept anchor holds", changes)
	}
}
