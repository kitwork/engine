package work

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kitwork/engine/ui"
	"github.com/kitwork/engine/value"
)

// What a job of router.view's page sends back (28/10/2026, docs/KITWORK_BRIDGE.md): not the page, the data
// it changed. The view's function runs before the job and again after it; what differs between the two
// objects it bound goes back by instance (29/10/2026, mục 3), each name set straight into it, and a list the
// page draws with a key (for="todo of todos; key: todo.id") by row, name#key:
//
//	{ "seq": 4, "page": { "left": 1, "todos#45": { "id": 45, "done": true, … }, "todos#46": null } }
//
// A name whose value is new goes whole (null for a name the page no longer has); a row set goes whole (new,
// at the end, or changed in place), a row gone is null. A list goes whole when that is shorter, or when the
// rows that stay changed their order.

// A page's revisions (28/10/2026): each name of its data has one, a hash of its value as JSON, written with the
// page and sent back with every call. The run before the job is what the patch is computed from; a page whose
// revision of a name is not that run's held another value (another tab changed the data since it was drawn),
// and a patch made for the run before would leave it half new: the count of one list, the rows of another.
// Such a page gets that name's value whole, and every page gets the new revision of what changed.

// revOf is the revision of a value read back from JSON: ui.Revision, which the page writes an instance's data
// with as it draws it, so the two agree.
func revOf(v any) string { return ui.Revision(v) }

// viewRevs are the revisions of a run's data, by name.
func viewRevs(snapshot map[string]any) map[string]string {
	revs := make(map[string]string, len(snapshot))
	for name, v := range snapshot {
		revs[name] = revOf(v)
	}
	return revs
}

// staleRevs makes an instance's changes right for a tab that held the revisions held: a name it held at another
// revision than the run before the job is sent whole, a name it already holds at the new one is not sent, and the
// new revision of every other name the tab does not hold yet is returned. vars are the instance's own: never sent
// whole, never given a revision.
func staleRevs(held map[string]string, before, after map[string]any, vars map[string]bool, changes map[string]any) map[string]string {
	revs := map[string]string{}
	for name, now := range after {
		if vars[name] {
			continue
		}
		rev := revOf(now)
		if held[name] == rev {
			delete(changes, name)
			dropRows(changes, name)
			continue
		}
		revs[name] = rev
		if was, had := before[name]; !had || held[name] != revOf(was) {
			dropRows(changes, name)
			changes[name] = now
		}
	}
	for name := range before {
		if _, kept := after[name]; !kept && !vars[name] {
			revs[name] = ""
		}
	}
	return revs
}

// dropRows takes out of an instance's changes the rows of a list (name#key).
func dropRows(changes map[string]any, name string) {
	for key := range changes {
		if strings.HasPrefix(key, name+"#") {
			delete(changes, key)
		}
	}
}

// viewSnapshot is the data of one run of the view's function: each name the page can read that is no
// function, as JSON reads it back, so a job that changes a value in place still differs from what was
// drawn before it.
func viewSnapshot(data value.Value) map[string]any {
	out := map[string]any{}
	if !data.IsMap() {
		return out
	}
	for name, v := range data.Map() {
		if !visibleName(name) || v.K == value.Func {
			continue
		}
		b, err := v.MarshalJSON()
		if err != nil {
			continue
		}
		var back any
		if json.Unmarshal(b, &back) == nil {
			out[name] = back
		}
	}
	return out
}

// viewPatch is what changed from before to after in one instance, as the wire carries it (docs/KITWORK_BRIDGE.md
// mục 3): each name whose value is new, whole (null for a name the instance no longer has), and a list drawn by key
// as its rows, name#key, each the row as it is now or null for one gone (listPatch). keys are the key of each list
// the instance draws by key, as a path into a row ("id", "author.id").
func viewPatch(before, after map[string]any, keys map[string]string) map[string]any {
	changes := map[string]any{}
	for name, now := range after {
		was, had := before[name]
		if had && sameValue(was, now) {
			continue
		}
		if key := keys[name]; had && key != "" {
			if rows, ok := listPatch(name, was, now, key); ok {
				for k, row := range rows {
					changes[k] = row
				}
				continue
			}
		}
		changes[name] = now
	}
	for name := range before {
		if _, kept := after[name]; !kept {
			changes[name] = nil
		}
	}
	return changes
}

// listPatch is a keyed list's change by row, name#key: each row set (new, or changed: whole) and each row gone
// (null); the browser keeps the rows it has in their order, puts each new one at the end and takes out each one
// gone. False when either list is not one (a row with no key, two rows with one key), when the rows that stay
// changed their order (sorted again: the list goes whole), or when the whole list is shorter.
func listPatch(name string, was, now any, key string) (map[string]any, bool) {
	before, ok := was.([]any)
	if !ok {
		return nil, false
	}
	after, ok := now.([]any)
	if !ok {
		return nil, false
	}
	path := strings.Split(key, ".")
	oldKeys, oldRows, ok := keyedRows(before, path)
	if !ok {
		return nil, false
	}
	newKeys, newRows, ok := keyedRows(after, path)
	if !ok {
		return nil, false
	}
	rows := map[string]any{}
	kept := map[string]bool{}
	for _, k := range oldKeys {
		if _, stays := newRows[k]; stays {
			kept[k] = true
		} else {
			rows[name+"#"+k] = nil
		}
	}
	expected := make([]string, 0, len(newKeys))
	for _, k := range oldKeys {
		if kept[k] {
			expected = append(expected, k)
		}
	}
	for i, k := range newKeys {
		if old, had := oldRows[k]; !had {
			expected = append(expected, k)
			rows[name+"#"+k] = after[i]
		} else if !sameValue(old, after[i]) {
			rows[name+"#"+k] = after[i]
		}
	}
	if !sameKeys(expected, newKeys) {
		return nil, false
	}
	byRow, err1 := json.Marshal(rows)
	whole, err2 := json.Marshal(map[string]any{name: now})
	if err1 != nil || err2 != nil || len(byRow) >= len(whole) {
		return nil, false
	}
	return rows, true
}

// keyedRows reads each row's key as text (the way the kernel compares a key: String(key)), in order, and
// the rows by key; false when a row has no key or two rows share one.
func keyedRows(list []any, path []string) ([]string, map[string]any, bool) {
	keys := make([]string, 0, len(list))
	rows := make(map[string]any, len(list))
	for _, row := range list {
		k, ok := keyText(rowKey(row, path))
		if !ok {
			return nil, nil, false
		}
		if _, twice := rows[k]; twice {
			return nil, nil, false
		}
		keys = append(keys, k)
		rows[k] = row
	}
	return keys, rows, true
}

// rowKey is the value at path in a row, nil when there is none.
func rowKey(row any, path []string) any {
	v := row
	for _, part := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[part]
	}
	return v
}

// keyText is a key as the kernel compares it, String(key): a number as JavaScript writes it, a string as
// it is; false for anything else.
func keyText(k any) (string, bool) {
	switch v := k.(type) {
	case string:
		return v, true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameValue reports whether two values read back from JSON write the same JSON.
func sameValue(a, b any) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}
