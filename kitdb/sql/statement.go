package sql

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	MaximumDDLColumns        = 512
	MaximumInsertRows        = 10_000
	MaximumSelectJoins       = 7
	MaximumJoinEqualities    = 32
	MaximumSetOperations     = 15
	MaximumCommonTables      = 16
	MaximumQueryNesting      = 8
	MaximumDecimalPrecision  = 1_000
	MaximumTemporalPrecision = 6
	MaximumTextLength        = 10_485_760
)

// LiteralKind identifies the bounded literal forms accepted by the
// standalone relational profile. Resolution into a storage value happens
// after schema binding, so unknown PostgreSQL parameters are coerced by the
// declared field rather than guessed from their text.
type LiteralKind uint8

const (
	LiteralInvalid LiteralKind = iota
	LiteralNull
	LiteralBoolean
	LiteralNumber
	LiteralString
	LiteralParameter
	LiteralCurrentTimestamp
	LiteralDefault
	LiteralCurrentDate
	LiteralCurrentTime
	LiteralLocalTimestamp
)

type Literal struct {
	Kind      LiteralKind
	Text      string
	Boolean   bool
	Parameter int
}

type ParsedStatement struct {
	Savepoint      *SavepointStatement
	CreateTrigger  *CreateTriggerStatement
	DropTrigger    *DropTriggerStatement
	CreateDomain   *CreateDomainStatement
	DropDomain     *DropDomainStatement
	CreateSequence *CreateSequenceStatement
	DropSequence   *DropSequenceStatement
	AlterSequence  *AlterSequenceStatement
	CreateFunction *CreateFunctionStatement
	DropFunction   *DropFunctionStatement
	Kind           StatementKind
	CreateTable    *CreateTableStatement
	CreateIndex    *CreateIndexStatement
	DropTable      *DropTableStatement
	DropIndex      *DropIndexStatement
	AlterTable     *AlterTableStatement
	Analyze        *AnalyzeStatement
	Reindex        *ReindexStatement
	Pragma         *PragmaStatement
	Explain        *SelectStatement
	ExplainAnalyze bool
	Insert         *InsertStatement
	Select         *SelectStatement
	Update         *UpdateStatement
	Delete         *DeleteStatement
}

// PragmaStatement is one bounded, read-only inspection request. Its argument
// is a table name rather than a general expression, so PRAGMA cannot smuggle a
// second execution language into the standalone SQL surface.
type PragmaStatement struct {
	Name     string
	Argument string
}

type CreateIndexStatement struct {
	Name        string
	Table       string
	Columns     []string
	Conditions  []Condition
	Unique      bool
	IfNotExists bool
}

type DropIndexStatement struct {
	Name     string
	IfExists bool
}

type DropTableStatement struct {
	Name     string
	IfExists bool
}

type AlterTableAction uint8

const (
	AlterTableInvalid AlterTableAction = iota
	AlterTableAddColumn
	AlterTableDropColumn
	AlterTableRenameColumn
	AlterTableRenameTable
	AlterTableSetSearchable
	AlterTableDropSearchable
	AlterTableSetAnalytics
	AlterTableDropAnalytics
	AlterTableSetPartitioning
	AlterTableDropPartitioning
)

type AlterTableStatement struct {
	Table        string
	Action       AlterTableAction
	Column       *ColumnDefinition
	IfExists     bool
	OldName      string
	NewName      string
	SearchWeight int
	Partition    *PartitionDefinition
}

type AnalyzeStatement struct {
	Table string
}

type ReindexStatement struct {
	Table string
}

type CreateTableStatement struct {
	Name        string
	IfNotExists bool
	Columns     []ColumnDefinition
	PrimaryKey  []string
	Unique      [][]string
	ForeignKeys []ForeignKeyDefinition
	Checks      []CheckDefinition
	Partition   *PartitionDefinition
}

type PartitionDefinition struct {
	Strategy string
	Field    string
}

type ColumnDefinition struct {
	DomainName    string
	SequenceName  string
	SequenceMode  string
	SequenceCache int64
	Name          string
	Type          Type
	Choices       []string
	Precision     int
	Scale         int
	TimePrecision *int
	TextLength    *int
	Primary       bool
	NotNull       bool
	Unique        bool
	Searchable    bool
	SearchWeight  int
	Analytics     bool
	HasDefault    bool
	Default       Literal
	Reference     *ForeignKeyDefinition
	Checks        []CheckDefinition
}

type columnTypeModifiers struct {
	Precision     int
	Scale         int
	TimePrecision *int
	TextLength    *int
}

type ForeignKeyDefinition struct {
	Name          string
	Columns       []string
	TargetTable   string
	TargetColumns []string
	OnDelete      string
	OnUpdate      string
}

type CheckDefinition struct {
	Name       string
	Column     string
	Expression CheckPlan
}

// ExpressionPlan is the bounded, storage-neutral syntax tree shared by WHERE,
// HAVING, scalar projections, INSERT values, UPDATE assignments and table checks.
// Durable checks replace Field names with stable field tags before publication.
type ExpressionPlan struct {
	Kind          string
	Literal       Literal
	Field         string
	Operator      string
	Arguments     []ExpressionPlan
	CastKind      string `json:"castKind,omitempty"`
	Precision     int    `json:"precision,omitempty"`
	Scale         int    `json:"scale,omitempty"`
	TimePrecision *int   `json:"timePrecision,omitempty"`
	TextLength    *int   `json:"textLength,omitempty"`
	// Function is resolved from the query snapshot, never persisted in an expression.
	Function *FunctionDefinition `json:"-"`
}

// Validate checks the same structural bounds as parsed SQL. Embedded callers
// can construct plans directly, but cannot bypass expression size/depth limits.
func (expression ExpressionPlan) Validate() error {
	return validateExpressionPlan(expression)
}

// CheckPlan preserves the original parser-facing name while callers move to
// the general expression terminology. It is an alias, not a second IR.
type CheckPlan = ExpressionPlan

type InsertStatement struct {
	Overriding string
	Table      string
	Columns    []string
	Rows       [][]Literal
	// Values carries expressions instead of Rows; callers must supply only one.
	// Literal-only SQL keeps Rows for existing embedded/import callers.
	Values      [][]ExpressionPlan
	Select      *SelectStatement
	Conflict    *ConflictClause
	DefaultRows int
	Returning   []Projection
}

// ConflictClause arbitrates a primary/unique column tuple, not an arbitrary index expression.
type ConflictClause struct {
	Columns     []string
	Nothing     bool
	Assignments []Assignment
	Predicate   *ExpressionPlan
}

type SelectStatement struct {
	Table         string
	TableAlias    string
	Source        *SelectStatement
	CommonTables  []CommonTableExpression
	Joins         []Join
	Distinct      bool
	Projection    []Projection
	Conditions    []Condition
	Predicate     *CheckPlan
	Search        *SearchPredicate
	SetOperations []SetOperation
	Order         []Order
	GroupBy       []string
	Having        *CheckPlan
	Limit         int
	Offset        int
	HasLimit      bool
	After         Literal
	HasAfter      bool
}

// CommonTableExpression is one bounded, non-recursive query-local relation.
// Entries are evaluated in declaration order, so a CTE may reference an
// earlier sibling but never itself or a later sibling.
type CommonTableExpression struct {
	Name    string
	Columns []string
	Select  *SelectStatement
}

// SetOperation joins another SELECT core to the preceding result. The first
// standalone slice intentionally accepts UNION ALL only: it preserves rows
// without introducing an unbounded duplicate-elimination operator.
type SetOperation struct {
	Kind   string
	Select *SelectStatement
}

// SearchPredicate is the storage-neutral ranked-search clause extracted from
// WHERE. An empty Fields slice means every catalog field marked searchable.
type SearchPredicate struct {
	Fields []string
	Query  Literal
}

type UpdateStatement struct {
	Table       string
	Assignments []Assignment
	Conditions  []Condition
	Predicate   *CheckPlan
	Returning   []Projection
}

type DeleteStatement struct {
	Table      string
	Conditions []Condition
	Predicate  *CheckPlan
	Returning  []Projection
}

type Assignment struct {
	Default    bool
	Column     string
	Value      Literal
	Expression *CheckPlan
}

type Projection struct {
	Name       string
	Qualifier  string
	Alias      string
	All        bool
	Count      bool
	Aggregate  string
	Expression *CheckPlan
}

type Join struct {
	Kind  string
	Table string
	Alias string
	Left  string
	Right string
	And   []JoinEquality
}

// JoinEquality extends the first Left/Right equality without changing old plans.
type JoinEquality struct {
	Left  string
	Right string
}

type Condition struct {
	Column   string
	Operator string
	Value    Literal
}

type Order struct {
	Column     string
	Descending bool
	Expression *CheckPlan
}

type statementParser struct {
	cursor          *Cursor
	nextParameter   int
	expressionDepth int
	queryDepth      int
}

const (
	maximumExpressionDepth = 24
	maximumExpressionNodes = 256
)

// ParseStatement parses the first standalone execution slice. It deliberately
// rejects syntax outside the implemented profile instead of accepting a plan
// that execution would reinterpret or silently ignore.
func ParseStatement(source string) (ParsedStatement, error) {
	envelope, err := ParseEnvelope(source)
	if err != nil {
		return ParsedStatement{}, err
	}
	cursor, err := NewCursor(envelope.Tokens, envelope.Body)
	if err != nil {
		return ParsedStatement{}, err
	}
	parser := statementParser{cursor: cursor, nextParameter: 1}
	statement := ParsedStatement{Kind: envelope.Kind, ExplainAnalyze: envelope.ExplainAnalyze}
	switch envelope.Kind {
	case StatementSavepoint:
		action := strings.ToLower(envelope.Tokens[0].Text)
		if action == "rollback" {
			if !cursor.AcceptKeyword("work") {
				cursor.AcceptKeyword("transaction")
			}
			if err = cursor.ExpectKeyword("to"); err != nil {
				return ParsedStatement{}, err
			}
		}
		if action != "savepoint" {
			cursor.AcceptKeyword("savepoint")
		}
		token := cursor.Peek()
		name, nameErr := parser.identifier()
		if nameErr != nil {
			return ParsedStatement{}, nameErr
		}
		trimmed := strings.TrimSpace(source)
		if trimmed[token.Start] != '"' && trimmed[token.Start] != '`' && trimmed[token.Start] != '[' {
			name = strings.ToLower(name)
		}
		if len(name) > 128 {
			return ParsedStatement{}, fmt.Errorf("kitdb SQL: savepoint name exceeds 128 bytes")
		}
		statement.Savepoint = &SavepointStatement{Action: action, Name: name}
	case StatementCreate:
		replace := false
		if cursor.AcceptKeyword("or") {
			if err := cursor.ExpectKeyword("replace"); err != nil {
				return ParsedStatement{}, err
			}
			replace = true
		}
		unique := cursor.AcceptKeyword("unique")
		switch {
		case cursor.AcceptKeyword("trigger"):
			if unique || replace {
				return ParsedStatement{}, fmt.Errorf("kitdb SQL: TRIGGER does not support UNIQUE or OR REPLACE")
			}
			statement.CreateTrigger, err = parser.parseCreateTrigger()
		case cursor.AcceptKeyword("domain"):
			if unique || replace {
				return ParsedStatement{}, fmt.Errorf("kitdb SQL: DOMAIN does not support UNIQUE or OR REPLACE")
			}
			statement.CreateDomain, err = parser.parseCreateDomain()
		case cursor.AcceptKeyword("sequence"):
			if unique || replace {
				return ParsedStatement{}, fmt.Errorf("kitdb SQL: sequence does not support UNIQUE or OR REPLACE")
			}
			statement.CreateSequence, err = parser.parseCreateSequence()
		case cursor.AcceptKeyword("function"):
			if unique {
				return ParsedStatement{}, fmt.Errorf("kitdb SQL: UNIQUE is not valid for functions")
			}
			statement.CreateFunction, err = parser.parseCreateFunction(replace)
		case cursor.AcceptKeyword("table"):
			if unique || replace {
				return ParsedStatement{}, fmt.Errorf("kitdb SQL: UNIQUE is valid only with CREATE INDEX")
			}
			statement.CreateTable, err = parser.parseCreateTable()
		case cursor.AcceptKeyword("index"):
			if replace {
				return ParsedStatement{}, fmt.Errorf("kitdb SQL: OR REPLACE is only supported for functions")
			}
			statement.CreateIndex, err = parser.parseCreateIndex(unique)
		default:
			return ParsedStatement{}, fmt.Errorf("kitdb SQL: standalone CREATE supports TABLE and INDEX")
		}
	case StatementDrop:
		switch {
		case cursor.AcceptKeyword("trigger"):
			statement.DropTrigger, err = parser.parseDropTrigger()
		case cursor.AcceptKeyword("domain"):
			statement.DropDomain, err = parser.parseDropDomain()
		case cursor.AcceptKeyword("sequence"):
			statement.DropSequence, err = parser.parseDropSequence()
		case cursor.AcceptKeyword("function"):
			statement.DropFunction, err = parser.parseDropFunction()
		case cursor.AcceptKeyword("table"):
			statement.DropTable, err = parser.parseDropTable()
		case cursor.AcceptKeyword("index"):
			statement.DropIndex, err = parser.parseDropIndex()
		default:
			return ParsedStatement{}, fmt.Errorf("kitdb SQL: standalone DROP supports TABLE and INDEX")
		}
	case StatementAnalyze:
		statement.Analyze, err = parser.parseAnalyze()
	case StatementReindex:
		statement.Reindex, err = parser.parseReindex()
	case StatementPragma:
		statement.Pragma, err = parser.parsePragma()
	case StatementAlter:
		if cursor.AcceptKeyword("sequence") {
			statement.AlterSequence, err = parser.parseAlterSequence()
		} else {
			statement.AlterTable, err = parser.parseAlterTable()
		}
	case StatementInsert:
		statement.Insert, err = parser.parseInsert()
	case StatementSelect:
		if envelope.With {
			statement.Select, err = parser.parseWithSelect()
		} else {
			statement.Select, err = parser.parseSelect()
		}
	case StatementExplain:
		if envelope.With {
			statement.Explain, err = parser.parseWithSelect()
		} else {
			statement.Explain, err = parser.parseSelect()
		}
	case StatementUpdate:
		statement.Update, err = parser.parseUpdate()
	case StatementDelete:
		statement.Delete, err = parser.parseDelete()
	default:
		return ParsedStatement{}, fmt.Errorf(
			"kitdb SQL: standalone execution currently supports relational DDL and DML",
		)
	}
	if err != nil {
		return ParsedStatement{}, err
	}
	if cursor.AcceptSymbol(";") && cursor.Peek().Kind != TokenEOF {
		return ParsedStatement{}, fmt.Errorf("kitdb SQL: multiple statements are not supported")
	}
	if cursor.Peek().Kind != TokenEOF {
		return ParsedStatement{}, fmt.Errorf("kitdb SQL: unexpected token %q", cursor.Peek().Text)
	}
	return statement, nil
}

func (parser *statementParser) parsePragma() (*PragmaStatement, error) {
	name, err := parser.identifier()
	if err != nil {
		return nil, err
	}
	plan := &PragmaStatement{Name: strings.ToLower(name)}
	if plan.Name == "cache_status" && (parser.cursor.Peek().Kind == TokenEOF ||
		(parser.cursor.Peek().Kind == TokenSymbol && parser.cursor.Peek().Text == ";")) {
		return plan, nil
	}
	if err := parser.cursor.ExpectSymbol("("); err != nil {
		return nil, fmt.Errorf("kitdb SQL: PRAGMA %s requires one table argument: %w", plan.Name, err)
	}
	token := parser.cursor.Peek()
	switch token.Kind {
	case TokenString:
		plan.Argument = parser.cursor.Take().Text
	case TokenIdentifier:
		plan.Argument, err = parser.qualifiedIdentifier()
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("kitdb SQL: PRAGMA %s expects a table identifier, got %q", plan.Name, token.Text)
	}
	if plan.Argument == "" {
		return nil, fmt.Errorf("kitdb SQL: PRAGMA %s expects a non-empty table identifier", plan.Name)
	}
	if err := parser.cursor.ExpectSymbol(")"); err != nil {
		return nil, fmt.Errorf("kitdb SQL: PRAGMA %s accepts exactly one table argument: %w", plan.Name, err)
	}
	return plan, nil
}

func (parser *statementParser) parseAnalyze() (*AnalyzeStatement, error) {
	plan := &AnalyzeStatement{}
	if parser.cursor.Peek().Kind == TokenEOF ||
		(parser.cursor.Peek().Kind == TokenSymbol && parser.cursor.Peek().Text == ";") {
		return plan, nil
	}
	table, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan.Table = table
	return plan, nil
}

func (parser *statementParser) parseReindex() (*ReindexStatement, error) {
	if err := parser.cursor.ExpectKeyword("table"); err != nil {
		return nil, fmt.Errorf("kitdb SQL: REINDEX currently supports TABLE: %w", err)
	}
	table, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	return &ReindexStatement{Table: table}, nil
}

func (parser *statementParser) parseCreateIndex(unique bool) (*CreateIndexStatement, error) {
	plan := &CreateIndexStatement{Unique: unique}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("not"); err != nil {
			return nil, err
		}
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfNotExists = true
	}
	name, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan.Name = name
	if err := parser.cursor.ExpectKeyword("on"); err != nil {
		return nil, err
	}
	plan.Table, err = parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan.Columns, err = parser.identifierList()
	if err != nil {
		return nil, err
	}
	if len(plan.Columns) == 0 {
		return nil, fmt.Errorf("kitdb SQL: CREATE INDEX needs at least one column")
	}
	if parser.cursor.AcceptKeyword("where") {
		plan.Conditions, err = parser.parseConditions()
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (parser *statementParser) parseDropIndex() (*DropIndexStatement, error) {
	plan := &DropIndexStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfExists = true
	}
	name, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan.Name = name
	return plan, nil
}

func (parser *statementParser) parseDropTable() (*DropTableStatement, error) {
	plan := &DropTableStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfExists = true
	}
	name, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan.Name = name
	if parser.cursor.AcceptKeyword("cascade") {
		return nil, fmt.Errorf("kitdb SQL: DROP TABLE CASCADE is not safely executable yet")
	}
	_ = parser.cursor.AcceptKeyword("restrict")
	return plan, nil
}

func (parser *statementParser) parseAlterTable() (*AlterTableStatement, error) {
	if err := parser.cursor.ExpectKeyword("table"); err != nil {
		return nil, err
	}
	table, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan := &AlterTableStatement{Table: table}
	switch {
	case parser.cursor.AcceptKeyword("set"):
		if err := parser.cursor.ExpectKeyword("partition"); err != nil {
			return nil, fmt.Errorf("kitdb SQL: ALTER TABLE SET supports PARTITION BY: %w", err)
		}
		plan.Partition, err = parser.parsePartitionDefinition()
		if err != nil {
			return nil, err
		}
		plan.Action = AlterTableSetPartitioning
	case parser.cursor.AcceptKeyword("add"):
		_ = parser.cursor.AcceptKeyword("column")
		column, err := parser.parseColumn()
		if err != nil {
			return nil, err
		}
		plan.Action, plan.Column = AlterTableAddColumn, &column
	case parser.cursor.AcceptKeyword("drop"):
		if parser.cursor.AcceptKeyword("partitioning") {
			plan.Action = AlterTableDropPartitioning
			break
		}
		if parser.cursor.AcceptKeyword("partition") {
			return nil, fmt.Errorf("kitdb SQL: use DROP PARTITIONING; DROP PARTITION could imply deleting data")
		}
		_ = parser.cursor.AcceptKeyword("column")
		if parser.cursor.AcceptKeyword("if") {
			if err := parser.cursor.ExpectKeyword("exists"); err != nil {
				return nil, err
			}
			plan.IfExists = true
		}
		plan.OldName, err = parser.identifier()
		if err != nil {
			return nil, err
		}
		if parser.cursor.AcceptKeyword("cascade") {
			return nil, fmt.Errorf("kitdb SQL: ALTER TABLE DROP COLUMN CASCADE is not safely executable yet")
		}
		_ = parser.cursor.AcceptKeyword("restrict")
		plan.Action = AlterTableDropColumn
	case parser.cursor.AcceptKeyword("rename"):
		if parser.cursor.AcceptKeyword("column") {
			plan.OldName, err = parser.identifier()
			if err != nil {
				return nil, err
			}
			if err := parser.cursor.ExpectKeyword("to"); err != nil {
				return nil, err
			}
			plan.NewName, err = parser.identifier()
			if err != nil {
				return nil, err
			}
			plan.Action = AlterTableRenameColumn
			break
		}
		if err := parser.cursor.ExpectKeyword("to"); err != nil {
			return nil, err
		}
		plan.NewName, err = parser.identifier()
		if err != nil {
			return nil, err
		}
		plan.Action = AlterTableRenameTable
	case parser.cursor.AcceptKeyword("alter"):
		_ = parser.cursor.AcceptKeyword("column")
		plan.OldName, err = parser.identifier()
		if err != nil {
			return nil, err
		}
		switch {
		case parser.cursor.AcceptKeyword("set"):
			switch {
			case parser.cursor.AcceptKeyword("searchable"):
				plan.Action = AlterTableSetSearchable
				plan.SearchWeight = 1
				if parser.cursor.AcceptKeyword("weight") {
					plan.SearchWeight, err = parser.nonNegativeInteger("SEARCHABLE WEIGHT")
					if err != nil {
						return nil, err
					}
					if plan.SearchWeight < 1 || plan.SearchWeight > 16 {
						return nil, fmt.Errorf("kitdb SQL: SEARCHABLE WEIGHT must be between 1 and 16")
					}
				}
			case parser.cursor.AcceptKeyword("analytics"):
				plan.Action = AlterTableSetAnalytics
			default:
				return nil, fmt.Errorf("kitdb SQL: ALTER COLUMN SET supports SEARCHABLE or ANALYTICS")
			}
		case parser.cursor.AcceptKeyword("drop"):
			switch {
			case parser.cursor.AcceptKeyword("searchable"):
				plan.Action = AlterTableDropSearchable
			case parser.cursor.AcceptKeyword("analytics"):
				plan.Action = AlterTableDropAnalytics
			default:
				return nil, fmt.Errorf("kitdb SQL: ALTER COLUMN DROP supports SEARCHABLE or ANALYTICS")
			}
		default:
			return nil, fmt.Errorf("kitdb SQL: ALTER COLUMN supports SET/DROP SEARCHABLE or ANALYTICS")
		}
	default:
		return nil, fmt.Errorf("kitdb SQL: ALTER TABLE supports ADD/DROP/RENAME/ALTER COLUMN, SET/DROP PARTITIONING and RENAME TO")
	}
	return plan, nil
}

func (parser *statementParser) parseCreateTable() (*CreateTableStatement, error) {
	plan := &CreateTableStatement{}
	if parser.cursor.AcceptKeyword("if") {
		if err := parser.cursor.ExpectKeyword("not"); err != nil {
			return nil, err
		}
		if err := parser.cursor.ExpectKeyword("exists"); err != nil {
			return nil, err
		}
		plan.IfNotExists = true
	}
	name, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan.Name = name
	if err := parser.cursor.ExpectSymbol("("); err != nil {
		return nil, err
	}
	if parser.cursor.AcceptSymbol(")") {
		return nil, fmt.Errorf("kitdb SQL: CREATE TABLE needs at least one column")
	}
	seen := make(map[string]string)
	for {
		switch {
		case parser.cursor.AcceptKeyword("primary"):
			if err := parser.cursor.ExpectKeyword("key"); err != nil {
				return nil, err
			}
			if len(plan.PrimaryKey) != 0 {
				return nil, fmt.Errorf("kitdb SQL: CREATE TABLE repeats PRIMARY KEY")
			}
			plan.PrimaryKey, err = parser.identifierList()
			if err != nil {
				return nil, err
			}
		case parser.cursor.AcceptKeyword("unique"):
			fields, err := parser.identifierList()
			if err != nil {
				return nil, err
			}
			plan.Unique = append(plan.Unique, fields)
		case parser.cursor.AcceptKeyword("foreign"):
			foreign, err := parser.parseForeignKey("")
			if err != nil {
				return nil, err
			}
			plan.ForeignKeys = append(plan.ForeignKeys, foreign)
		case parser.cursor.AcceptKeyword("check"):
			check, err := parser.parseCheck("", "")
			if err != nil {
				return nil, err
			}
			plan.Checks = append(plan.Checks, check)
		case parser.cursor.AcceptKeyword("constraint"):
			constraintName, err := parser.identifier()
			if err != nil {
				return nil, err
			}
			switch {
			case parser.cursor.AcceptKeyword("primary"):
				if err := parser.cursor.ExpectKeyword("key"); err != nil {
					return nil, err
				}
				if len(plan.PrimaryKey) != 0 {
					return nil, fmt.Errorf("kitdb SQL: CREATE TABLE repeats PRIMARY KEY")
				}
				plan.PrimaryKey, err = parser.identifierList()
			case parser.cursor.AcceptKeyword("unique"):
				var fields []string
				fields, err = parser.identifierList()
				plan.Unique = append(plan.Unique, fields)
			case parser.cursor.AcceptKeyword("foreign"):
				var foreign ForeignKeyDefinition
				foreign, err = parser.parseForeignKey(constraintName)
				plan.ForeignKeys = append(plan.ForeignKeys, foreign)
			case parser.cursor.AcceptKeyword("check"):
				var check CheckDefinition
				check, err = parser.parseCheck(constraintName, "")
				plan.Checks = append(plan.Checks, check)
			default:
				return nil, fmt.Errorf("kitdb SQL: unsupported named constraint %q", parser.cursor.Peek().Text)
			}
			if err != nil {
				return nil, err
			}
		default:
			if len(plan.Columns) >= MaximumDDLColumns {
				return nil, fmt.Errorf("kitdb SQL: CREATE TABLE exceeds %d columns", MaximumDDLColumns)
			}
			column, err := parser.parseColumn()
			if err != nil {
				return nil, err
			}
			key := strings.ToLower(column.Name)
			if previous := seen[key]; previous != "" {
				return nil, fmt.Errorf("kitdb SQL: columns %q and %q have the same name", previous, column.Name)
			}
			seen[key] = column.Name
			plan.Columns = append(plan.Columns, column)
			if column.Reference != nil {
				plan.ForeignKeys = append(plan.ForeignKeys, *column.Reference)
			}
			plan.Checks = append(plan.Checks, column.Checks...)
		}
		if parser.cursor.AcceptSymbol(")") {
			break
		}
		if err := parser.cursor.ExpectSymbol(","); err != nil {
			return nil, err
		}
		if parser.cursor.Peek().Kind == TokenEOF || parser.cursor.AcceptSymbol(")") {
			return nil, fmt.Errorf("kitdb SQL: trailing comma in CREATE TABLE")
		}
	}
	if len(plan.Columns) == 0 {
		return nil, fmt.Errorf("kitdb SQL: CREATE TABLE needs at least one column")
	}
	if parser.cursor.AcceptKeyword("partition") {
		plan.Partition, err = parser.parsePartitionDefinition()
		if err != nil {
			return nil, err
		}
	}
	if parser.cursor.AcceptKeyword("without") {
		if err := parser.cursor.ExpectKeyword("rowid"); err != nil {
			return nil, err
		}
	}
	_ = parser.cursor.AcceptKeyword("strict")
	if err := normalizeCreateTable(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (parser *statementParser) parsePartitionDefinition() (*PartitionDefinition, error) {
	if err := parser.cursor.ExpectKeyword("by"); err != nil {
		return nil, err
	}
	partition := &PartitionDefinition{}
	switch {
	case parser.cursor.AcceptKeyword("hash"):
		partition.Strategy = "hash"
	case parser.cursor.AcceptKeyword("range"):
		partition.Strategy = "range"
	default:
		return nil, fmt.Errorf("kitdb SQL: PARTITION BY supports HASH or RANGE")
	}
	fields, err := parser.identifierList()
	if err != nil {
		return nil, err
	}
	if len(fields) != 1 {
		return nil, fmt.Errorf("kitdb SQL: PARTITION BY needs exactly one field")
	}
	partition.Field = fields[0]
	return partition, nil
}

func (parser *statementParser) parseColumn() (ColumnDefinition, error) {
	name, err := parser.identifier()
	if err != nil {
		return ColumnDefinition{}, err
	}
	serial := true
	var typeInfo Type
	var choices []string
	var modifiers columnTypeModifiers
	var domainName string
	switch {
	case parser.cursor.AcceptKeyword("smallserial"), parser.cursor.AcceptKeyword("serial2"):
		typeInfo, _ = LookupKind("smallint")
	case parser.cursor.AcceptKeyword("serial"), parser.cursor.AcceptKeyword("serial4"):
		typeInfo, _ = LookupKind("int32")
	case parser.cursor.AcceptKeyword("bigserial"), parser.cursor.AcceptKeyword("serial8"):
		typeInfo, _ = LookupKind("bigint")
	default:
		serial = false
		if token := parser.cursor.Peek(); token.Kind == TokenIdentifier {
			if _, known := ResolveName(strings.ToLower(token.Text)); !known && !strings.EqualFold(token.Text, "double") && !strings.EqualFold(token.Text, "character") {
				domainName, err = parser.domainIdentifier()
			} else {
				typeInfo, choices, modifiers, err = parser.columnType()
			}
		} else {
			typeInfo, choices, modifiers, err = parser.columnType()
		}
	}
	if err != nil {
		return ColumnDefinition{}, fmt.Errorf("kitdb SQL: column %q: %w", name, err)
	}
	column := ColumnDefinition{
		DomainName: domainName,
		Name:       name, Type: typeInfo, Choices: choices,
		Precision: modifiers.Precision, Scale: modifiers.Scale,
		TimePrecision: modifiers.TimePrecision, TextLength: modifiers.TextLength,
	}
	if serial {
		column.SequenceMode, column.SequenceCache, column.NotNull = "serial", 1, true
	}
	explicitNull := false
	for {
		token := parser.cursor.Peek()
		if token.Kind == TokenEOF || (token.Kind == TokenSymbol && (token.Text == "," || token.Text == ")" || token.Text == ";")) {
			break
		}
		switch {
		case parser.cursor.AcceptKeyword("primary"):
			if err := parser.cursor.ExpectKeyword("key"); err != nil {
				return ColumnDefinition{}, err
			}
			if column.Primary {
				return ColumnDefinition{}, fmt.Errorf("kitdb SQL: column %q repeats PRIMARY KEY", name)
			}
			column.Primary = true
		case parser.cursor.AcceptKeyword("not"):
			if err := parser.cursor.ExpectKeyword("null"); err != nil {
				return ColumnDefinition{}, err
			}
			column.NotNull = true
		case parser.cursor.AcceptKeyword("null"):
			explicitNull = true
		case parser.cursor.AcceptKeyword("generated"):
			if column.SequenceMode != "" || column.HasDefault {
				return ColumnDefinition{}, fmt.Errorf("kitdb SQL: generated column cannot have another default")
			}
			if _, valid := SequenceDataTypeForKind(typeInfo.Kind); !valid {
				return ColumnDefinition{}, fmt.Errorf("kitdb SQL: IDENTITY requires SMALLINT, INTEGER or BIGINT")
			}
			if parser.cursor.AcceptKeyword("always") {
				column.SequenceMode = "always"
			} else {
				if err := parser.cursor.ExpectKeyword("by"); err != nil {
					return ColumnDefinition{}, err
				}
				if err := parser.cursor.ExpectKeyword("default"); err != nil {
					return ColumnDefinition{}, err
				}
				column.SequenceMode = "by_default"
			}
			if err := parser.cursor.ExpectKeyword("as"); err != nil {
				return ColumnDefinition{}, err
			}
			if err := parser.cursor.ExpectKeyword("identity"); err != nil {
				return ColumnDefinition{}, err
			}
			column.SequenceCache = 1
			if parser.cursor.AcceptSymbol("(") {
				if !parser.cursor.AcceptKeyword("cache") {
					return ColumnDefinition{}, fmt.Errorf("kitdb SQL: IDENTITY currently supports CACHE as its only sequence option")
				}
				cache, err := parser.sequenceInteger()
				if err != nil {
					return ColumnDefinition{}, err
				}
				if cache < 1 || cache > 4096 {
					return ColumnDefinition{}, fmt.Errorf("kitdb SQL: IDENTITY CACHE must be between 1 and 4096")
				}
				column.SequenceCache = cache
				if err := parser.cursor.ExpectSymbol(")"); err != nil {
					return ColumnDefinition{}, err
				}
			}
			column.NotNull = true
		case parser.cursor.AcceptKeyword("unique"):
			column.Unique = true
		case parser.cursor.AcceptKeyword("searchable"):
			if column.Searchable {
				return ColumnDefinition{}, fmt.Errorf("kitdb SQL: column %q repeats SEARCHABLE", name)
			}
			if column.DomainName == "" && column.Type.Family != FamilyText && column.Type.Family != FamilyIdentifier &&
				column.Type.Family != FamilyChoice {
				return ColumnDefinition{}, fmt.Errorf(
					"kitdb SQL: column %q SEARCHABLE requires a text-compatible type", name,
				)
			}
			column.Searchable = true
			column.SearchWeight = 1
			if parser.cursor.AcceptKeyword("weight") {
				weight, err := parser.nonNegativeInteger("SEARCHABLE WEIGHT")
				if err != nil {
					return ColumnDefinition{}, err
				}
				if weight < 1 || weight > 16 {
					return ColumnDefinition{}, fmt.Errorf("kitdb SQL: SEARCHABLE WEIGHT must be between 1 and 16")
				}
				column.SearchWeight = weight
			}
		case parser.cursor.AcceptKeyword("analytics"):
			if column.Analytics {
				return ColumnDefinition{}, fmt.Errorf("kitdb SQL: column %q repeats ANALYTICS", name)
			}
			if column.DomainName != "" {
				column.Analytics = true
				continue
			}
			switch column.Type.Family {
			case FamilyInteger, FamilySystem, FamilyFloat, FamilyBoolean, FamilyText, FamilyIdentifier, FamilyChoice:
				column.Analytics = true
			default:
				return ColumnDefinition{}, fmt.Errorf(
					"kitdb SQL: column %q ANALYTICS uses an unsupported type", name,
				)
			}
		case parser.cursor.AcceptKeyword("default"):
			if column.HasDefault || column.SequenceMode != "" {
				return ColumnDefinition{}, fmt.Errorf("kitdb SQL: column %q repeats DEFAULT", name)
			}
			column.HasDefault = true
			if current, found := parser.currentTemporalLiteral(); found {
				column.Default = current
				continue
			}
			wrapped := parser.cursor.AcceptSymbol("(")
			if parser.cursor.AcceptKeyword("nextval") {
				if _, valid := SequenceDataTypeForKind(typeInfo.Kind); !valid {
					return ColumnDefinition{}, fmt.Errorf("kitdb SQL: sequence default requires SMALLINT, INTEGER or BIGINT")
				}
				if err := parser.cursor.ExpectSymbol("("); err != nil {
					return ColumnDefinition{}, err
				}
				literal, literalErr := parser.literal(false)
				if literalErr != nil || literal.Kind != LiteralString {
					return ColumnDefinition{}, fmt.Errorf("kitdb SQL: sequence default requires a literal sequence name")
				}
				column.SequenceName, err = ParseSequenceName(literal.Text)
				if err != nil {
					return ColumnDefinition{}, err
				}
				if err := parser.cursor.ExpectSymbol(")"); err != nil {
					return ColumnDefinition{}, err
				}
				column.SequenceMode, column.HasDefault = "default", false
			} else {
				column.Default, err = parser.literal(false)
			}
			if err != nil {
				return ColumnDefinition{}, err
			}
			if wrapped {
				if err := parser.cursor.ExpectSymbol(")"); err != nil {
					return ColumnDefinition{}, err
				}
			}
		case parser.cursor.AcceptKeyword("references"):
			if column.Reference != nil {
				return ColumnDefinition{}, fmt.Errorf("kitdb SQL: column %q repeats REFERENCES", name)
			}
			reference := ForeignKeyDefinition{Columns: []string{name}}
			if err := parser.parseReferenceTarget(&reference); err != nil {
				return ColumnDefinition{}, err
			}
			column.Reference = &reference
		case parser.cursor.AcceptKeyword("check"):
			check, err := parser.parseCheck("", name)
			if err != nil {
				return ColumnDefinition{}, err
			}
			column.Checks = append(column.Checks, check)
		case parser.cursor.AcceptKeyword("constraint"):
			constraintName, err := parser.identifier()
			if err != nil {
				return ColumnDefinition{}, err
			}
			if !parser.cursor.AcceptKeyword("check") {
				return ColumnDefinition{}, fmt.Errorf(
					"kitdb SQL: column constraint %q currently supports CHECK only", constraintName,
				)
			}
			check, err := parser.parseCheck(constraintName, name)
			if err != nil {
				return ColumnDefinition{}, err
			}
			column.Checks = append(column.Checks, check)
		case parser.cursor.AcceptKeyword("autoincrement"):
			return ColumnDefinition{}, fmt.Errorf("kitdb SQL: AUTOINCREMENT is not supported; use KITID or provide the key")
		default:
			return ColumnDefinition{}, fmt.Errorf("kitdb SQL: unsupported column clause %q", token.Text)
		}
	}
	if explicitNull && column.SequenceMode != "" && column.SequenceMode != "default" {
		return ColumnDefinition{}, fmt.Errorf("kitdb SQL: identity/serial column cannot be NULL")
	}
	return column, nil
}

func (parser *statementParser) parseForeignKey(name string) (ForeignKeyDefinition, error) {
	if err := parser.cursor.ExpectKeyword("key"); err != nil {
		return ForeignKeyDefinition{}, err
	}
	columns, err := parser.identifierList()
	if err != nil {
		return ForeignKeyDefinition{}, err
	}
	if err := parser.cursor.ExpectKeyword("references"); err != nil {
		return ForeignKeyDefinition{}, err
	}
	definition := ForeignKeyDefinition{Name: name, Columns: columns}
	if err := parser.parseReferenceTarget(&definition); err != nil {
		return ForeignKeyDefinition{}, err
	}
	return definition, nil
}

func (parser *statementParser) parseReferenceTarget(definition *ForeignKeyDefinition) error {
	target, err := parser.qualifiedIdentifier()
	if err != nil {
		return err
	}
	definition.TargetTable = target
	definition.TargetColumns, err = parser.identifierList()
	if err != nil {
		return fmt.Errorf("kitdb SQL: REFERENCES requires an explicit target column list: %w", err)
	}
	for parser.cursor.AcceptKeyword("on") {
		actionKind := ""
		switch {
		case parser.cursor.AcceptKeyword("delete"):
			actionKind = "delete"
		case parser.cursor.AcceptKeyword("update"):
			actionKind = "update"
		default:
			return fmt.Errorf("kitdb SQL: REFERENCES ON expects DELETE or UPDATE")
		}
		action, err := parser.parseReferentialAction()
		if err != nil {
			return err
		}
		if actionKind == "delete" {
			if definition.OnDelete != "" {
				return fmt.Errorf("kitdb SQL: REFERENCES repeats ON DELETE")
			}
			definition.OnDelete = action
		} else {
			if definition.OnUpdate != "" {
				return fmt.Errorf("kitdb SQL: REFERENCES repeats ON UPDATE")
			}
			definition.OnUpdate = action
		}
	}
	return nil
}

func (parser *statementParser) parseReferentialAction() (string, error) {
	switch {
	case parser.cursor.AcceptKeyword("restrict"):
		return "restrict", nil
	case parser.cursor.AcceptKeyword("cascade"):
		return "cascade", nil
	case parser.cursor.AcceptKeyword("no"):
		if err := parser.cursor.ExpectKeyword("action"); err != nil {
			return "", err
		}
		return "no action", nil
	case parser.cursor.AcceptKeyword("set"):
		if parser.cursor.AcceptKeyword("null") {
			return "set null", nil
		}
		if parser.cursor.AcceptKeyword("default") {
			return "set default", nil
		}
		return "", fmt.Errorf("kitdb SQL: REFERENCES SET expects NULL or DEFAULT")
	default:
		return "", fmt.Errorf("kitdb SQL: unsupported referential action %q", parser.cursor.Peek().Text)
	}
}

func (parser *statementParser) parseCheck(name, column string) (CheckDefinition, error) {
	if err := parser.cursor.ExpectSymbol("("); err != nil {
		return CheckDefinition{}, err
	}
	expression, err := parser.parseCheckOr()
	if err != nil {
		return CheckDefinition{}, err
	}
	if err := parser.cursor.ExpectSymbol(")"); err != nil {
		return CheckDefinition{}, err
	}
	return CheckDefinition{Name: name, Column: column, Expression: expression}, nil
}

func (parser *statementParser) parseCheckOr() (CheckPlan, error) {
	left, err := parser.parseCheckAnd()
	if err != nil {
		return CheckPlan{}, err
	}
	for parser.cursor.AcceptKeyword("or") {
		right, err := parser.parseCheckAnd()
		if err != nil {
			return CheckPlan{}, err
		}
		left = CheckPlan{Kind: "binary", Operator: "or", Arguments: []CheckPlan{left, right}}
	}
	return left, nil
}

func (parser *statementParser) parseCheckAnd() (CheckPlan, error) {
	left, err := parser.parseCheckNot()
	if err != nil {
		return CheckPlan{}, err
	}
	for parser.cursor.AcceptKeyword("and") {
		right, err := parser.parseCheckNot()
		if err != nil {
			return CheckPlan{}, err
		}
		left = CheckPlan{Kind: "binary", Operator: "and", Arguments: []CheckPlan{left, right}}
	}
	return left, nil
}

func (parser *statementParser) parseCheckNot() (CheckPlan, error) {
	if parser.cursor.AcceptKeyword("not") {
		child, err := parser.parseCheckNot()
		if err != nil {
			return CheckPlan{}, err
		}
		return CheckPlan{Kind: "unary", Operator: "not", Arguments: []CheckPlan{child}}, nil
	}
	return parser.parseCheckComparison()
}

func (parser *statementParser) parseCheckComparison() (CheckPlan, error) {
	left, err := parser.parseCheckPrimary()
	if err != nil {
		return CheckPlan{}, err
	}
	if parser.cursor.AcceptKeyword("is") {
		operator := "is null"
		if parser.cursor.AcceptKeyword("not") {
			operator = "is not null"
		}
		if err := parser.cursor.ExpectKeyword("null"); err != nil {
			return CheckPlan{}, err
		}
		return CheckPlan{Kind: "unary", Operator: operator, Arguments: []CheckPlan{left}}, nil
	}
	operator := parser.cursor.Peek()
	if operator.Kind != TokenSymbol || !supportedComparison(operator.Text) {
		return left, nil
	}
	parser.cursor.Take()
	right, err := parser.parseCheckPrimary()
	if err != nil {
		return CheckPlan{}, err
	}
	return CheckPlan{Kind: "binary", Operator: operator.Text, Arguments: []CheckPlan{left, right}}, nil
}

func (parser *statementParser) parseCheckPrimary() (CheckPlan, error) {
	if parser.cursor.AcceptSymbol("(") {
		expression, err := parser.parseCheckOr()
		if err != nil {
			return CheckPlan{}, err
		}
		if err := parser.cursor.ExpectSymbol(")"); err != nil {
			return CheckPlan{}, err
		}
		return expression, nil
	}
	token := parser.cursor.Peek()
	literal := token.Kind == TokenString || token.Kind == TokenNumber ||
		(token.Kind == TokenSymbol && token.Text == "-")
	if token.Kind == TokenIdentifier {
		switch strings.ToLower(token.Text) {
		case "null", "true", "false", "current_timestamp", "current_date", "current_time", "localtimestamp":
			literal = true
		}
	}
	if literal {
		item, err := parser.literal(false)
		if err != nil {
			return CheckPlan{}, err
		}
		return CheckPlan{Kind: "literal", Literal: item}, nil
	}
	field, err := parser.qualifiedIdentifier()
	if err != nil {
		return CheckPlan{}, err
	}
	return CheckPlan{Kind: "field", Field: field}, nil
}

func (parser *statementParser) columnType() (Type, []string, columnTypeModifiers, error) {
	token := parser.cursor.Take()
	if token.Kind != TokenIdentifier {
		return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("expected a column type, got %q", token.Text)
	}
	name := strings.ToLower(token.Text)
	if name == "double" && parser.cursor.AcceptKeyword("precision") {
		name = "double precision"
	} else if name == "character" && parser.cursor.AcceptKeyword("varying") {
		name = "character varying"
	}
	typeInfo, found := ResolveName(name)
	if !found {
		return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("unsupported column type %q", token.Text)
	}
	var choices []string
	var modifiers columnTypeModifiers
	if parser.cursor.AcceptSymbol("(") {
		if name == "choice" || name == "enum" {
			for {
				item := parser.cursor.Take()
				if item.Kind != TokenString || item.Text == "" {
					return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("%s values must be non-empty strings", strings.ToUpper(name))
				}
				for _, existing := range choices {
					if existing == item.Text {
						return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("%s repeats value %q", strings.ToUpper(name), item.Text)
					}
				}
				choices = append(choices, item.Text)
				if parser.cursor.AcceptSymbol(")") {
					break
				}
				if err := parser.cursor.ExpectSymbol(","); err != nil {
					return Type{}, nil, columnTypeModifiers{}, err
				}
			}
		} else {
			parameters := make([]int, 0, 2)
			for {
				parameter := parser.cursor.Take()
				if parameter.Kind != TokenNumber || strings.ContainsAny(parameter.Text, ".eE") {
					return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("type parameters for %s must be integers", strings.ToUpper(name))
				}
				value, err := strconv.Atoi(parameter.Text)
				if err != nil {
					return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("type parameter for %s is out of range", strings.ToUpper(name))
				}
				parameters = append(parameters, value)
				if parser.cursor.AcceptSymbol(")") {
					break
				}
				if err := parser.cursor.ExpectSymbol(","); err != nil {
					return Type{}, nil, columnTypeModifiers{}, err
				}
			}
			switch {
			case typeInfo.Family == FamilyDecimal:
				if len(parameters) > 2 {
					return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("type %s has too many parameters", strings.ToUpper(name))
				}
				modifiers.Precision = parameters[0]
				if len(parameters) == 2 {
					modifiers.Scale = parameters[1]
				}
				if modifiers.Precision < 1 || modifiers.Precision > MaximumDecimalPrecision ||
					modifiers.Scale < 0 || modifiers.Scale > modifiers.Precision {
					return Type{}, nil, columnTypeModifiers{}, fmt.Errorf(
						"%s precision must be 1..%d and scale must be 0..precision",
						strings.ToUpper(name), MaximumDecimalPrecision,
					)
				}
			case typeInfo.ID == TypeTime || typeInfo.ID == TypeTimestamp || typeInfo.ID == TypeTimestampTZ:
				if len(parameters) != 1 || parameters[0] < 0 || parameters[0] > MaximumTemporalPrecision {
					return Type{}, nil, columnTypeModifiers{}, fmt.Errorf(
						"%s precision must be 0..%d", strings.ToUpper(name), MaximumTemporalPrecision,
					)
				}
				value := parameters[0]
				modifiers.TimePrecision = &value
			case typeInfo.ID == TypeVarchar || typeInfo.ID == TypeChar:
				if len(parameters) != 1 || parameters[0] < 1 || parameters[0] > MaximumTextLength {
					return Type{}, nil, columnTypeModifiers{}, fmt.Errorf(
						"%s length must be 1..%d", strings.ToUpper(name), MaximumTextLength,
					)
				}
				value := parameters[0]
				modifiers.TextLength = &value
			default:
				return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("type %s does not accept parameters", strings.ToUpper(name))
			}
		}
	}
	if typeInfo.ID == TypeChar && modifiers.TextLength == nil {
		length := 1
		modifiers.TextLength = &length
	}
	switch typeInfo.ID {
	case TypeTimestamp:
		if parser.cursor.AcceptKeyword("with") {
			if err := parser.cursor.ExpectKeyword("time"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
			if err := parser.cursor.ExpectKeyword("zone"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
			typeInfo, _ = LookupKind("timestamptz")
		} else if parser.cursor.AcceptKeyword("without") {
			if err := parser.cursor.ExpectKeyword("time"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
			if err := parser.cursor.ExpectKeyword("zone"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
		}
	case TypeTime:
		if parser.cursor.AcceptKeyword("with") {
			if err := parser.cursor.ExpectKeyword("time"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
			if err := parser.cursor.ExpectKeyword("zone"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
			return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("TIME WITH TIME ZONE is outside the bounded KitDB temporal profile")
		}
		if parser.cursor.AcceptKeyword("without") {
			if err := parser.cursor.ExpectKeyword("time"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
			if err := parser.cursor.ExpectKeyword("zone"); err != nil {
				return Type{}, nil, columnTypeModifiers{}, err
			}
		}
	}
	if typeInfo.ID == TypeChoice && len(choices) == 0 {
		return Type{}, nil, columnTypeModifiers{}, fmt.Errorf("%s needs at least one string value", strings.ToUpper(name))
	}
	return typeInfo, choices, modifiers, nil
}

func normalizeCreateTable(plan *CreateTableStatement) error {
	primary := append([]string(nil), plan.PrimaryKey...)
	for _, column := range plan.Columns {
		if column.Primary {
			primary = append(primary, column.Name)
		}
	}
	if plan.Partition != nil {
		column, found := findDDLColumn(plan.Columns, plan.Partition.Field)
		if !found {
			return fmt.Errorf("kitdb SQL: PARTITION BY references missing column %q", plan.Partition.Field)
		}
		if column.DomainName == "" && column.Type.Family != FamilyInteger {
			return fmt.Errorf("kitdb SQL: PARTITION BY %s requires an integer field", strings.ToUpper(plan.Partition.Strategy))
		}
		plan.Partition.Field = column.Name
	}
	if len(primary) == 0 {
		return fmt.Errorf("kitdb SQL: table %q needs a PRIMARY KEY", plan.Name)
	}
	seenPrimary := make(map[string]struct{}, len(primary))
	for _, requested := range primary {
		column, found := findDDLColumn(plan.Columns, requested)
		if !found {
			return fmt.Errorf("kitdb SQL: PRIMARY KEY references missing column %q", requested)
		}
		if column.Type.ID == TypeInterval {
			return fmt.Errorf("kitdb SQL: INTERVAL cannot be a PRIMARY KEY in the bounded temporal profile")
		}
		key := strings.ToLower(column.Name)
		if _, duplicate := seenPrimary[key]; duplicate {
			return fmt.Errorf("kitdb SQL: PRIMARY KEY repeats column %q", column.Name)
		}
		seenPrimary[key] = struct{}{}
	}
	plan.PrimaryKey = primary
	for _, unique := range plan.Unique {
		if len(unique) == 0 {
			return fmt.Errorf("kitdb SQL: UNIQUE needs at least one column")
		}
		seen := make(map[string]struct{}, len(unique))
		for _, requested := range unique {
			column, found := findDDLColumn(plan.Columns, requested)
			if !found {
				return fmt.Errorf("kitdb SQL: UNIQUE references missing column %q", requested)
			}
			if column.Type.ID == TypeInterval {
				return fmt.Errorf("kitdb SQL: INTERVAL cannot participate in UNIQUE in the bounded temporal profile")
			}
			key := strings.ToLower(column.Name)
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("kitdb SQL: UNIQUE repeats column %q", column.Name)
			}
			seen[key] = struct{}{}
		}
	}
	for _, column := range plan.Columns {
		if column.Unique && column.Type.ID == TypeInterval {
			return fmt.Errorf("kitdb SQL: INTERVAL column %q cannot be UNIQUE in the bounded temporal profile", column.Name)
		}
	}
	constraintNames := make(map[string]string, len(plan.ForeignKeys)+len(plan.Checks))
	for index := range plan.ForeignKeys {
		foreign := &plan.ForeignKeys[index]
		if len(foreign.Columns) == 0 || len(foreign.Columns) != len(foreign.TargetColumns) {
			return fmt.Errorf("kitdb SQL: FOREIGN KEY needs matching local and target column lists")
		}
		seen := make(map[string]struct{}, len(foreign.Columns))
		for _, requested := range foreign.Columns {
			column, found := findDDLColumn(plan.Columns, requested)
			if !found {
				return fmt.Errorf("kitdb SQL: FOREIGN KEY references missing local column %q", requested)
			}
			key := strings.ToLower(column.Name)
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("kitdb SQL: FOREIGN KEY repeats local column %q", column.Name)
			}
			seen[key] = struct{}{}
		}
		if foreign.OnDelete == "" {
			foreign.OnDelete = "no action"
		}
		if foreign.OnUpdate == "" {
			foreign.OnUpdate = "no action"
		}
		if foreign.OnDelete != "no action" && foreign.OnDelete != "restrict" && foreign.OnDelete != "cascade" && foreign.OnDelete != "set null" && foreign.OnDelete != "set default" {
			return fmt.Errorf("kitdb SQL: ON DELETE %s is parsed but not safely executable yet", strings.ToUpper(foreign.OnDelete))
		}
		if foreign.OnUpdate != "no action" && foreign.OnUpdate != "restrict" && foreign.OnUpdate != "cascade" && foreign.OnUpdate != "set null" && foreign.OnUpdate != "set default" {
			return fmt.Errorf("kitdb SQL: ON UPDATE %s is parsed but not safely executable yet", strings.ToUpper(foreign.OnUpdate))
		}
		if foreign.Name != "" {
			key := strings.ToLower(foreign.Name)
			if previous := constraintNames[key]; previous != "" {
				return fmt.Errorf("kitdb SQL: constraints %q and %q share a name", previous, foreign.Name)
			}
			constraintNames[key] = foreign.Name
		}
	}
	for _, check := range plan.Checks {
		if check.Expression.Kind == "" {
			return fmt.Errorf("kitdb SQL: CHECK expression is empty")
		}
		if check.Name != "" {
			key := strings.ToLower(check.Name)
			if previous := constraintNames[key]; previous != "" {
				return fmt.Errorf("kitdb SQL: constraints %q and %q share a name", previous, check.Name)
			}
			constraintNames[key] = check.Name
		}
	}
	return nil
}

func findDDLColumn(columns []ColumnDefinition, requested string) (ColumnDefinition, bool) {
	for _, column := range columns {
		if column.Name == requested {
			return column, true
		}
	}
	for _, column := range columns {
		if strings.EqualFold(column.Name, requested) {
			return column, true
		}
	}
	return ColumnDefinition{}, false
}

func (parser *statementParser) parseInsert() (*InsertStatement, error) {
	if err := parser.cursor.ExpectKeyword("into"); err != nil {
		return nil, err
	}
	table, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan := &InsertStatement{Table: table}
	if parser.cursor.AcceptSymbol("(") {
		plan.Columns, err = parser.identifierListAfterOpen()
		if err != nil {
			return nil, err
		}
		if duplicate := duplicateIdentifier(plan.Columns); duplicate != "" {
			return nil, fmt.Errorf("kitdb SQL: duplicate INSERT column %q", duplicate)
		}
	}
	if parser.cursor.AcceptKeyword("overriding") {
		if parser.cursor.AcceptKeyword("system") {
			plan.Overriding = "system"
		} else if parser.cursor.AcceptKeyword("user") {
			plan.Overriding = "user"
		} else {
			return nil, fmt.Errorf("kitdb SQL: OVERRIDING requires SYSTEM or USER")
		}
		if err := parser.cursor.ExpectKeyword("value"); err != nil {
			return nil, err
		}
	}
	if parser.cursor.AcceptKeyword("default") {
		if err := parser.cursor.ExpectKeyword("values"); err != nil {
			return nil, err
		}
		plan.DefaultRows = 1
	} else if parser.cursor.AcceptKeyword("select") {
		plan.Select, err = parser.parseSelect()
		if err != nil {
			return nil, err
		}
	} else if parser.cursor.AcceptKeyword("with") {
		plan.Select, err = parser.parseWithSelect()
		if err != nil {
			return nil, err
		}
	} else {
		if err := parser.cursor.ExpectKeyword("values"); err != nil {
			return nil, err
		}
		literalOnly := true
		for {
			if len(plan.Values) >= MaximumInsertRows {
				return nil, fmt.Errorf("kitdb SQL: INSERT exceeds %d rows", MaximumInsertRows)
			}
			if err := parser.cursor.ExpectSymbol("("); err != nil {
				return nil, err
			}
			row := make([]ExpressionPlan, 0, len(plan.Columns))
			if parser.cursor.AcceptSymbol(")") {
				return nil, fmt.Errorf("kitdb SQL: INSERT row cannot be empty")
			}
			for {
				expression := &ExpressionPlan{Kind: "literal", Literal: Literal{Kind: LiteralDefault}}
				if !parser.cursor.AcceptKeyword("default") {
					expression, err = parser.parseExpression()
				}
				if err != nil {
					return nil, err
				}
				literalOnly = literalOnly && expression.Kind == "literal"
				row = append(row, *expression)
				if parser.cursor.AcceptSymbol(")") {
					break
				}
				if err := parser.cursor.ExpectSymbol(","); err != nil {
					return nil, err
				}
			}
			if len(plan.Columns) != 0 && len(row) != len(plan.Columns) {
				return nil, fmt.Errorf("kitdb SQL: INSERT has %d columns but row has %d values", len(plan.Columns), len(row))
			}
			if len(plan.Values) != 0 && len(row) != len(plan.Values[0]) {
				return nil, fmt.Errorf("kitdb SQL: INSERT rows have different value counts")
			}
			plan.Values = append(plan.Values, row)
			if !parser.cursor.AcceptSymbol(",") {
				break
			}
		}
		if literalOnly {
			plan.Rows = make([][]Literal, len(plan.Values))
			for i, row := range plan.Values {
				plan.Rows[i] = make([]Literal, len(row))
				for j := range row {
					plan.Rows[i][j] = row[j].Literal
				}
			}
			plan.Values = nil
		}
	}
	if parser.cursor.AcceptKeyword("on") {
		if err := parser.cursor.ExpectKeyword("conflict"); err != nil {
			return nil, err
		}
		plan.Conflict, err = parser.parseConflict()
		if err != nil {
			return nil, err
		}
	}
	if parser.cursor.AcceptKeyword("returning") {
		plan.Returning, err = parser.projections(false)
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (parser *statementParser) parseSelect() (*SelectStatement, error) {
	plan, err := parser.parseSelectCore()
	if err != nil {
		return nil, err
	}
	for parser.cursor.AcceptKeyword("union") {
		if len(plan.SetOperations) >= MaximumSetOperations {
			return nil, fmt.Errorf(
				"kitdb SQL: SELECT exceeds %d set operations", MaximumSetOperations,
			)
		}
		if !parser.cursor.AcceptKeyword("all") {
			return nil, fmt.Errorf("kitdb SQL: UNION currently requires ALL")
		}
		if err := parser.cursor.ExpectKeyword("select"); err != nil {
			return nil, err
		}
		branch, err := parser.parseSelectCore()
		if err != nil {
			return nil, err
		}
		plan.SetOperations = append(plan.SetOperations, SetOperation{Kind: "union all", Select: branch})
	}
	if err := parser.parseSelectTail(plan); err != nil {
		return nil, err
	}
	if len(plan.SetOperations) != 0 {
		if selectTreeHasSearch(plan) {
			return nil, fmt.Errorf("kitdb SQL: SEARCH cannot be combined with UNION ALL")
		}
		if plan.HasAfter {
			return nil, fmt.Errorf("kitdb SQL: AFTER cannot be combined with UNION ALL")
		}
	}
	if selectHasNestedSource(plan) && selectTreeHasSearch(plan) {
		return nil, fmt.Errorf("kitdb SQL: SEARCH cannot be materialized by WITH or a derived table")
	}
	return plan, nil
}

func (parser *statementParser) parseWithSelect() (*SelectStatement, error) {
	if parser.cursor.AcceptKeyword("recursive") {
		return nil, fmt.Errorf("kitdb SQL: WITH RECURSIVE is not supported")
	}
	commonTables := make([]CommonTableExpression, 0, 2)
	seen := make(map[string]struct{})
	for {
		if len(commonTables) >= MaximumCommonTables {
			return nil, fmt.Errorf("kitdb SQL: WITH exceeds %d common table expressions", MaximumCommonTables)
		}
		name, err := parser.identifier()
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(name)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("kitdb SQL: WITH repeats common table %q", name)
		}
		seen[key] = struct{}{}
		var columns []string
		if parser.cursor.Peek().Kind == TokenSymbol && parser.cursor.Peek().Text == "(" {
			columns, err = parser.identifierList()
			if err != nil {
				return nil, err
			}
		}
		if err := parser.cursor.ExpectKeyword("as"); err != nil {
			return nil, err
		}
		if err := parser.cursor.ExpectSymbol("("); err != nil {
			return nil, err
		}
		query, err := parser.parseNestedQuery()
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: WITH %q: %w", name, err)
		}
		if err := parser.cursor.ExpectSymbol(")"); err != nil {
			return nil, err
		}
		commonTables = append(commonTables, CommonTableExpression{Name: name, Columns: columns, Select: query})
		if !parser.cursor.AcceptSymbol(",") {
			break
		}
	}
	if err := parser.cursor.ExpectKeyword("select"); err != nil {
		return nil, fmt.Errorf("kitdb SQL: WITH must end in SELECT: %w", err)
	}
	plan, err := parser.parseSelect()
	if err != nil {
		return nil, err
	}
	plan.CommonTables = commonTables
	if selectTreeHasSearch(plan) {
		return nil, fmt.Errorf("kitdb SQL: SEARCH cannot be materialized by WITH or a derived table")
	}
	return plan, nil
}

func (parser *statementParser) parseNestedQuery() (*SelectStatement, error) {
	if parser.queryDepth >= MaximumQueryNesting {
		return nil, fmt.Errorf("kitdb SQL: query nesting exceeds %d levels", MaximumQueryNesting)
	}
	parser.queryDepth++
	defer func() { parser.queryDepth-- }()
	switch {
	case parser.cursor.AcceptKeyword("select"):
		return parser.parseSelect()
	case parser.cursor.AcceptKeyword("with"):
		return parser.parseWithSelect()
	default:
		return nil, fmt.Errorf("kitdb SQL: expected SELECT or WITH, got %q", parser.cursor.Peek().Text)
	}
}

func (parser *statementParser) parseSelectCore() (*SelectStatement, error) {
	plan := &SelectStatement{Distinct: parser.cursor.AcceptKeyword("distinct")}
	projection, err := parser.projections(true)
	if err != nil {
		return nil, err
	}
	plan.Projection = projection
	if !parser.cursor.AcceptKeyword("from") {
		return plan, nil
	}
	if parser.cursor.AcceptSymbol("(") {
		plan.Source, err = parser.parseNestedQuery()
		if err != nil {
			return nil, err
		}
		if err := parser.cursor.ExpectSymbol(")"); err != nil {
			return nil, err
		}
		plan.TableAlias, err = parser.optionalAlias()
		if err == nil && plan.TableAlias == "" {
			err = fmt.Errorf("kitdb SQL: derived table requires an alias")
		}
	} else {
		plan.Table, err = parser.qualifiedIdentifier()
		if err == nil {
			plan.TableAlias, err = parser.optionalAlias()
		}
	}
	if err != nil {
		return nil, err
	}
	plan.Joins, err = parser.parseJoins()
	if err != nil {
		return nil, err
	}
	if parser.cursor.AcceptKeyword("where") {
		plan.Predicate, err = parser.parsePredicate()
		if err != nil {
			return nil, err
		}
		plan.Search, plan.Predicate, err = extractSearchPredicate(plan.Predicate)
		if err != nil {
			return nil, err
		}
		if plan.Predicate != nil {
			plan.Conditions = plannerConditions(*plan.Predicate)
		}
	}
	if parser.cursor.AcceptKeyword("group") {
		if err := parser.cursor.ExpectKeyword("by"); err != nil {
			return nil, err
		}
		for {
			column, err := parser.columnReference()
			if err != nil {
				return nil, err
			}
			plan.GroupBy = append(plan.GroupBy, column)
			if !parser.cursor.AcceptSymbol(",") {
				break
			}
		}
	}
	if parser.cursor.AcceptKeyword("having") {
		plan.Having, err = parser.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (parser *statementParser) parseSelectTail(plan *SelectStatement) error {
	var err error
	if parser.cursor.AcceptKeyword("order") {
		if err := parser.cursor.ExpectKeyword("by"); err != nil {
			return err
		}
		for {
			expression, err := parser.parseExpression()
			if err != nil {
				return err
			}
			order := Order{Expression: expression}
			if expression.Kind == "field" {
				order.Column, order.Expression = expression.Field, nil
			}
			if parser.cursor.AcceptKeyword("desc") {
				order.Descending = true
			} else {
				_ = parser.cursor.AcceptKeyword("asc")
			}
			plan.Order = append(plan.Order, order)
			if !parser.cursor.AcceptSymbol(",") {
				break
			}
		}
	}
	if parser.cursor.AcceptKeyword("limit") {
		plan.Limit, err = parser.nonNegativeInteger("LIMIT")
		if err != nil {
			return err
		}
		plan.HasLimit = true
	}
	if parser.cursor.AcceptKeyword("offset") {
		plan.Offset, err = parser.nonNegativeInteger("OFFSET")
		if err != nil {
			return err
		}
	}
	if parser.cursor.AcceptKeyword("after") {
		plan.After, err = parser.searchLiteral("AFTER cursor")
		if err != nil {
			return err
		}
		plan.HasAfter = true
	}
	if plan.HasAfter && plan.Search == nil {
		return fmt.Errorf("kitdb SQL: AFTER is valid only with SEARCH")
	}
	if plan.HasAfter && plan.Offset != 0 {
		return fmt.Errorf("kitdb SQL: SEARCH AFTER cannot be combined with OFFSET")
	}
	return nil
}

func selectTreeHasSearch(plan *SelectStatement) bool {
	if plan == nil {
		return false
	}
	if plan.Search != nil {
		return true
	}
	if selectTreeHasSearch(plan.Source) {
		return true
	}
	for _, commonTable := range plan.CommonTables {
		if selectTreeHasSearch(commonTable.Select) {
			return true
		}
	}
	for _, operation := range plan.SetOperations {
		if selectTreeHasSearch(operation.Select) {
			return true
		}
	}
	return false
}

func selectHasNestedSource(plan *SelectStatement) bool {
	if plan == nil {
		return false
	}
	if plan.Source != nil || len(plan.CommonTables) != 0 {
		return true
	}
	for _, operation := range plan.SetOperations {
		if selectHasNestedSource(operation.Select) {
			return true
		}
	}
	return false
}

func (parser *statementParser) parseUpdate() (*UpdateStatement, error) {
	table, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectKeyword("set"); err != nil {
		return nil, err
	}
	plan := &UpdateStatement{Table: table}
	plan.Assignments, err = parser.parseAssignments()
	if err != nil {
		return nil, err
	}
	if parser.cursor.AcceptKeyword("where") {
		plan.Predicate, err = parser.parsePredicate()
		if err != nil {
			return nil, err
		}
		plan.Conditions = plannerConditions(*plan.Predicate)
	}
	if parser.cursor.AcceptKeyword("returning") {
		plan.Returning, err = parser.projections(false)
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (parser *statementParser) parseAssignments() ([]Assignment, error) {
	var assignments []Assignment
	seen := make(map[string]struct{})
	for {
		column, err := parser.columnReference()
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(column)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("kitdb SQL: UPDATE assigns field %q more than once", column)
		}
		seen[key] = struct{}{}
		if err := parser.cursor.ExpectSymbol("="); err != nil {
			return nil, err
		}
		assignment := Assignment{Column: column, Default: parser.cursor.AcceptKeyword("default")}
		if !assignment.Default {
			expression, err := parser.parseExpression()
			if err != nil {
				return nil, err
			}
			assignment.Expression = expression
			if expression.Kind == "literal" {
				assignment.Value = expression.Literal
			}
		}
		assignments = append(assignments, assignment)
		if !parser.cursor.AcceptSymbol(",") {
			break
		}
	}
	return assignments, nil
}

func (parser *statementParser) parseConflict() (*ConflictClause, error) {
	plan := &ConflictClause{}
	var err error
	if parser.cursor.AcceptSymbol("(") {
		plan.Columns, err = parser.identifierListAfterOpen()
		if err != nil {
			return nil, err
		}
		if len(plan.Columns) == 0 || duplicateIdentifier(plan.Columns) != "" {
			return nil, fmt.Errorf("kitdb SQL: invalid ON CONFLICT columns")
		}
	}
	if err := parser.cursor.ExpectKeyword("do"); err != nil {
		return nil, err
	}
	if parser.cursor.AcceptKeyword("nothing") {
		plan.Nothing = true
		return plan, nil
	}
	if len(plan.Columns) == 0 {
		return nil, fmt.Errorf("kitdb SQL: ON CONFLICT DO UPDATE requires columns")
	}
	if err := parser.cursor.ExpectKeyword("update"); err != nil {
		return nil, err
	}
	if err := parser.cursor.ExpectKeyword("set"); err != nil {
		return nil, err
	}
	plan.Assignments, err = parser.parseAssignments()
	if err != nil {
		return nil, err
	}
	if parser.cursor.AcceptKeyword("where") {
		plan.Predicate, err = parser.parsePredicate()
	}
	return plan, err
}

func (parser *statementParser) parseDelete() (*DeleteStatement, error) {
	if err := parser.cursor.ExpectKeyword("from"); err != nil {
		return nil, err
	}
	table, err := parser.qualifiedIdentifier()
	if err != nil {
		return nil, err
	}
	plan := &DeleteStatement{Table: table}
	if parser.cursor.AcceptKeyword("where") {
		plan.Predicate, err = parser.parsePredicate()
		if err != nil {
			return nil, err
		}
		plan.Conditions = plannerConditions(*plan.Predicate)
	}
	if parser.cursor.AcceptKeyword("returning") {
		plan.Returning, err = parser.projections(false)
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (parser *statementParser) parseConditions() ([]Condition, error) {
	conditions := make([]Condition, 0, 2)
	for {
		column, err := parser.columnReference()
		if err != nil {
			return nil, err
		}
		condition := Condition{Column: column}
		if parser.cursor.AcceptKeyword("is") {
			condition.Operator = "is"
			if parser.cursor.AcceptKeyword("not") {
				condition.Operator = "is not"
			}
			if err := parser.cursor.ExpectKeyword("null"); err != nil {
				return nil, err
			}
			condition.Value = Literal{Kind: LiteralNull}
		} else {
			operator := parser.cursor.Take()
			if operator.Kind != TokenSymbol || !supportedComparison(operator.Text) {
				return nil, fmt.Errorf("kitdb SQL: unsupported WHERE operator %q", operator.Text)
			}
			condition.Operator = operator.Text
			condition.Value, err = parser.literal(true)
			if err != nil {
				return nil, err
			}
		}
		conditions = append(conditions, condition)
		if !parser.cursor.AcceptKeyword("and") {
			break
		}
	}
	return conditions, nil
}

func (parser *statementParser) parsePredicate() (*CheckPlan, error) {
	return parser.parseExpression()
}

func (parser *statementParser) parseExpression() (*CheckPlan, error) {
	expression, err := parser.parsePredicateOr()
	if err != nil {
		return nil, err
	}
	if err := validateExpressionPlan(expression); err != nil {
		return nil, err
	}
	return &expression, nil
}

func (parser *statementParser) parsePredicateOr() (CheckPlan, error) {
	left, err := parser.parsePredicateAnd()
	if err != nil {
		return CheckPlan{}, err
	}
	for parser.cursor.AcceptKeyword("or") {
		right, err := parser.parsePredicateAnd()
		if err != nil {
			return CheckPlan{}, err
		}
		left = CheckPlan{Kind: "binary", Operator: "or", Arguments: []CheckPlan{left, right}}
	}
	return left, nil
}

func (parser *statementParser) parsePredicateAnd() (CheckPlan, error) {
	left, err := parser.parsePredicateNot()
	if err != nil {
		return CheckPlan{}, err
	}
	for parser.cursor.AcceptKeyword("and") {
		right, err := parser.parsePredicateNot()
		if err != nil {
			return CheckPlan{}, err
		}
		left = CheckPlan{Kind: "binary", Operator: "and", Arguments: []CheckPlan{left, right}}
	}
	return left, nil
}

func (parser *statementParser) parsePredicateNot() (CheckPlan, error) {
	if parser.cursor.AcceptKeyword("not") {
		child, err := parser.parseNestedExpression(parser.parsePredicateNot)
		if err != nil {
			return CheckPlan{}, err
		}
		return CheckPlan{Kind: "unary", Operator: "not", Arguments: []CheckPlan{child}}, nil
	}
	return parser.parsePredicateComparison()
}

func (parser *statementParser) parsePredicateComparison() (CheckPlan, error) {
	if search, found, err := parser.parseLeadingSearchExpression(); found || err != nil {
		return search, err
	}
	left, err := parser.parseExpressionConcatenation()
	if err != nil {
		return CheckPlan{}, err
	}
	if parser.cursor.AcceptKeyword("search") {
		if left.Kind != "field" {
			return CheckPlan{}, fmt.Errorf("kitdb SQL: SEARCH left operand must be a field, field tuple, or *")
		}
		query, err := parser.searchLiteral("SEARCH query")
		if err != nil {
			return CheckPlan{}, err
		}
		return searchExpression([]string{left.Field}, query), nil
	}
	if parser.cursor.AcceptKeyword("is") {
		operator := "is null"
		if parser.cursor.AcceptKeyword("not") {
			operator = "is not null"
		}
		if err := parser.cursor.ExpectKeyword("null"); err != nil {
			return CheckPlan{}, err
		}
		return CheckPlan{Kind: "unary", Operator: operator, Arguments: []CheckPlan{left}}, nil
	}
	negated := parser.cursor.AcceptKeyword("not")
	if parser.cursor.AcceptKeyword("between") {
		lower, err := parser.parseExpressionConcatenation()
		if err != nil {
			return CheckPlan{}, err
		}
		if err := parser.cursor.ExpectKeyword("and"); err != nil {
			return CheckPlan{}, err
		}
		upper, err := parser.parseExpressionConcatenation()
		if err != nil {
			return CheckPlan{}, err
		}
		operator := "between"
		if negated {
			operator = "not between"
		}
		return CheckPlan{Kind: "function", Operator: operator, Arguments: []CheckPlan{left, lower, upper}}, nil
	}
	likeOperator := ""
	if parser.cursor.AcceptKeyword("like") {
		likeOperator = "like"
	} else if parser.cursor.AcceptKeyword("ilike") {
		likeOperator = "ilike"
	}
	if likeOperator != "" {
		operator := likeOperator
		if negated {
			operator = "not " + operator
		}
		right, err := parser.parseExpressionConcatenation()
		if err != nil {
			return CheckPlan{}, err
		}
		return CheckPlan{Kind: "binary", Operator: operator, Arguments: []CheckPlan{left, right}}, nil
	}
	if parser.cursor.AcceptKeyword("in") {
		if err := parser.cursor.ExpectSymbol("("); err != nil {
			return CheckPlan{}, err
		}
		arguments := []CheckPlan{left}
		for {
			if len(arguments) > 1_000 {
				return CheckPlan{}, fmt.Errorf("kitdb SQL: IN exceeds 1000 values")
			}
			item, err := parser.parseExpressionConcatenation()
			if err != nil {
				return CheckPlan{}, err
			}
			arguments = append(arguments, item)
			if parser.cursor.AcceptSymbol(")") {
				break
			}
			if err := parser.cursor.ExpectSymbol(","); err != nil {
				return CheckPlan{}, err
			}
		}
		operator := "in"
		if negated {
			operator = "not in"
		}
		return CheckPlan{Kind: "function", Operator: operator, Arguments: arguments}, nil
	}
	if negated {
		return CheckPlan{}, fmt.Errorf("kitdb SQL: NOT after a value expects BETWEEN, LIKE, ILIKE, or IN")
	}
	operator := parser.cursor.Peek()
	if operator.Kind != TokenSymbol || !supportedComparison(operator.Text) {
		return left, nil
	}
	parser.cursor.Take()
	right, err := parser.parseExpressionConcatenation()
	if err != nil {
		return CheckPlan{}, err
	}
	return CheckPlan{Kind: "binary", Operator: operator.Text, Arguments: []CheckPlan{left, right}}, nil
}

func (parser *statementParser) parseLeadingSearchExpression() (CheckPlan, bool, error) {
	start := parser.cursor.Position()
	if parser.cursor.AcceptKeyword("search") {
		query, err := parser.searchLiteral("SEARCH query")
		return searchExpression(nil, query), true, err
	}
	if parser.cursor.AcceptSymbol("*") {
		if !parser.cursor.AcceptKeyword("search") {
			parser.cursor.Restore(start)
			return CheckPlan{}, false, nil
		}
		query, err := parser.searchLiteral("SEARCH query")
		return searchExpression(nil, query), true, err
	}
	if !parser.cursor.AcceptSymbol("(") {
		return CheckPlan{}, false, nil
	}
	first, err := parser.columnReference()
	if err != nil || !parser.cursor.AcceptSymbol(",") {
		parser.cursor.Restore(start)
		return CheckPlan{}, false, nil
	}
	fields := []string{first}
	for {
		field, err := parser.columnReference()
		if err != nil {
			return CheckPlan{}, true, err
		}
		fields = append(fields, field)
		if len(fields) > 32 {
			return CheckPlan{}, true, fmt.Errorf("kitdb SQL: SEARCH supports at most 32 fields")
		}
		if parser.cursor.AcceptSymbol(")") {
			break
		}
		if err := parser.cursor.ExpectSymbol(","); err != nil {
			return CheckPlan{}, true, err
		}
	}
	if !parser.cursor.AcceptKeyword("search") {
		parser.cursor.Restore(start)
		return CheckPlan{}, false, nil
	}
	query, err := parser.searchLiteral("SEARCH query")
	return searchExpression(fields, query), true, err
}

func (parser *statementParser) searchLiteral(label string) (Literal, error) {
	literal, err := parser.literal(true)
	if err != nil {
		return Literal{}, err
	}
	switch literal.Kind {
	case LiteralString, LiteralParameter, LiteralNull:
		return literal, nil
	default:
		return Literal{}, fmt.Errorf("kitdb SQL: %s must be text, NULL, or a bound parameter", label)
	}
}

func searchExpression(fields []string, query Literal) CheckPlan {
	arguments := make([]CheckPlan, 0, len(fields)+1)
	for _, field := range fields {
		arguments = append(arguments, CheckPlan{Kind: "field", Field: field})
	}
	arguments = append(arguments, CheckPlan{Kind: "literal", Literal: query})
	return CheckPlan{Kind: "search", Operator: "search", Arguments: arguments}
}

func extractSearchPredicate(expression *CheckPlan) (*SearchPredicate, *CheckPlan, error) {
	if expression == nil {
		return nil, nil, nil
	}
	if expression.Kind == "search" {
		search, err := decodeSearchPredicate(*expression)
		return search, nil, err
	}
	if expression.Kind == "binary" && expression.Operator == "and" && len(expression.Arguments) == 2 {
		leftSearch, leftResidual, err := extractSearchPredicate(&expression.Arguments[0])
		if err != nil {
			return nil, nil, err
		}
		rightSearch, rightResidual, err := extractSearchPredicate(&expression.Arguments[1])
		if err != nil {
			return nil, nil, err
		}
		if leftSearch != nil && rightSearch != nil {
			return nil, nil, fmt.Errorf("kitdb SQL: SELECT accepts one SEARCH predicate")
		}
		search := leftSearch
		if search == nil {
			search = rightSearch
		}
		return search, joinResidualAnd(leftResidual, rightResidual), nil
	}
	if expressionContainsSearch(*expression) {
		return nil, nil, fmt.Errorf("kitdb SQL: SEARCH may be combined with row filters only through AND")
	}
	copy := *expression
	return nil, &copy, nil
}

func decodeSearchPredicate(expression CheckPlan) (*SearchPredicate, error) {
	if expression.Kind != "search" || len(expression.Arguments) == 0 {
		return nil, fmt.Errorf("kitdb SQL: SEARCH needs a query")
	}
	query := expression.Arguments[len(expression.Arguments)-1]
	if query.Kind != "literal" {
		return nil, fmt.Errorf("kitdb SQL: SEARCH query must be a literal or bound parameter")
	}
	result := &SearchPredicate{Query: query.Literal}
	seen := make(map[string]struct{}, len(expression.Arguments)-1)
	for _, argument := range expression.Arguments[:len(expression.Arguments)-1] {
		if argument.Kind != "field" || argument.Field == "" {
			return nil, fmt.Errorf("kitdb SQL: SEARCH fields must be column references")
		}
		key := strings.ToLower(argument.Field)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("kitdb SQL: SEARCH repeats field %q", argument.Field)
		}
		seen[key] = struct{}{}
		result.Fields = append(result.Fields, argument.Field)
	}
	return result, nil
}

func joinResidualAnd(left, right *CheckPlan) *CheckPlan {
	switch {
	case left == nil:
		return right
	case right == nil:
		return left
	default:
		return &CheckPlan{Kind: "binary", Operator: "and", Arguments: []CheckPlan{*left, *right}}
	}
}

func expressionContainsSearch(expression CheckPlan) bool {
	if expression.Kind == "search" {
		return true
	}
	for _, argument := range expression.Arguments {
		if expressionContainsSearch(argument) {
			return true
		}
	}
	return false
}

func (parser *statementParser) parseExpressionConcatenation() (CheckPlan, error) {
	left, err := parser.parseExpressionAdditive()
	if err != nil {
		return CheckPlan{}, err
	}
	for parser.cursor.AcceptSymbol("||") {
		right, err := parser.parseExpressionAdditive()
		if err != nil {
			return CheckPlan{}, err
		}
		left = CheckPlan{Kind: "binary", Operator: "||", Arguments: []CheckPlan{left, right}}
	}
	return left, nil
}

func (parser *statementParser) parseExpressionAdditive() (CheckPlan, error) {
	left, err := parser.parseExpressionMultiplicative()
	if err != nil {
		return CheckPlan{}, err
	}
	for {
		operator := ""
		switch {
		case parser.cursor.AcceptSymbol("+"):
			operator = "+"
		case parser.cursor.AcceptSymbol("-"):
			operator = "-"
		default:
			return left, nil
		}
		right, err := parser.parseExpressionMultiplicative()
		if err != nil {
			return CheckPlan{}, err
		}
		left = CheckPlan{Kind: "binary", Operator: operator, Arguments: []CheckPlan{left, right}}
	}
}

func (parser *statementParser) parseExpressionMultiplicative() (CheckPlan, error) {
	left, err := parser.parseExpressionUnary()
	if err != nil {
		return CheckPlan{}, err
	}
	for {
		operator := ""
		switch {
		case parser.cursor.AcceptSymbol("*"):
			operator = "*"
		case parser.cursor.AcceptSymbol("/"):
			operator = "/"
		case parser.cursor.AcceptSymbol("%"):
			operator = "%"
		default:
			return left, nil
		}
		right, err := parser.parseExpressionUnary()
		if err != nil {
			return CheckPlan{}, err
		}
		left = CheckPlan{Kind: "binary", Operator: operator, Arguments: []CheckPlan{left, right}}
	}
}

func (parser *statementParser) parseExpressionUnary() (CheckPlan, error) {
	operator := ""
	switch {
	case parser.cursor.AcceptSymbol("+"):
		operator = "+"
	case parser.cursor.AcceptSymbol("-"):
		operator = "-"
	}
	if operator == "" {
		return parser.parsePredicatePrimary()
	}
	child, err := parser.parseNestedExpression(parser.parseExpressionUnary)
	if err != nil {
		return CheckPlan{}, err
	}
	if child.Kind == "literal" && child.Literal.Kind == LiteralNumber {
		if operator == "-" {
			child.Literal.Text = "-" + child.Literal.Text
		}
		return child, nil
	}
	return CheckPlan{Kind: "unary", Operator: operator, Arguments: []CheckPlan{child}}, nil
}

func (parser *statementParser) parsePredicatePrimary() (CheckPlan, error) {
	if parser.cursor.AcceptSymbol("(") {
		expression, err := parser.parseNestedExpression(parser.parsePredicateOr)
		if err != nil {
			return CheckPlan{}, err
		}
		if err := parser.cursor.ExpectSymbol(")"); err != nil {
			return CheckPlan{}, err
		}
		return expression, nil
	}
	if expression, found, err := parser.parseTypedLiteral(); found || err != nil {
		return expression, err
	}
	token := parser.cursor.Peek()
	literal := token.Kind == TokenString || token.Kind == TokenNumber || token.Kind == TokenPlaceholder
	if token.Kind == TokenIdentifier {
		switch strings.ToLower(token.Text) {
		case "null", "true", "false", "current_timestamp", "current_date", "current_time", "localtimestamp":
			literal = true
		}
	}
	if literal {
		item, err := parser.literal(true)
		if err != nil {
			return CheckPlan{}, err
		}
		return CheckPlan{Kind: "literal", Literal: item}, nil
	}
	if token.Kind == TokenIdentifier {
		start := parser.cursor.Position()
		name, err := parser.identifier()
		if err != nil {
			return CheckPlan{}, err
		}
		if parser.cursor.AcceptSymbol("(") {
			return parser.parseExpressionFunction(name)
		}
		parser.cursor.Restore(start)
	}
	field, err := parser.columnReference()
	if err != nil {
		return CheckPlan{}, err
	}
	return CheckPlan{Kind: "field", Field: field}, nil
}

func (parser *statementParser) parseTypedLiteral() (CheckPlan, bool, error) {
	start := parser.cursor.Position()
	token := parser.cursor.Peek()
	if token.Kind != TokenIdentifier {
		return CheckPlan{}, false, nil
	}
	kind := ""
	switch strings.ToLower(token.Text) {
	case "date":
		kind = "date"
	case "time":
		kind = "time"
	case "timestamp":
		kind = "timestamp"
	case "timestamptz":
		kind = "timestamptz"
	case "interval":
		kind = "interval"
	case "uuid":
		kind = "uuid"
	default:
		return CheckPlan{}, false, nil
	}
	parser.cursor.Take()
	if kind == "timestamp" && parser.cursor.AcceptKeyword("with") {
		if err := parser.cursor.ExpectKeyword("time"); err != nil {
			parser.cursor.Restore(start)
			return CheckPlan{}, true, err
		}
		if err := parser.cursor.ExpectKeyword("zone"); err != nil {
			parser.cursor.Restore(start)
			return CheckPlan{}, true, err
		}
		kind = "timestamptz"
	}
	value := parser.cursor.Take()
	if value.Kind != TokenString {
		parser.cursor.Restore(start)
		return CheckPlan{}, false, nil
	}
	return CheckPlan{
		Kind: "cast", CastKind: kind,
		Arguments: []CheckPlan{{Kind: "literal", Literal: Literal{Kind: LiteralString, Text: value.Text}}},
	}, true, nil
}

func (parser *statementParser) parseExpressionFunction(name string) (CheckPlan, error) {
	if strings.EqualFold(name, "cast") {
		return parser.parseCastExpression()
	}
	arguments := make([]CheckPlan, 0, 2)
	if parser.cursor.AcceptSymbol(")") {
		return CheckPlan{Kind: "function", Operator: strings.ToLower(name)}, nil
	}
	for {
		argument, err := parser.parseNestedExpression(parser.parsePredicateOr)
		if err != nil {
			return CheckPlan{}, err
		}
		arguments = append(arguments, argument)
		if len(arguments) > 16 {
			return CheckPlan{}, fmt.Errorf("kitdb SQL: %s exceeds 16 arguments", strings.ToUpper(name))
		}
		if parser.cursor.AcceptSymbol(")") {
			break
		}
		if err := parser.cursor.ExpectSymbol(","); err != nil {
			return CheckPlan{}, err
		}
	}
	return CheckPlan{Kind: "function", Operator: strings.ToLower(name), Arguments: arguments}, nil
}

func (parser *statementParser) parseCastExpression() (CheckPlan, error) {
	argument, err := parser.parseNestedExpression(parser.parsePredicateOr)
	if err != nil {
		return CheckPlan{}, err
	}
	if err := parser.cursor.ExpectKeyword("as"); err != nil {
		return CheckPlan{}, fmt.Errorf("kitdb SQL: CAST expects AS: %w", err)
	}
	typeInfo, choices, modifiers, err := parser.columnType()
	if err != nil {
		return CheckPlan{}, err
	}
	if len(choices) != 0 || typeInfo.Family == FamilyChoice || typeInfo.Family == FamilyArray ||
		typeInfo.Family == FamilyVector {
		return CheckPlan{}, fmt.Errorf("kitdb SQL: CAST target %s is not supported", strings.ToUpper(typeInfo.Kind))
	}
	if err := parser.cursor.ExpectSymbol(")"); err != nil {
		return CheckPlan{}, err
	}
	return CheckPlan{
		Kind: "cast", Arguments: []CheckPlan{argument}, CastKind: typeInfo.Kind,
		Precision: modifiers.Precision, Scale: modifiers.Scale,
		TimePrecision: modifiers.TimePrecision, TextLength: modifiers.TextLength,
	}, nil
}

func (parser *statementParser) parseNestedExpression(
	parse func() (CheckPlan, error),
) (CheckPlan, error) {
	parser.expressionDepth++
	if parser.expressionDepth > maximumExpressionDepth {
		parser.expressionDepth--
		return CheckPlan{}, fmt.Errorf("kitdb SQL: expression exceeds depth %d", maximumExpressionDepth)
	}
	expression, err := parse()
	parser.expressionDepth--
	return expression, err
}

func validateExpressionPlan(expression CheckPlan) error {
	type frame struct {
		node  CheckPlan
		depth int
	}
	stack := []frame{{node: expression, depth: 1}}
	nodes := 0
	for len(stack) != 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		nodes++
		if nodes > maximumExpressionNodes {
			return fmt.Errorf("kitdb SQL: expression exceeds %d nodes", maximumExpressionNodes)
		}
		if current.depth > maximumExpressionDepth {
			return fmt.Errorf("kitdb SQL: expression exceeds depth %d", maximumExpressionDepth)
		}
		for index := range current.node.Arguments {
			stack = append(stack, frame{node: current.node.Arguments[index], depth: current.depth + 1})
		}
	}
	return nil
}

func plannerConditions(expression CheckPlan) []Condition {
	if expression.Kind == "binary" && expression.Operator == "and" && len(expression.Arguments) == 2 {
		left := plannerConditions(expression.Arguments[0])
		right := plannerConditions(expression.Arguments[1])
		return append(left, right...)
	}
	conditions, ok := flattenPlannerConditions(expression)
	if !ok {
		return nil
	}
	return conditions
}

func flattenPlannerConditions(expression CheckPlan) ([]Condition, bool) {
	if expression.Kind == "binary" && expression.Operator == "and" && len(expression.Arguments) == 2 {
		left, leftOK := flattenPlannerConditions(expression.Arguments[0])
		right, rightOK := flattenPlannerConditions(expression.Arguments[1])
		if !leftOK || !rightOK {
			return nil, false
		}
		return append(left, right...), true
	}
	if expression.Kind == "binary" && supportedComparison(expression.Operator) && len(expression.Arguments) == 2 &&
		expression.Arguments[0].Kind == "field" {
		literal, ok := plannerLiteral(expression.Arguments[1])
		if !ok {
			return nil, false
		}
		return []Condition{{
			Column: expression.Arguments[0].Field, Operator: expression.Operator,
			Value: literal,
		}}, true
	}
	if expression.Kind == "unary" && (expression.Operator == "is null" || expression.Operator == "is not null") &&
		len(expression.Arguments) == 1 && expression.Arguments[0].Kind == "field" {
		operator := "is"
		if expression.Operator == "is not null" {
			operator = "is not"
		}
		return []Condition{{Column: expression.Arguments[0].Field, Operator: operator, Value: Literal{Kind: LiteralNull}}}, true
	}
	return nil, false
}

// Typed literals are still constants after parsing. Exposing their inner
// literal to the bounded planner lets field coercion build exact index bounds
// while the full predicate remains the authoritative residual check.
func plannerLiteral(expression CheckPlan) (Literal, bool) {
	if expression.Kind == "literal" {
		return expression.Literal, true
	}
	if expression.Kind != "cast" || len(expression.Arguments) != 1 || expression.Arguments[0].Kind != "literal" {
		return Literal{}, false
	}
	switch expression.CastKind {
	case "date", "time", "timestamp", "timestamptz", "interval", "uuid":
		return expression.Arguments[0].Literal, true
	default:
		return Literal{}, false
	}
}

func (parser *statementParser) projections(allowCount bool) ([]Projection, error) {
	projections := make([]Projection, 0, 4)
	for {
		projection := Projection{}
		switch {
		case parser.cursor.AcceptSymbol("*"):
			projection.All = true
		case allowCount && isAggregateKeyword(parser.cursor.Peek()):
			function := strings.ToLower(parser.cursor.Take().Text)
			if err := parser.cursor.ExpectSymbol("("); err != nil {
				return nil, err
			}
			if parser.cursor.AcceptSymbol("*") {
				if function != "count" {
					return nil, fmt.Errorf("kitdb SQL: %s(*) is invalid", strings.ToUpper(function))
				}
			} else {
				name, err := parser.columnReference()
				if err != nil {
					return nil, err
				}
				projection.Name = name
			}
			if err := parser.cursor.ExpectSymbol(")"); err != nil {
				return nil, err
			}
			projection.Aggregate = function
			projection.Count = function == "count"
		default:
			start := parser.cursor.Position()
			qualifier, qualifierErr := parser.identifier()
			if qualifierErr == nil && parser.cursor.AcceptSymbol(".") && parser.cursor.AcceptSymbol("*") {
				projection.All, projection.Qualifier = true, qualifier
				break
			}
			parser.cursor.Restore(start)
			expression, err := parser.parseExpression()
			if err != nil {
				return nil, err
			}
			projection.Expression = expression
			if expression.Kind == "field" {
				projection.Name, projection.Expression = expression.Field, nil
			}
		}
		if parser.cursor.AcceptKeyword("as") {
			alias, err := parser.identifier()
			if err != nil {
				return nil, err
			}
			projection.Alias = alias
		} else if token := parser.cursor.Peek(); token.Kind == TokenIdentifier && !selectClauseKeyword(token.Text) {
			alias, err := parser.identifier()
			if err != nil {
				return nil, err
			}
			projection.Alias = alias
		}
		projections = append(projections, projection)
		if !parser.cursor.AcceptSymbol(",") {
			break
		}
	}
	return projections, nil
}

func isAggregateKeyword(token Token) bool {
	if token.Kind != TokenIdentifier {
		return false
	}
	switch strings.ToLower(token.Text) {
	case "count", "sum", "avg", "min", "max":
		return true
	default:
		return false
	}
}

func (parser *statementParser) literal(allowParameter bool) (Literal, error) {
	negative := parser.cursor.AcceptSymbol("-")
	token := parser.cursor.Take()
	switch token.Kind {
	case TokenString:
		if negative {
			return Literal{}, fmt.Errorf("kitdb SQL: a string cannot have a unary minus")
		}
		return Literal{Kind: LiteralString, Text: token.Text}, nil
	case TokenNumber:
		text := token.Text
		if negative {
			text = "-" + text
		}
		return Literal{Kind: LiteralNumber, Text: text}, nil
	case TokenPlaceholder:
		if !allowParameter || negative {
			return Literal{}, fmt.Errorf("kitdb SQL: a bound parameter is not allowed here")
		}
		position := parser.nextParameter
		if token.Text != "?" {
			if !strings.HasPrefix(token.Text, "$") {
				return Literal{}, fmt.Errorf("kitdb SQL: standalone parameters use ? or $N")
			}
			parsed, err := strconv.Atoi(token.Text[1:])
			if err != nil || parsed < 1 {
				return Literal{}, fmt.Errorf("kitdb SQL: invalid parameter %q", token.Text)
			}
			position = parsed
		} else {
			parser.nextParameter++
		}
		return Literal{Kind: LiteralParameter, Parameter: position}, nil
	case TokenIdentifier:
		if negative {
			return Literal{}, fmt.Errorf("kitdb SQL: invalid negative literal %q", token.Text)
		}
		switch strings.ToLower(token.Text) {
		case "null":
			return Literal{Kind: LiteralNull}, nil
		case "true":
			return Literal{Kind: LiteralBoolean, Boolean: true}, nil
		case "false":
			return Literal{Kind: LiteralBoolean}, nil
		case "current_timestamp":
			return Literal{Kind: LiteralCurrentTimestamp}, nil
		case "current_date":
			return Literal{Kind: LiteralCurrentDate}, nil
		case "current_time":
			return Literal{Kind: LiteralCurrentTime}, nil
		case "localtimestamp":
			return Literal{Kind: LiteralLocalTimestamp}, nil
		}
	}
	return Literal{}, fmt.Errorf("kitdb SQL: expected a literal or bound parameter, got %q", token.Text)
}

func (parser *statementParser) currentTemporalLiteral() (Literal, bool) {
	switch {
	case parser.cursor.AcceptKeyword("current_timestamp"):
		return Literal{Kind: LiteralCurrentTimestamp}, true
	case parser.cursor.AcceptKeyword("current_date"):
		return Literal{Kind: LiteralCurrentDate}, true
	case parser.cursor.AcceptKeyword("current_time"):
		return Literal{Kind: LiteralCurrentTime}, true
	case parser.cursor.AcceptKeyword("localtimestamp"):
		return Literal{Kind: LiteralLocalTimestamp}, true
	default:
		return Literal{}, false
	}
}

func (parser *statementParser) identifier() (string, error) {
	token := parser.cursor.Take()
	if token.Kind != TokenIdentifier || token.Text == "" {
		return "", fmt.Errorf("kitdb SQL: expected identifier, got %q", token.Text)
	}
	return token.Text, nil
}

func (parser *statementParser) qualifiedIdentifier() (string, error) {
	name, err := parser.identifier()
	if err != nil {
		return "", err
	}
	for parser.cursor.AcceptSymbol(".") {
		name, err = parser.identifier()
		if err != nil {
			return "", err
		}
	}
	return name, nil
}

func (parser *statementParser) columnReference() (string, error) {
	parts := make([]string, 0, 2)
	name, err := parser.identifier()
	if err != nil {
		return "", err
	}
	parts = append(parts, name)
	for parser.cursor.AcceptSymbol(".") {
		name, err = parser.identifier()
		if err != nil {
			return "", err
		}
		parts = append(parts, name)
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return parts[len(parts)-2] + "." + parts[len(parts)-1], nil
}

func (parser *statementParser) optionalAlias() (string, error) {
	if parser.cursor.AcceptKeyword("as") {
		return parser.identifier()
	}
	token := parser.cursor.Peek()
	if token.Kind != TokenIdentifier || selectClauseKeyword(token.Text) {
		return "", nil
	}
	return parser.identifier()
}

func (parser *statementParser) parseJoins() ([]Join, error) {
	joins := make([]Join, 0, 1)
	for {
		kind := ""
		switch {
		case parser.cursor.AcceptKeyword("join"):
			kind = "inner"
		case parser.cursor.AcceptKeyword("inner"):
			if err := parser.cursor.ExpectKeyword("join"); err != nil {
				return nil, err
			}
			kind = "inner"
		case parser.cursor.AcceptKeyword("left"):
			_ = parser.cursor.AcceptKeyword("outer")
			if err := parser.cursor.ExpectKeyword("join"); err != nil {
				return nil, err
			}
			kind = "left"
		default:
			return joins, nil
		}
		if len(joins) >= MaximumSelectJoins {
			return nil, fmt.Errorf("kitdb SQL: SELECT exceeds %d JOIN clauses", MaximumSelectJoins)
		}
		table, err := parser.qualifiedIdentifier()
		if err != nil {
			return nil, err
		}
		alias, err := parser.optionalAlias()
		if err != nil {
			return nil, err
		}
		if err := parser.cursor.ExpectKeyword("on"); err != nil {
			return nil, err
		}
		var equalities []JoinEquality
		if err := parser.parseJoinEqualities(0, &equalities); err != nil {
			return nil, err
		}
		joins = append(joins, Join{Kind: kind, Table: table, Alias: alias,
			Left: equalities[0].Left, Right: equalities[0].Right, And: equalities[1:]})
	}
}

func (parser *statementParser) parseJoinEqualities(depth int, equalities *[]JoinEquality) error {
	if depth > MaximumQueryNesting {
		return fmt.Errorf("kitdb SQL: JOIN ON exceeds %d nesting levels", MaximumQueryNesting)
	}
	for {
		if parser.cursor.AcceptSymbol("(") {
			if err := parser.parseJoinEqualities(depth+1, equalities); err != nil {
				return err
			}
			if err := parser.cursor.ExpectSymbol(")"); err != nil {
				return err
			}
		} else {
			if len(*equalities) >= MaximumJoinEqualities {
				return fmt.Errorf("kitdb SQL: JOIN ON exceeds %d equalities", MaximumJoinEqualities)
			}
			left, err := parser.columnReference()
			if err != nil {
				return err
			}
			if err := parser.cursor.ExpectSymbol("="); err != nil {
				return fmt.Errorf("kitdb SQL: JOIN requires column equalities joined by AND: %w", err)
			}
			right, err := parser.columnReference()
			if err != nil {
				return err
			}
			*equalities = append(*equalities, JoinEquality{Left: left, Right: right})
		}
		if !parser.cursor.AcceptKeyword("and") {
			return nil
		}
	}
}

func selectClauseKeyword(word string) bool {
	switch strings.ToLower(word) {
	case "from", "join", "inner", "left", "right", "full", "cross", "where", "group", "having", "order", "limit", "offset", "union", "for", "on", "with", "returning":
		return true
	default:
		return false
	}
}

func (parser *statementParser) identifierList() ([]string, error) {
	if err := parser.cursor.ExpectSymbol("("); err != nil {
		return nil, err
	}
	return parser.identifierListAfterOpen()
}

func (parser *statementParser) identifierListAfterOpen() ([]string, error) {
	items := make([]string, 0, 2)
	for {
		name, err := parser.identifier()
		if err != nil {
			return nil, err
		}
		items = append(items, name)
		if parser.cursor.AcceptSymbol(")") {
			break
		}
		if err := parser.cursor.ExpectSymbol(","); err != nil {
			return nil, err
		}
	}
	if duplicate := duplicateIdentifier(items); duplicate != "" {
		return nil, fmt.Errorf("kitdb SQL: field list repeats %q", duplicate)
	}
	return items, nil
}

func duplicateIdentifier(items []string) string {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := strings.ToLower(item)
		if _, duplicate := seen[key]; duplicate {
			return item
		}
		seen[key] = struct{}{}
	}
	return ""
}

func (parser *statementParser) nonNegativeInteger(label string) (int, error) {
	token := parser.cursor.Take()
	if token.Kind != TokenNumber || strings.Contains(token.Text, ".") {
		return 0, fmt.Errorf("kitdb SQL: %s expects a non-negative integer", label)
	}
	parsed, err := strconv.ParseUint(token.Text, 10, 31)
	if err != nil {
		return 0, fmt.Errorf("kitdb SQL: invalid %s %q", label, token.Text)
	}
	return int(parsed), nil
}

func supportedComparison(operator string) bool {
	switch operator {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		return true
	default:
		return false
	}
}
