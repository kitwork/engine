package work

import (
	"encoding/json"
	"strings"
)

// callBody is a call of a job, or an ask for what changed, as call.js sends it (29/10/2026,
// docs/KITWORK_BRIDGE.md mục 3): by instance, the vars each holds under its place, the page's under "page", the
// bridge's own bookkeeping apart.
type callBody struct {
	Seq  float64
	Args []any
	// At is the place of the instance whose job it is, "" for a job of the page
	At string
	// Page is the vars the page holds, nil for none; Mine those of the instance at At
	Page, Mine map[string]any
	// Revs are the revisions the tab holds, by place
	Revs  map[string]map[string]string
	Marks string
}

func (c callBody) String() string {
	body := map[string]any{"seq": c.Seq}
	if c.Args != nil {
		body["args"] = c.Args
	}
	if c.Page != nil {
		body[pagePlace] = c.Page
	}
	if c.At != "" {
		body["at"] = c.At
		mine := c.Mine
		if mine == nil {
			mine = map[string]any{}
		}
		body[c.At] = mine
	}
	bridge := map[string]any{}
	if c.Revs != nil {
		bridge["revs"] = c.Revs
	}
	if c.Marks != "" {
		bridge["marks"] = c.Marks
	}
	if len(bridge) > 0 {
		body["bridge"] = bridge
	}
	data, _ := json.Marshal(body)
	return string(data)
}

// vars is a map of names, for a callBody: vars("title", "", "done", true).
func vars(pairs ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out[pairs[i].(string)] = pairs[i+1]
	}
	return out
}

// instanceOf is the changes an answer brings for the instance at a place: its names, and its keyed lists' rows as
// name#key; nil when it brings none.
func instanceOf(answer map[string]any, place string) map[string]any {
	changes, _ := answer[place].(map[string]any)
	return changes
}

// bridgeOf is the bridge's own part of an answer: revisions, stamp, tickets.
func bridgeOf(answer map[string]any) map[string]any {
	bridge, _ := answer["bridge"].(map[string]any)
	if bridge == nil {
		return map[string]any{}
	}
	return bridge
}

// revsOf is the revisions an answer brings for the instance at a place, nil when it brings none.
func revsOf(answer map[string]any, place string) map[string]any {
	revs, _ := bridgeOf(answer)["revs"].(map[string]any)
	held, _ := revs[place].(map[string]any)
	return held
}

// rowsIn is the rows an answer brings for a keyed list of an instance, by key: each the row set, nil for one gone.
func rowsIn(changes map[string]any, name string) map[string]any {
	rows := map[string]any{}
	for key, row := range changes {
		if k, ok := strings.CutPrefix(key, name+"#"); ok {
			rows[k] = row
		}
	}
	return rows
}
