package work

import (
	collectionhelper "github.com/kitwork/engine/utilities/collection"
	"github.com/kitwork/engine/value"
)

// CollectionQuery is the fluent filter/sort/slice chain over a collection's frontmatter index:
//
//	posts.where("status", "published").orderBy("publishedAt", "desc").limit(20).all()
//
// Same vocabulary as the database builder (where/orderBy/limit/skip), but it runs ENTIRELY on the
// RAM-cached index (signature-invalidated) — no SQL, no injection surface, microsecond cost. Full-text
// is the one operation that leaves RAM: posts.search("...") uses the FTS index instead.
type CollectionQuery struct {
	handle *CollectionHandle
	spec   collectionhelper.Query
}

// Chain starters on the handle — each begins a fresh query.

func (h *CollectionHandle) Where(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).Where(args...)
}

func (h *CollectionHandle) OrderBy(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).OrderBy(args...)
}

func (h *CollectionHandle) Limit(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).Limit(args...)
}

func (h *CollectionHandle) Skip(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).Skip(args...)
}

// Where adds one filter. Two args = equality: where("status", "published"). Three args = operator:
// where("views", ">", 100). Operators: = != > >= < <= contains (element of a list, or substring).
func (cq *CollectionQuery) Where(args ...value.Value) *CollectionQuery {
	switch len(args) {
	case 2:
		cq.spec.Filters = append(cq.spec.Filters, collectionhelper.Filter{
			Field: args[0].String(), Op: "=", Value: args[1].Interface(),
		})
	case 3:
		cq.spec.Filters = append(cq.spec.Filters, collectionhelper.Filter{
			Field: args[0].String(), Op: args[1].String(), Value: args[2].Interface(),
		})
	}
	return cq
}

func (cq *CollectionQuery) OrderBy(args ...value.Value) *CollectionQuery {
	if len(args) > 0 {
		cq.spec.OrderField = args[0].String()
	}
	cq.spec.OrderDesc = len(args) > 1 && args[1].String() == "desc"
	return cq
}

func (cq *CollectionQuery) Limit(args ...value.Value) *CollectionQuery {
	if len(args) > 0 {
		cq.spec.LimitN = int(args[0].N)
	}
	return cq
}

func (cq *CollectionQuery) Skip(args ...value.Value) *CollectionQuery {
	if len(args) > 0 {
		cq.spec.SkipN = int(args[0].N)
	}
	return cq
}

// Terminals. 0-arg like the handle's own All/Index, so both `.all()` and getter-style `.all` work.

func (cq *CollectionQuery) All() value.Value {
	entries, err := cq.run()
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(entries)
}

func (cq *CollectionQuery) First() value.Value {
	entries, err := cq.run()
	if err != nil {
		return collectionInvalid(err)
	}
	if len(entries) == 0 {
		return value.Value{K: value.Nil}
	}
	return collectionValue(entries[0])
}

func (cq *CollectionQuery) Count() value.Value {
	entries, err := cq.run()
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(len(entries))
}

func (cq *CollectionQuery) run() ([]collectionhelper.IndexEntry, error) {
	index, err := cq.handle.collection.Index()
	if err != nil {
		return nil, err
	}
	return cq.spec.Apply(index), nil
}
