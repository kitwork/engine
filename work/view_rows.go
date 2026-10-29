package work

import "sync"

// The rows that changed since the revision a tab holds (28/10/2026): a tab that asks for what changed
// (kitwork-refresh), or calls a job holding a list older than the job ran on, held a keyed list at a revision
// of its own. The server keeps the last versions of each list a page draws by key, by revision, as its runs
// read them; a tab holding one of them gets the list's rows that changed since then (name#key, a row gone
// null), as a job's answer gives them, not the list whole. A tab holding a revision no longer kept gets the
// list whole, as before.

// viewRowsKept is how many versions of one list of one page are kept.
const viewRowsKept = 8

// viewRows are the versions kept of the keyed lists of one page, by name, the newest last.
type viewRows struct {
	mu   sync.Mutex
	kept map[string][]keptList
}

type keptList struct {
	rev   string
	value any
}

func (t *Tenant) viewRowsOf(leaf *FolderRouter) *viewRows {
	rows, _ := t.viewRows.LoadOrStore(viewPageKey(leaf), &viewRows{kept: map[string][]keptList{}})
	return rows.(*viewRows)
}

// keepViewRows keeps the version of each keyed list of the page a run read (snapshot: as JSON reads it back,
// never changed after).
func (t *Tenant) keepViewRows(leaf *FolderRouter, snapshot map[string]any) {
	if leaf == nil {
		return
	}
	keys := t.viewListKeys(leaf.node)
	if len(keys) == 0 {
		return
	}
	rows := t.viewRowsOf(leaf)
	rows.mu.Lock()
	defer rows.mu.Unlock()
	for name := range keys {
		value, ok := snapshot[name]
		if !ok {
			continue
		}
		rev := revOf(value)
		versions := rows.kept[name]
		if n := len(versions); n > 0 && versions[n-1].rev == rev {
			continue
		}
		versions = append(versions, keptList{rev: rev, value: value})
		if len(versions) > viewRowsKept {
			versions = versions[len(versions)-viewRowsKept:]
		}
		rows.kept[name] = versions
	}
}

// keptAt is the version of a list of the page kept at a revision.
func (t *Tenant) keptAt(leaf *FolderRouter, name, rev string) (any, bool) {
	rows := t.viewRowsOf(leaf)
	rows.mu.Lock()
	defer rows.mu.Unlock()
	for _, version := range rows.kept[name] {
		if version.rev == rev {
			return version.value, true
		}
	}
	return nil, false
}

// rowsSince puts in an instance's changes, for each keyed list they carry whole, its rows changed since the
// revision the tab holds, when that version is kept.
func (t *Tenant) rowsSince(leaf *FolderRouter, held map[string]string, changes map[string]any) {
	if leaf == nil || len(held) == 0 {
		return
	}
	keys := t.viewListKeys(leaf.node)
	var whole []string
	for name := range changes {
		if keys[name] != "" && held[name] != "" {
			whole = append(whole, name)
		}
	}
	for _, name := range whole {
		was, kept := t.keptAt(leaf, name, held[name])
		if !kept {
			continue
		}
		if rows, ok := listPatch(name, was, changes[name], keys[name]); ok {
			delete(changes, name)
			for k, row := range rows {
				changes[k] = row
			}
		}
	}
}
