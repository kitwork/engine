package relational

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type storedDomain struct {
	Version int          `json:"version"`
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Hash    string       `json:"hash"`
	Column  domainColumn `json:"column"`
}

// Persist logical kinds, never the in-process TypeID or PostgreSQL metadata.
type domainColumn struct {
	Kind          string                     `json:"kind"`
	Precision     int                        `json:"precision,omitempty"`
	Scale         int                        `json:"scale,omitempty"`
	TimePrecision *int                       `json:"timePrecision,omitempty"`
	TextLength    *int                       `json:"textLength,omitempty"`
	NotNull       bool                       `json:"notNull,omitempty"`
	HasDefault    bool                       `json:"hasDefault,omitempty"`
	Default       kitdbsql.Literal           `json:"default"`
	Checks        []kitdbsql.CheckDefinition `json:"checks,omitempty"`
}

func (column domainColumn) plan() kitdbsql.ColumnDefinition {
	typeInfo, _ := kitdbsql.LookupKind(column.Kind)
	return kitdbsql.ColumnDefinition{Name: "value", Type: typeInfo,
		Precision: column.Precision, Scale: column.Scale, TimePrecision: column.TimePrecision, TextLength: column.TextLength,
		NotNull: column.NotNull, HasDefault: column.HasDefault, Default: column.Default, Checks: column.Checks}
}

func domainColumnFromPlan(column kitdbsql.ColumnDefinition) domainColumn {
	return domainColumn{Kind: column.Type.Kind, Precision: column.Precision, Scale: column.Scale,
		TimePrecision: column.TimePrecision, TextLength: column.TextLength, NotNull: column.NotNull,
		HasDefault: column.HasDefault, Default: column.Default, Checks: column.Checks}
}

func domainHash(domain storedDomain) string {
	domain.Hash = ""
	encoded, _ := json.Marshal(domain)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func decodeStoredDomain(entry kitdbengine.CatalogDomain) (*storedDomain, error) {
	var domain storedDomain
	decoder := json.NewDecoder(bytes.NewReader(entry.Definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&domain); err != nil {
		return nil, fmt.Errorf("kitdb: domain catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("kitdb: trailing domain catalog bytes")
	}
	if domain.Version != 1 || domain.ID != entry.ID || domain.Name != entry.Name || domain.Hash != entry.Hash ||
		domain.ID != kitdbsql.StableSchemaID("domain", domain.Name) || domain.Hash != domainHash(domain) {
		return nil, fmt.Errorf("kitdb: domain catalog identity/hash mismatch")
	}
	if _, err := validateDomain(domain.Name, domain.Column.plan()); err != nil {
		return nil, err
	}
	return &domain, nil
}

func validateDomain(name string, column kitdbsql.ColumnDefinition) (kitdbsql.Schema, error) {
	if len(name) == 0 || len(name) > 128 || name != strings.ToLower(name) {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: invalid domain name")
	}
	for i, r := range name {
		if !(r >= 'a' && r <= 'z' || r == '_' || i > 0 && r >= '0' && r <= '9') {
			return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: domain name requires a simple identifier")
		}
	}
	if _, builtin := kitdbsql.ResolveName(name); builtin || name == "double" || name == "character" || name == "serial2" || name == "serial4" || name == "serial8" || name == "smallserial" || name == "bigserial" {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: domain %q conflicts with a built-in type", name)
	}
	typeInfo, known := kitdbsql.LookupKind(column.Type.Kind)
	if !known || typeInfo.ID != column.Type.ID ||
		(!kitdbsql.SupportedFunctionKind(typeInfo.Kind) && typeInfo.Kind != "varchar" && typeInfo.Kind != "char") {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: DOMAIN supports text, varchar, char, UUID, integer, numeric, double and boolean base types")
	}
	if column.Name != "value" || column.DomainName != "" || column.Primary || column.Unique || column.Searchable ||
		column.SearchWeight != 0 || column.Analytics || column.Reference != nil || column.SequenceName != "" ||
		column.SequenceMode != "" || column.SequenceCache != 0 || len(column.Choices) != 0 || len(column.Checks) > 16 {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: invalid DOMAIN base definition")
	}
	if column.HasDefault && (literalUsesClock(column.Default.Kind) || column.Default.Kind == kitdbsql.LiteralParameter || column.Default.Kind == kitdbsql.LiteralDefault) {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: DOMAIN default must be an immutable literal")
	}
	for _, check := range column.Checks {
		if err := kitdbsql.ValidateExpression(check.Expression); err != nil {
			return kitdbsql.Schema{}, err
		}
		if err := domainValueOnly(check.Expression); err != nil {
			return kitdbsql.Schema{}, err
		}
	}
	// Reuse field coercion and CHECK validation, without inventing another value
	// encoding or placing domain DDL in the table namespace.
	column.Type = typeInfo
	schema, _, err := schemaFromCreate(&kitdbsql.CreateTableStatement{
		Name: "domain_" + name, Columns: []kitdbsql.ColumnDefinition{column}, Checks: column.Checks,
	}, kitdbengine.CatalogSnapshot{})
	if err != nil {
		return kitdbsql.Schema{}, err
	}
	kind := typeInfo.Kind
	if typeInfo.Kind == "varchar" || typeInfo.Kind == "char" {
		kind = "text"
	}
	for _, check := range column.Checks {
		checkKind, err := pureFunctionExpressionKind(&check.Expression, map[string]string{"value": kind})
		if err != nil {
			return kitdbsql.Schema{}, err
		}
		if checkKind != "" && checkKind != "bool" {
			return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: DOMAIN CHECK must return boolean")
		}
	}
	return schema, nil
}

func domainValueOnly(plan kitdbsql.ExpressionPlan) error {
	if plan.Kind == "field" && !strings.EqualFold(plan.Field, "value") {
		return fmt.Errorf("kitdb SQL: DOMAIN CHECK may reference only VALUE")
	}
	for _, child := range plan.Arguments {
		if err := domainValueOnly(child); err != nil {
			return err
		}
	}
	return nil
}

func bindDomainValue(plan kitdbsql.ExpressionPlan, column string) kitdbsql.ExpressionPlan {
	if plan.Kind == "field" {
		plan.Field = column
	}
	children := make([]kitdbsql.ExpressionPlan, len(plan.Arguments))
	for i, child := range plan.Arguments {
		children[i] = bindDomainValue(child, column)
	}
	plan.Arguments = children
	return plan
}

func resolveCreateDomains(plan *kitdbsql.CreateTableStatement, catalog kitdbengine.CatalogSnapshot) (kitdbsql.CreateTableStatement, map[string]*kitdbsql.DomainReference, error) {
	resolved := *plan
	refs := make(map[string]*kitdbsql.DomainReference)
	resolved.Columns = append([]kitdbsql.ColumnDefinition(nil), plan.Columns...)
	resolved.Checks = append([]kitdbsql.CheckDefinition(nil), plan.Checks...)
	cache := make(map[string]*storedDomain)
	for i, column := range resolved.Columns {
		if column.DomainName == "" {
			continue
		}
		name := strings.ToLower(column.DomainName)
		domain := cache[name]
		if domain == nil {
			for _, entry := range catalog.Domains {
				if entry.Name != name {
					continue
				}
				var err error
				domain, err = decodeStoredDomain(entry)
				if err != nil {
					return resolved, nil, err
				}
				cache[name] = domain
				break
			}
		}
		if domain == nil {
			return resolved, nil, fmt.Errorf("kitdb SQL: domain %q does not exist", name)
		}
		if column.SequenceMode != "" {
			return resolved, nil, fmt.Errorf("kitdb SQL: domain columns do not support sequence defaults yet")
		}
		base := domain.Column.plan()
		column.Type, _ = kitdbsql.LookupKind(base.Type.Kind)
		column.Precision, column.Scale, column.TextLength, column.TimePrecision = base.Precision, base.Scale, base.TextLength, base.TimePrecision
		column.NotNull = column.NotNull || base.NotNull
		if !column.HasDefault && base.HasDefault {
			column.HasDefault, column.Default = true, base.Default
		}
		for j, check := range base.Checks {
			check.Name = fmt.Sprintf("domain_%s_%s_%d", name, column.Name, j+1)
			check.Column = column.Name
			check.Expression = bindDomainValue(check.Expression, column.Name)
			resolved.Checks = append(resolved.Checks, check)
		}
		column.DomainName = ""
		resolved.Columns[i] = column
		refs[column.Name] = &kitdbsql.DomainReference{ID: domain.ID, Name: domain.Name, Hash: domain.Hash}
	}
	return resolved, refs, nil
}

func (engine *Engine) executeDomainDDL(ctx context.Context, create *kitdbsql.CreateDomainStatement, drop *kitdbsql.DropDomainStatement) (Result, error) {
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
	name := ""
	if create != nil {
		name = create.Name
	} else if drop != nil {
		name = drop.Name
	} else {
		return Result{}, fmt.Errorf("kitdb SQL: invalid DOMAIN plan")
	}
	var existing *kitdbengine.CatalogDomain
	for i := range catalog.Domains {
		if catalog.Domains[i].Name == name {
			existing = &catalog.Domains[i]
			break
		}
	}
	command := "CREATE DOMAIN"
	var definition []byte
	if create != nil {
		if existing != nil {
			return Result{}, fmt.Errorf("kitdb SQL: domain %q already exists", name)
		}
		if _, err := validateDomain(name, create.Column); err != nil {
			return Result{}, err
		}
		domain := storedDomain{Version: 1, ID: kitdbsql.StableSchemaID("domain", name), Name: name, Column: domainColumnFromPlan(create.Column)}
		domain.Hash = domainHash(domain)
		definition, err = json.Marshal(domain)
		if err != nil {
			return Result{}, err
		}
	} else {
		command = "DROP DOMAIN"
		if existing == nil {
			if drop.IfExists {
				return Result{CommandTag: command}, nil
			}
			return Result{}, fmt.Errorf("kitdb SQL: domain %q does not exist", name)
		}
		for _, entry := range catalog.Structs {
			schema, err := decodeCatalogSchema(entry.Definition)
			if err != nil {
				return Result{}, err
			}
			for _, field := range schema.Fields {
				if field.Domain != nil && field.Domain.ID == existing.ID {
					return Result{}, fmt.Errorf("kitdb SQL: cannot drop domain %q: table %q field %q depends on it", name, schema.Name, field.Name)
				}
			}
		}
	}
	tx, err := engine.database.Begin()
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	if create != nil {
		err = tx.DefineDomain(definition)
	} else {
		err = tx.DeleteDomain(existing.ID)
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
