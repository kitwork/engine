package relational

import (
	"fmt"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type joinAggregate struct {
	plan     *kitdbsql.SelectStatement
	columns  []Column
	fields   []kitdbsql.Field
	bindings []aggregateBinding
	slots    []joinColumn
	names    []string
	row      map[string]any
}

// Normalize qualified JOIN fields to collision-free, query-local slots. The
// existing aggregate executor still owns exact arithmetic, NULL and HAVING.
func bindJoinAggregate(sources []joinSource, plan *kitdbsql.SelectStatement) (*joinAggregate, error) {
	if len(plan.Projection) > maximumJoinProjections || len(plan.Order) > maximumJoinOrders {
		return nil, fmt.Errorf("kitdb SQL: JOIN aggregate exceeds projection or ordering limits")
	}
	normalized := *plan
	normalized.Projection = append([]kitdbsql.Projection(nil), plan.Projection...)
	normalized.GroupBy = append([]string(nil), plan.GroupBy...)
	normalized.Order = append([]kitdbsql.Order(nil), plan.Order...)
	aggregate := &joinAggregate{plan: &normalized}
	schema := kitdbsql.Schema{Name: "JOIN"}
	resolve := func(requested string) (string, error) {
		column, err := resolveJoinColumn(sources, requested)
		if err != nil {
			return "", err
		}
		for i, slot := range aggregate.slots {
			if slot.source == column.source && slot.field.Name == column.field.Name {
				return aggregate.names[i], nil
			}
		}
		name := fmt.Sprintf("join_field_%d", len(aggregate.slots))
		field := column.field
		field.Name, field.Aliases = name, nil
		field.ID, field.Tag = name, uint32(len(aggregate.slots)+1)
		schema.Fields = append(schema.Fields, field)
		aggregate.slots = append(aggregate.slots, column)
		aggregate.names = append(aggregate.names, name)
		return name, nil
	}
	for i, requested := range plan.GroupBy {
		name, err := resolve(requested)
		if err != nil {
			return nil, err
		}
		normalized.GroupBy[i] = name
	}
	for i, projection := range plan.Projection {
		if projection.All || projection.Expression != nil {
			return nil, fmt.Errorf("kitdb SQL: JOIN aggregates require field-based aggregates and grouped fields")
		}
		if projection.Name == "" {
			continue
		}
		name, err := resolve(projection.Name)
		if err != nil {
			return nil, err
		}
		normalized.Projection[i].Name = name
		if projection.Aggregate == "" && !projection.Count && projection.Alias == "" {
			column, _ := resolveJoinColumn(sources, projection.Name)
			normalized.Projection[i].Alias = column.field.Name
		}
	}
	columns, err := describeAggregateSelect(schema, &normalized)
	if err != nil {
		return nil, err
	}
	for i, order := range plan.Order {
		if order.Expression != nil {
			return nil, fmt.Errorf("kitdb SQL: JOIN aggregate ORDER BY requires a projected field or alias")
		}
		if !strings.Contains(order.Column, ".") {
			continue
		}
		column, err := resolveJoinColumn(sources, order.Column)
		if err != nil {
			return nil, err
		}
		found := false
		for j, projection := range plan.Projection {
			if projection.Aggregate != "" || projection.Count || projection.Name == "" {
				continue
			}
			projected, _ := resolveJoinColumn(sources, projection.Name)
			if projected.source == column.source && projected.field.Name == column.field.Name {
				normalized.Order[i].Column = columns[j].Name
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("kitdb SQL: JOIN aggregate ORDER BY field %q must be projected", order.Column)
		}
	}
	for _, order := range normalized.Order {
		matches := 0
		for _, column := range columns {
			if strings.EqualFold(column.Name, order.Column) {
				matches++
			}
		}
		if matches > 1 {
			return nil, fmt.Errorf("kitdb SQL: JOIN aggregate ORDER BY output %q is ambiguous; use distinct aliases", order.Column)
		}
	}
	if _, _, err := bindAggregateOrder(&normalized, columns); err != nil {
		return nil, err
	}
	fields, bindings, err := bindAggregateShape(schema, &normalized)
	if err != nil {
		return nil, err
	}
	aggregate.columns, aggregate.fields, aggregate.bindings = columns, fields, bindings
	return aggregate, nil
}

func (aggregate *joinAggregate) add(stream *aggregateStream, environment joinEnvironment) error {
	if aggregate.row == nil {
		if err := stream.working.reserve(64+len(aggregate.slots)*64, "JOIN aggregate input slots"); err != nil {
			return err
		}
		aggregate.row = make(map[string]any, len(aggregate.slots))
	}
	for i, slot := range aggregate.slots {
		aggregate.row[aggregate.names[i]] = joinEnvironmentValue(environment, slot)
	}
	err := stream.add(aggregate.row)
	// Only group states may retain input values after this joined row.
	clear(aggregate.row)
	return err
}
