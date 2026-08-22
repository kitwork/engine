package analytics

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// AggregateKind declares the supported aggregate functions.
type AggregateKind uint8

const (
	AggCount AggregateKind = iota
	AggSumInt64
	AggSumFloat64
	AggMinInt64
	AggMaxInt64
	AggMinFloat64
	AggMaxFloat64
	AggAvgInt64
	AggAvgFloat64
)

// Aggregate configures one grouped measure.
type Aggregate struct {
	Kind   AggregateKind
	Column string
	Alias  string
}

// Count returns a COUNT(*) aggregate.
func Count(alias string) Aggregate {
	return Aggregate{Kind: AggCount, Alias: aliasOrDefault(alias, "count")}
}

// SumInt64 returns a SUM(column) aggregate.
func SumInt64(column, alias string) Aggregate {
	return Aggregate{Kind: AggSumInt64, Column: column, Alias: aliasOrDefault(alias, "sum_"+column)}
}

// SumFloat64 returns a SUM(column) aggregate.
func SumFloat64(column, alias string) Aggregate {
	return Aggregate{Kind: AggSumFloat64, Column: column, Alias: aliasOrDefault(alias, "sum_"+column)}
}

// MinInt64 returns a MIN(column) aggregate.
func MinInt64(column, alias string) Aggregate {
	return Aggregate{Kind: AggMinInt64, Column: column, Alias: aliasOrDefault(alias, "min_"+column)}
}

// MaxInt64 returns a MAX(column) aggregate.
func MaxInt64(column, alias string) Aggregate {
	return Aggregate{Kind: AggMaxInt64, Column: column, Alias: aliasOrDefault(alias, "max_"+column)}
}

// MinFloat64 returns a MIN(column) aggregate.
func MinFloat64(column, alias string) Aggregate {
	return Aggregate{Kind: AggMinFloat64, Column: column, Alias: aliasOrDefault(alias, "min_"+column)}
}

// MaxFloat64 returns a MAX(column) aggregate.
func MaxFloat64(column, alias string) Aggregate {
	return Aggregate{Kind: AggMaxFloat64, Column: column, Alias: aliasOrDefault(alias, "max_"+column)}
}

// AvgInt64 returns an AVG(column) aggregate.
func AvgInt64(column, alias string) Aggregate {
	return Aggregate{Kind: AggAvgInt64, Column: column, Alias: aliasOrDefault(alias, "avg_"+column)}
}

// AvgFloat64 returns an AVG(column) aggregate.
func AvgFloat64(column, alias string) Aggregate {
	return Aggregate{Kind: AggAvgFloat64, Column: column, Alias: aliasOrDefault(alias, "avg_"+column)}
}

func aliasOrDefault(alias, fallback string) string {
	if alias != "" {
		return alias
	}
	return fallback
}

type aggregateState struct {
	kind     AggregateKind
	count    uint64
	sumInt   int64
	sumFloat float64
	hasInt   bool
	hasFloat bool
	minInt   int64
	maxInt   int64
	minFloat float64
	maxFloat float64
}

func (state *aggregateState) add(value any, ok bool) {
	switch state.kind {
	case AggCount:
		state.count++
	case AggSumInt64:
		if !ok {
			return
		}
		state.sumInt += value.(int64)
	case AggSumFloat64:
		if !ok {
			return
		}
		state.sumFloat += value.(float64)
	case AggMinInt64:
		if !ok {
			return
		}
		typed := value.(int64)
		if !state.hasInt || typed < state.minInt {
			state.minInt = typed
			state.hasInt = true
		}
	case AggMaxInt64:
		if !ok {
			return
		}
		typed := value.(int64)
		if !state.hasInt || typed > state.maxInt {
			state.maxInt = typed
			state.hasInt = true
		}
	case AggMinFloat64:
		if !ok {
			return
		}
		typed := value.(float64)
		if !state.hasFloat || typed < state.minFloat {
			state.minFloat = typed
			state.hasFloat = true
		}
	case AggMaxFloat64:
		if !ok {
			return
		}
		typed := value.(float64)
		if !state.hasFloat || typed > state.maxFloat {
			state.maxFloat = typed
			state.hasFloat = true
		}
	case AggAvgInt64:
		if !ok {
			return
		}
		state.count++
		state.sumInt += value.(int64)
	case AggAvgFloat64:
		if !ok {
			return
		}
		state.count++
		state.sumFloat += value.(float64)
	default:
		panic(fmt.Sprintf("analytics: unsupported aggregate kind %d", state.kind))
	}
}

func (state aggregateState) value() any {
	switch state.kind {
	case AggCount:
		return int64(state.count)
	case AggSumInt64:
		return state.sumInt
	case AggSumFloat64:
		return state.sumFloat
	case AggMinInt64:
		if !state.hasInt {
			return nil
		}
		return state.minInt
	case AggMaxInt64:
		if !state.hasInt {
			return nil
		}
		return state.maxInt
	case AggMinFloat64:
		if !state.hasFloat {
			return nil
		}
		return state.minFloat
	case AggMaxFloat64:
		if !state.hasFloat {
			return nil
		}
		return state.maxFloat
	case AggAvgInt64:
		if state.count == 0 {
			return nil
		}
		return float64(state.sumInt) / float64(state.count)
	case AggAvgFloat64:
		if state.count == 0 {
			return nil
		}
		return state.sumFloat / float64(state.count)
	default:
		return nil
	}
}

// GroupResult is one grouped analytics row.
type GroupResult struct {
	Keys    map[string]any
	Values  map[string]any
	Summary string
}

type groupState struct {
	key        string
	keys       map[string]any
	aggregates []aggregateState
}

func (store *Store) GroupBy(ctx context.Context, groupColumns []string, predicates []Predicate, aggregates ...Aggregate) ([]GroupResult, error) {
	if len(aggregates) == 0 {
		return nil, fmt.Errorf("%w: no aggregates", ErrInvalidSchema)
	}
	if err := validateGroupColumns(store.schema, groupColumns); err != nil {
		return nil, err
	}
	normalized, err := store.normalizePredicates(predicates)
	if err != nil {
		return nil, err
	}
	segments := store.snapshotSegments()
	groups := make(map[string]*groupState)
	order := make([]string, 0)
	for _, segment := range segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !segment.mayMatch(normalized) {
			continue
		}
		for row := 0; row < segment.rows; row++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			matched := true
			for _, predicate := range normalized {
				value, ok := segment.value(predicate.Column(), row)
				if !predicate.Matches(value, ok) {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			key, keyValues := buildGroupKey(segment, row, groupColumns)
			state, exists := groups[key]
			if !exists {
				state = &groupState{
					key:        key,
					keys:       keyValues,
					aggregates: make([]aggregateState, len(aggregates)),
				}
				for position, aggregate := range aggregates {
					state.aggregates[position].kind = aggregate.Kind
				}
				groups[key] = state
				order = append(order, key)
			}
			for position, aggregate := range aggregates {
				value, ok := segment.value(aggregate.Column, row)
				state.aggregates[position].add(value, ok)
			}
		}
	}
	sort.Strings(order)
	results := make([]GroupResult, 0, len(order))
	for _, key := range order {
		state := groups[key]
		values := make(map[string]any, len(aggregates))
		for position, aggregate := range aggregates {
			values[aggregate.Alias] = state.aggregates[position].value()
		}
		results = append(results, GroupResult{
			Keys:    state.keys,
			Values:  values,
			Summary: state.key,
		})
	}
	return results, nil
}

func buildGroupKey(segment *Segment, row int, groupColumns []string) (string, map[string]any) {
	if len(groupColumns) == 0 {
		return "__all__", map[string]any{}
	}
	builder := strings.Builder{}
	keys := make(map[string]any, len(groupColumns))
	for _, column := range groupColumns {
		value, ok := segment.value(column, row)
		keys[column] = value
		builder.WriteString(column)
		builder.WriteByte('=')
		builder.WriteString(encodeGroupValue(value, ok))
		builder.WriteByte('\x1f')
	}
	return builder.String(), keys
}

func encodeGroupValue(value any, ok bool) string {
	if !ok {
		return "null"
	}
	switch typed := value.(type) {
	case bool:
		if typed {
			return "bool:true"
		}
		return "bool:false"
	case int64:
		return "int64:" + strconv.FormatInt(typed, 10)
	case float64:
		return "float64:" + strconv.FormatFloat(typed, 'g', -1, 64)
	case string:
		return "text:" + strconv.Quote(typed)
	case timeLike:
		return "time:" + typed.String()
	default:
		return fmt.Sprintf("%T:%v", value, value)
	}
}

type timeLike interface {
	String() string
}
