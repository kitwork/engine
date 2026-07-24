package work

import (
	insightshelper "github.com/kitwork/engine/utilities/insights"
	"github.com/kitwork/engine/value"
)

type Insights struct {
	tenant *Tenant
}

func (w *KitWork) Insights() *Insights {
	return &Insights{tenant: w.tenant}
}

func (in *Insights) store() *insightshelper.Store {
	db := sqliteFor(in.tenant, "insights.db").db()
	return insightshelper.NewStore(db)
}

func (in *Insights) Search(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Nil}
	}
	q := args[0].String()
	results := 0
	if len(args) > 1 {
		results = int(args[1].N)
	}

	st := in.store()
	if st == nil {
		return value.Value{K: value.Invalid, V: "insights: store unavailable"}
	}

	if err := st.RecordSearch(q, results); err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.Value{K: value.Nil}
}

func (in *Insights) Gaps(args ...value.Value) value.Value {
	limit := 50
	if len(args) > 0 && args[0].N > 0 {
		limit = int(args[0].N)
	}
	st := in.store()
	if st == nil {
		return value.Value{K: value.Invalid, V: "insights: store unavailable"}
	}
	records, err := st.Gaps(limit)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return recordsToValue(records)
}

func (in *Insights) Searches(args ...value.Value) value.Value {
	limit := 50
	if len(args) > 0 && args[0].N > 0 {
		limit = int(args[0].N)
	}
	st := in.store()
	if st == nil {
		return value.Value{K: value.Invalid, V: "insights: store unavailable"}
	}
	records, err := st.Searches(limit)
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return recordsToValue(records)
}

func recordsToValue(records []insightshelper.SearchRecord) value.Value {
	out := make([]map[string]any, len(records))
	for i, r := range records {
		out[i] = map[string]any{
			"query":       r.Query,
			"total":       r.Total,
			"misses":      r.Misses,
			"resultsLast": r.ResultsLast,
			"lastSeen":    r.LastSeen,
		}
	}
	return collectionValue(out)
}
