package relational

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	maximumTriggerNodes       = 256
	maximumTriggerEvaluations = 10_000
	maximumTriggerDepth       = 8
)

type storedTrigger struct {
	Version      int                       `json:"version"`
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Hash         string                    `json:"hash"`
	SourceStruct string                    `json:"sourceStruct"`
	TargetStruct string                    `json:"targetStruct"`
	SourceFields []uint32                  `json:"sourceFields"`
	TargetFields []uint32                  `json:"targetFields"`
	Event        string                    `json:"event"`
	When         *kitdbsql.ExpressionPlan  `json:"when,omitempty"`
	Values       []kitdbsql.ExpressionPlan `json:"values"`
}

type triggerInput struct {
	name  string
	old   bool
	field kitdbsql.Field
}

type boundTrigger struct {
	definition *storedTrigger
	inputs     []triggerInput
	when       *boundPredicate
	values     []*boundPredicate
	insert     kitdbsql.InsertStatement
}

type triggerExecutionKey struct{}

// One root statement owns the budget and bound plans. Cascaded actions reuse
// this state and the parent's write-set; no global trigger worker/cache exists.
type triggerExecution struct {
	bound       map[string][]*boundTrigger
	evaluations int
	depth       int
}

func triggerHash(trigger storedTrigger) string {
	trigger.Hash = ""
	encoded, _ := json.Marshal(trigger)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateTriggerExpressions(when *kitdbsql.ExpressionPlan, values []kitdbsql.ExpressionPlan) error {
	count := 0
	var walk func(kitdbsql.ExpressionPlan) error
	walk = func(plan kitdbsql.ExpressionPlan) error {
		count++
		if count > maximumTriggerNodes {
			return fmt.Errorf("kitdb SQL: trigger exceeds %d expression nodes", maximumTriggerNodes)
		}
		for _, child := range plan.Arguments {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	plans := append([]kitdbsql.ExpressionPlan(nil), values...)
	if when != nil {
		plans = append(plans, *when)
	}
	for _, plan := range plans {
		if err := kitdbsql.ValidateExpression(plan); err != nil {
			return err
		}
		if err := walk(plan); err != nil {
			return err
		}
	}
	return nil
}

func decodeStoredTrigger(entry kitdbengine.CatalogTrigger) (*storedTrigger, error) {
	var trigger storedTrigger
	decoder := json.NewDecoder(bytes.NewReader(entry.Definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&trigger); err != nil {
		return nil, fmt.Errorf("kitdb: trigger catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("kitdb: trailing trigger catalog bytes")
	}
	if len(trigger.Values) != len(entry.TargetFields) || len(trigger.Values) == 0 || len(trigger.Values) > kitdbsql.MaximumTriggerColumns {
		return nil, fmt.Errorf("kitdb: invalid trigger values")
	}
	if err := validateTriggerExpressions(trigger.When, trigger.Values); err != nil {
		return nil, err
	}
	if trigger.Version != 1 || trigger.ID != entry.ID || trigger.Name != entry.Name || trigger.Hash != entry.Hash ||
		trigger.SourceStruct != entry.SourceStruct || trigger.TargetStruct != entry.TargetStruct || trigger.Event != entry.Event ||
		trigger.ID != kitdbsql.StableSchemaID("trigger", trigger.SourceStruct+":"+trigger.Name) || triggerHash(trigger) != trigger.Hash ||
		!sameTriggerTags(trigger.SourceFields, entry.SourceFields) || !sameTriggerTags(trigger.TargetFields, entry.TargetFields) {
		return nil, fmt.Errorf("kitdb: trigger catalog identity/hash mismatch")
	}
	return &trigger, nil
}

func sameTriggerTags(a, b []uint32) bool {
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

func triggerSchemaByID(catalog kitdbengine.CatalogSnapshot, id string) (kitdbsql.Schema, error) {
	for _, entry := range catalog.Structs {
		if entry.ID == id {
			return decodeCatalogSchema(entry.Definition)
		}
	}
	return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: trigger table %s no longer exists", id)
}

func triggerSchemaByName(catalog kitdbengine.CatalogSnapshot, name string) (kitdbsql.Schema, error) {
	for _, entry := range catalog.Structs {
		if strings.EqualFold(entry.Name, name) {
			return decodeCatalogSchema(entry.Definition)
		}
	}
	return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: trigger table %q does not exist", name)
}

func triggerVariable(name string) (bool, uint32, error) {
	image, number, found := strings.Cut(name, "_")
	if !found || (image != "old" && image != "new") {
		return false, 0, fmt.Errorf("kitdb: invalid trigger variable %q", name)
	}
	tag, err := strconv.ParseUint(number, 10, 32)
	if err != nil || tag == 0 || strconv.FormatUint(tag, 10) != number {
		return false, 0, fmt.Errorf("kitdb: invalid trigger field tag")
	}
	return image == "old", uint32(tag), nil
}

func rewriteTriggerExpression(plan kitdbsql.ExpressionPlan, source kitdbsql.Schema, event string, tags map[uint32]bool) (kitdbsql.ExpressionPlan, error) {
	if plan.Kind == "field" {
		image, name, found := strings.Cut(plan.Field, ".")
		image = strings.ToLower(image)
		if !found || (image != "old" && image != "new") || strings.Contains(name, ".") {
			return plan, fmt.Errorf("kitdb SQL: trigger expressions require OLD.field or NEW.field")
		}
		if image == "old" && event == "insert" || image == "new" && event == "delete" {
			return plan, fmt.Errorf("kitdb SQL: %s is unavailable for %s triggers", strings.ToUpper(image), strings.ToUpper(event))
		}
		_, field, found := source.FieldByName(name)
		if !found {
			return plan, fmt.Errorf("kitdb SQL: trigger references missing field %q", name)
		}
		tags[field.Tag] = true
		plan.Field = fmt.Sprintf("%s_%d", image, field.Tag)
	}
	children := make([]kitdbsql.ExpressionPlan, len(plan.Arguments))
	for i, child := range plan.Arguments {
		var err error
		children[i], err = rewriteTriggerExpression(child, source, event, tags)
		if err != nil {
			return plan, err
		}
	}
	plan.Arguments = children
	return plan, nil
}

func buildStoredTrigger(plan *kitdbsql.CreateTriggerStatement, catalog kitdbengine.CatalogSnapshot) (*storedTrigger, error) {
	if plan == nil || len(plan.Name) == 0 || len(plan.Name) > 128 || plan.Name != strings.ToLower(plan.Name) || strings.ContainsAny(plan.Name, ".\x00") ||
		len(plan.Columns) == 0 || len(plan.Columns) != len(plan.Values) || len(plan.Columns) > kitdbsql.MaximumTriggerColumns {
		return nil, fmt.Errorf("kitdb SQL: invalid trigger plan")
	}
	switch plan.Event {
	case "insert", "update", "delete":
	default:
		return nil, fmt.Errorf("kitdb SQL: invalid trigger event")
	}
	if err := validateTriggerExpressions(plan.When, plan.Values); err != nil {
		return nil, err
	}
	source, err := triggerSchemaByName(catalog, plan.Table)
	if err != nil {
		return nil, err
	}
	target, err := triggerSchemaByName(catalog, plan.Target)
	if err != nil {
		return nil, err
	}
	trigger := &storedTrigger{Version: 1, Name: plan.Name, SourceStruct: source.ID, TargetStruct: target.ID, Event: plan.Event}
	trigger.ID = kitdbsql.StableSchemaID("trigger", source.ID+":"+plan.Name)
	tags := make(map[uint32]bool)
	if plan.When != nil {
		condition, err := rewriteTriggerExpression(*plan.When, source, plan.Event, tags)
		if err != nil {
			return nil, err
		}
		trigger.When = &condition
	}
	seen := make(map[uint32]bool)
	for i, name := range plan.Columns {
		_, field, found := target.FieldByName(name)
		if !found || seen[field.Tag] {
			return nil, fmt.Errorf("kitdb SQL: trigger target field %q is missing or repeated", name)
		}
		seen[field.Tag] = true
		trigger.TargetFields = append(trigger.TargetFields, field.Tag)
		value, err := rewriteTriggerExpression(plan.Values[i], source, plan.Event, tags)
		if err != nil {
			return nil, err
		}
		trigger.Values = append(trigger.Values, value)
	}
	for tag := range tags {
		trigger.SourceFields = append(trigger.SourceFields, tag)
	}
	sort.Slice(trigger.SourceFields, func(i, j int) bool { return trigger.SourceFields[i] < trigger.SourceFields[j] })
	if _, err := bindStoredTrigger(trigger, catalog); err != nil {
		return nil, err
	}
	trigger.Hash = triggerHash(*trigger)
	return trigger, nil
}

func triggerScalarKind(field kitdbsql.Field) (string, error) {
	kind := field.Kind
	if kind == "varchar" || kind == "char" {
		kind = "text"
	}
	if !kitdbsql.SupportedFunctionKind(kind) && !exactTemporalFieldKind(kind) && kind != "date" && kind != "time" && kind != "datetime" {
		return "", fmt.Errorf("kitdb SQL: trigger field %q requires a supported scalar type", field.Name)
	}
	return kind, nil
}

func bindStoredTrigger(trigger *storedTrigger, catalog kitdbengine.CatalogSnapshot) (*boundTrigger, error) {
	source, err := triggerSchemaByID(catalog, trigger.SourceStruct)
	if err != nil {
		return nil, err
	}
	target, err := triggerSchemaByID(catalog, trigger.TargetStruct)
	if err != nil {
		return nil, err
	}
	if err := standaloneWriteSupported(target); err != nil {
		return nil, err
	}
	bound := &boundTrigger{definition: trigger, insert: kitdbsql.InsertStatement{Table: target.Name}}
	variables := make(map[string]string)
	seenTags := make(map[uint32]bool)
	synthetic := kitdbsql.Schema{Name: "trigger"}
	var collect func(kitdbsql.ExpressionPlan) error
	collect = func(plan kitdbsql.ExpressionPlan) error {
		if plan.Kind == "field" {
			old, tag, err := triggerVariable(plan.Field)
			if err != nil {
				return err
			}
			if old && trigger.Event == "insert" || !old && trigger.Event == "delete" {
				return fmt.Errorf("kitdb: invalid trigger row image")
			}
			field, found := fieldByTag(source, tag)
			if !found {
				return fmt.Errorf("kitdb: trigger source field tag %d disappeared", tag)
			}
			kind, err := triggerScalarKind(field)
			if err != nil {
				return err
			}
			seenTags[tag] = true
			if _, exists := variables[plan.Field]; !exists {
				variables[plan.Field] = kind
				bound.inputs = append(bound.inputs, triggerInput{name: plan.Field, old: old, field: field})
				field.Name, field.Aliases = plan.Field, nil
				synthetic.Fields = append(synthetic.Fields, field)
			}
		}
		for _, child := range plan.Arguments {
			if err := collect(child); err != nil {
				return err
			}
		}
		return nil
	}
	if trigger.When != nil {
		if err := collect(*trigger.When); err != nil {
			return nil, err
		}
	}
	for _, value := range trigger.Values {
		if err := collect(value); err != nil {
			return nil, err
		}
	}
	if len(seenTags) != len(trigger.SourceFields) {
		return nil, fmt.Errorf("kitdb: trigger dependency header mismatch")
	}
	for _, tag := range trigger.SourceFields {
		if !seenTags[tag] {
			return nil, fmt.Errorf("kitdb: trigger dependency header mismatch")
		}
	}
	bind := func(plan *kitdbsql.ExpressionPlan) (*boundPredicate, string, error) {
		kind, err := pureFunctionExpressionKind(plan, variables)
		if err != nil {
			return nil, "", err
		}
		predicate, err := bindPredicate(synthetic, plan, nil)
		if err != nil {
			return nil, "", err
		}
		limitTriggerText(predicate)
		return predicate, kind, nil
	}
	if trigger.When != nil {
		predicate, kind, err := bind(trigger.When)
		if err != nil {
			return nil, err
		}
		if kind != "bool" && kind != "" {
			return nil, fmt.Errorf("kitdb SQL: trigger WHEN must return boolean")
		}
		bound.when = predicate
	}
	row := make([]kitdbsql.Literal, len(trigger.Values))
	for i, value := range trigger.Values {
		field, found := fieldByTag(target, trigger.TargetFields[i])
		if !found {
			return nil, fmt.Errorf("kitdb: trigger target field disappeared")
		}
		bound.insert.Columns = append(bound.insert.Columns, field.Name)
		if value.Kind == "literal" && value.Literal.Kind == kitdbsql.LiteralDefault {
			bound.values = append(bound.values, nil)
			row[i] = kitdbsql.Literal{Kind: kitdbsql.LiteralDefault}
			continue
		}
		if field.Sequence != nil && field.Sequence.Mode == "always" {
			return nil, fmt.Errorf("kitdb SQL: trigger cannot assign GENERATED ALWAYS field %q", field.Name)
		}
		predicate, kind, err := bind(&value)
		if err != nil {
			return nil, err
		}
		targetKind, err := triggerScalarKind(field)
		if err != nil {
			return nil, err
		}
		if value.Kind == "literal" {
			literal, err := resolveLiteral(value.Literal, nil)
			if err != nil {
				return nil, err
			}
			if _, err := coerceField(field, literal); err != nil {
				return nil, err
			}
		} else if !functionKindAssignable(kind, targetKind) {
			return nil, fmt.Errorf("kitdb SQL: trigger result %s is incompatible with target field %q (%s)", kind, field.Name, targetKind)
		}
		row[i] = kitdbsql.Literal{Kind: kitdbsql.LiteralParameter, Parameter: i + 1}
		bound.values = append(bound.values, predicate)
	}
	bound.insert.Rows = [][]kitdbsql.Literal{row}
	return bound, nil
}

func limitTriggerText(node *boundPredicate) {
	if node == nil {
		return
	}
	node.maximumTextBytes = maximumFunctionTextBytes
	for _, child := range node.arguments {
		limitTriggerText(child)
	}
}

func (transaction *Transaction) fireAfterTriggers(ctx context.Context, schema kitdbsql.Schema, event string, count int, images func(int) (map[string]any, map[string]any)) error {
	if len(transaction.catalog.Triggers) == 0 || count == 0 {
		return nil
	}
	state, ok := ctx.Value(triggerExecutionKey{}).(*triggerExecution)
	if !ok {
		return fmt.Errorf("kitdb: trigger execution requires a statement budget")
	}
	key := schema.ID + ":" + event
	triggers, found := state.bound[key]
	if !found {
		for _, entry := range transaction.catalog.Triggers {
			if entry.SourceStruct != schema.ID || entry.Event != event {
				continue
			}
			definition, err := decodeStoredTrigger(entry)
			if err != nil {
				return err
			}
			bound, err := bindStoredTrigger(definition, transaction.catalog)
			if err != nil {
				return err
			}
			triggers = append(triggers, bound)
		}
		sort.Slice(triggers, func(i, j int) bool { return triggers[i].definition.Name < triggers[j].definition.Name })
		state.bound[key] = triggers
	}
	if len(triggers) == 0 {
		return nil
	}
	if state.depth >= maximumTriggerDepth {
		return fmt.Errorf("kitdb SQL: trigger cascade exceeds depth %d", maximumTriggerDepth)
	}
	state.depth++
	defer func() { state.depth-- }()
	for i := 0; i < count; i++ {
		old, next := images(i)
		for _, trigger := range triggers {
			if err := ctx.Err(); err != nil {
				return err
			}
			state.evaluations++
			if state.evaluations > maximumTriggerEvaluations {
				return fmt.Errorf("kitdb SQL: trigger statement exceeds %d evaluations", maximumTriggerEvaluations)
			}
			if err := transaction.fireTrigger(ctx, trigger, old, next); err != nil {
				return fmt.Errorf("kitdb SQL: trigger %q: %w", trigger.definition.Name, err)
			}
		}
	}
	return nil
}

func (transaction *Transaction) fireTrigger(ctx context.Context, trigger *boundTrigger, old, next map[string]any) error {
	row := make(map[string]any, len(trigger.inputs))
	for _, input := range trigger.inputs {
		values := next
		if input.old {
			values = old
		}
		row[input.name] = values[input.field.Name]
	}
	if trigger.when != nil {
		value, err := evaluateBoundPredicate(row, trigger.when)
		if err != nil {
			return err
		}
		truth, err := checkTruthOf(value)
		if err != nil || truth != checkTrue {
			return err
		}
	}
	parameters := make([]any, len(trigger.values))
	for i, predicate := range trigger.values {
		if predicate == nil {
			continue
		}
		value, err := evaluateBoundPredicate(row, predicate)
		if err != nil {
			return err
		}
		parameters[i] = value
	}
	_, err := transaction.executeInsert(ctx, &trigger.insert, parameters)
	return err
}

func (engine *Engine) executeTriggerDDL(ctx context.Context, create *kitdbsql.CreateTriggerStatement, drop *kitdbsql.DropTriggerStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	command := "CREATE TRIGGER"
	var definition []byte
	var dropID string
	if create != nil {
		trigger, err := buildStoredTrigger(create, catalog)
		if err != nil {
			return Result{}, err
		}
		for _, entry := range catalog.Triggers {
			if entry.SourceStruct == trigger.SourceStruct && entry.Name == trigger.Name {
				return Result{}, fmt.Errorf("kitdb SQL: trigger %q already exists", trigger.Name)
			}
		}
		definition, err = json.Marshal(trigger)
		if err != nil {
			return Result{}, err
		}
	} else if drop != nil {
		command = "DROP TRIGGER"
		source, err := triggerSchemaByName(catalog, drop.Table)
		if err != nil {
			return Result{}, err
		}
		for _, entry := range catalog.Triggers {
			if entry.SourceStruct == source.ID && entry.Name == drop.Name {
				dropID = entry.ID
				break
			}
		}
		if dropID == "" {
			if drop.IfExists {
				return Result{CommandTag: command}, nil
			}
			return Result{}, fmt.Errorf("kitdb SQL: trigger %q does not exist on %q", drop.Name, source.Name)
		}
	} else {
		return Result{}, fmt.Errorf("kitdb SQL: invalid trigger DDL")
	}
	tx, err := engine.database.Begin()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	if create != nil {
		err = tx.DefineTrigger(definition)
	} else {
		err = tx.DeleteTrigger(dropID)
	}
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if _, err := tx.Commit(); err != nil {
		return Result{}, err
	}
	return Result{CommandTag: command}, nil
}
