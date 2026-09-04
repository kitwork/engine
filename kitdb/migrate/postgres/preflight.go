// Package postgres contains the PostgreSQL source adapter for KitDB migration.
// Source inspection is deliberately read-only and remains outside the KitDB
// transaction kernel.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const (
	ReportFormat     = "kitdb-postgres-preflight/v1"
	defaultSchema    = "public"
	defaultTimeout   = 30 * time.Second
	defaultMaxTables = 20
	maxListedTables  = 100
)

// Config bounds one PostgreSQL source inspection. URL is never copied into the
// report and is redacted from returned errors.
type Config struct {
	URL       string
	Schema    string
	Timeout   time.Duration
	MaxTables int
}

type Report struct {
	Format                string         `json:"format"`
	Ready                 bool           `json:"ready"`
	ReadOnlySession       bool           `json:"read_only_session"`
	LeastPrivilegeRole    bool           `json:"least_privilege_role"`
	CompleteReadableScope bool           `json:"complete_readable_scope"`
	Database              DatabaseReport `json:"database"`
	Role                  RoleReport     `json:"role"`
	Scope                 ScopeReport    `json:"scope"`
	Warnings              []string       `json:"warnings,omitempty"`
}

type DatabaseReport struct {
	Name             string              `json:"name"`
	ServerVersion    string              `json:"server_version"`
	ServerVersionNum int                 `json:"server_version_num"`
	Encoding         string              `json:"encoding"`
	TimeZone         string              `json:"time_zone"`
	InRecovery       bool                `json:"in_recovery"`
	Candidates       []DatabaseCandidate `json:"candidates,omitempty"`
}

type DatabaseCandidate struct {
	Name       string `json:"name"`
	Connect    bool   `json:"connect"`
	TotalBytes *int64 `json:"total_bytes,omitempty"`
}

type RoleReport struct {
	Name                string   `json:"name"`
	DefaultReadOnly     bool     `json:"default_read_only"`
	TransactionReadOnly bool     `json:"transaction_read_only"`
	Inherit             bool     `json:"inherit"`
	Superuser           bool     `json:"superuser"`
	CreateRole          bool     `json:"create_role"`
	CreateDatabase      bool     `json:"create_database"`
	Replication         bool     `json:"replication"`
	BypassRLS           bool     `json:"bypass_rls"`
	DatabaseCreate      bool     `json:"database_create"`
	DatabaseTemporary   bool     `json:"database_temporary"`
	InheritedRoles      []string `json:"inherited_roles,omitempty"`
}

type ScopeReport struct {
	Schema               string        `json:"schema"`
	Exists               bool          `json:"exists"`
	Usage                bool          `json:"usage"`
	Create               bool          `json:"create"`
	Tables               int64         `json:"tables"`
	ReadableTables       int64         `json:"readable_tables"`
	WritableTables       int64         `json:"writable_tables"`
	OwnedTables          int64         `json:"owned_tables"`
	Columns              int64         `json:"columns"`
	Sequences            int64         `json:"sequences"`
	ReadableSequences    int64         `json:"readable_sequences"`
	MutableSequences     int64         `json:"mutable_sequences"`
	EstimatedRows        int64         `json:"estimated_rows"`
	TotalBytes           int64         `json:"total_bytes"`
	RowSecurityTables    int64         `json:"row_security_tables"`
	ForcedSecurityTables int64         `json:"forced_row_security_tables"`
	LargestTables        []TableReport `json:"largest_tables,omitempty"`
}

type TableReport struct {
	Name              string `json:"name"`
	Partitioned       bool   `json:"partitioned"`
	Readable          bool   `json:"readable"`
	EstimatedRows     int64  `json:"estimated_rows"`
	TotalBytes        int64  `json:"total_bytes"`
	RowSecurity       bool   `json:"row_security"`
	ForcedRowSecurity bool   `json:"forced_row_security"`
}

// Preflight proves that the migration source can be inspected through one
// bounded, repeatable-read, read-only transaction. It reads catalog metadata;
// it never samples user rows or executes a write probe.
func Preflight(ctx context.Context, config Config) (Report, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return Report{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	database, err := sql.Open("postgres", config.URL)
	if err != nil {
		return Report{}, sourceError(config.URL, "initialize driver", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(0)
	database.SetConnMaxLifetime(config.Timeout)
	defer database.Close()

	if err := database.PingContext(ctx); err != nil {
		return Report{}, sourceError(config.URL, "connect", err)
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return Report{}, sourceError(config.URL, "begin read-only snapshot", err)
	}
	defer tx.Rollback()

	statementTimeout := strconv.FormatInt(config.Timeout.Milliseconds(), 10) + "ms"
	if _, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true)`, statementTimeout); err != nil {
		return Report{}, sourceError(config.URL, "bound statement timeout", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('application_name', 'kitdbmigrate-preflight', true)`); err != nil {
		return Report{}, sourceError(config.URL, "identify source session", err)
	}

	report := Report{Format: ReportFormat}
	if err := inspectIdentity(ctx, tx, &report); err != nil {
		return Report{}, sourceError(config.URL, "inspect identity", err)
	}
	if err := inspectDatabases(ctx, tx, &report.Database); err != nil {
		return Report{}, sourceError(config.URL, "inspect databases", err)
	}
	if err := inspectInheritedRoles(ctx, tx, &report.Role); err != nil {
		return Report{}, sourceError(config.URL, "inspect inherited roles", err)
	}
	if err := inspectScope(ctx, tx, config, &report.Scope); err != nil {
		return Report{}, sourceError(config.URL, "inspect schema", err)
	}

	evaluateReport(&report)
	return report, nil
}

func inspectDatabases(ctx context.Context, tx *sql.Tx, database *DatabaseReport) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			d.datname::text,
			has_database_privilege(current_user, d.oid, 'CONNECT'),
			CASE
				WHEN has_database_privilege(current_user, d.oid, 'CONNECT')
				THEN pg_catalog.pg_database_size(d.oid)
				ELSE NULL
			END
		FROM pg_catalog.pg_database AS d
		WHERE d.datallowconn AND NOT d.datistemplate
		ORDER BY d.datname
		LIMIT 100
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			candidate  DatabaseCandidate
			totalBytes sql.NullInt64
		)
		if err := rows.Scan(&candidate.Name, &candidate.Connect, &totalBytes); err != nil {
			return err
		}
		if totalBytes.Valid {
			candidate.TotalBytes = &totalBytes.Int64
		}
		database.Candidates = append(database.Candidates, candidate)
	}
	return rows.Err()
}

func normalizeConfig(config Config) (Config, error) {
	config.URL = strings.TrimSpace(config.URL)
	if config.URL == "" {
		return Config{}, fmt.Errorf("postgres preflight: source URL is required")
	}
	config.Schema = strings.TrimSpace(config.Schema)
	if config.Schema == "" {
		config.Schema = defaultSchema
	}
	if strings.IndexByte(config.Schema, 0) >= 0 {
		return Config{}, fmt.Errorf("postgres preflight: schema contains NUL")
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.Timeout < time.Second || config.Timeout > 5*time.Minute {
		return Config{}, fmt.Errorf("postgres preflight: timeout must be between 1s and 5m")
	}
	if config.MaxTables == 0 {
		config.MaxTables = defaultMaxTables
	}
	if config.MaxTables < 0 || config.MaxTables > maxListedTables {
		return Config{}, fmt.Errorf("postgres preflight: max tables must be between 0 and %d", maxListedTables)
	}
	return config, nil
}

func inspectIdentity(ctx context.Context, tx *sql.Tx, report *Report) error {
	var (
		versionNumberText string
		defaultReadOnly   string
		transactionRO     string
	)
	row := tx.QueryRowContext(ctx, `
		SELECT
			current_database()::text,
			current_user::text,
			current_setting('server_version'),
			current_setting('server_version_num'),
			current_setting('server_encoding'),
			current_setting('TimeZone'),
			current_setting('default_transaction_read_only'),
			current_setting('transaction_read_only'),
			pg_is_in_recovery(),
			r.rolinherit,
			r.rolsuper,
			r.rolcreaterole,
			r.rolcreatedb,
			r.rolreplication,
			r.rolbypassrls,
			has_database_privilege(current_user, current_database(), 'CREATE'),
			has_database_privilege(current_user, current_database(), 'TEMP')
		FROM pg_catalog.pg_roles AS r
		WHERE r.rolname = current_user
	`)
	if err := row.Scan(
		&report.Database.Name,
		&report.Role.Name,
		&report.Database.ServerVersion,
		&versionNumberText,
		&report.Database.Encoding,
		&report.Database.TimeZone,
		&defaultReadOnly,
		&transactionRO,
		&report.Database.InRecovery,
		&report.Role.Inherit,
		&report.Role.Superuser,
		&report.Role.CreateRole,
		&report.Role.CreateDatabase,
		&report.Role.Replication,
		&report.Role.BypassRLS,
		&report.Role.DatabaseCreate,
		&report.Role.DatabaseTemporary,
	); err != nil {
		return err
	}
	versionNumber, err := strconv.Atoi(versionNumberText)
	if err != nil {
		return fmt.Errorf("invalid server_version_num: %w", err)
	}
	report.Database.ServerVersionNum = versionNumber
	report.Role.DefaultReadOnly = settingEnabled(defaultReadOnly)
	report.Role.TransactionReadOnly = settingEnabled(transactionRO)
	return nil
}

func inspectInheritedRoles(ctx context.Context, tx *sql.Tx, role *RoleReport) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT inherited.rolname::text
		FROM pg_catalog.pg_auth_members AS membership
		JOIN pg_catalog.pg_roles AS member ON member.oid = membership.member
		JOIN pg_catalog.pg_roles AS inherited ON inherited.oid = membership.roleid
		WHERE member.rolname = current_user
		ORDER BY inherited.rolname
		LIMIT 100
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var inherited string
		if err := rows.Scan(&inherited); err != nil {
			return err
		}
		role.InheritedRoles = append(role.InheritedRoles, inherited)
	}
	return rows.Err()
}

func inspectScope(ctx context.Context, tx *sql.Tx, config Config, scope *ScopeReport) error {
	scope.Schema = config.Schema
	if err := tx.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = $1),
			COALESCE(has_schema_privilege(current_user, $1, 'USAGE'), false),
			COALESCE(has_schema_privilege(current_user, $1, 'CREATE'), false)
	`, config.Schema).Scan(&scope.Exists, &scope.Usage, &scope.Create); err != nil {
		return err
	}
	if !scope.Exists {
		return nil
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE has_table_privilege(c.oid, 'SELECT')),
			count(*) FILTER (WHERE
				has_table_privilege(c.oid, 'INSERT') OR
				has_table_privilege(c.oid, 'UPDATE') OR
				has_table_privilege(c.oid, 'DELETE') OR
				has_table_privilege(c.oid, 'TRUNCATE') OR
				has_table_privilege(c.oid, 'REFERENCES') OR
				has_table_privilege(c.oid, 'TRIGGER')
			),
			count(*) FILTER (WHERE c.relowner = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user)),
			COALESCE(sum(GREATEST(c.reltuples, 0)::bigint), 0),
			COALESCE(sum(pg_catalog.pg_total_relation_size(c.oid)), 0),
			count(*) FILTER (WHERE c.relrowsecurity),
			count(*) FILTER (WHERE c.relforcerowsecurity)
		FROM pg_catalog.pg_class AS c
		JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p')
	`, config.Schema).Scan(
		&scope.Tables,
		&scope.ReadableTables,
		&scope.WritableTables,
		&scope.OwnedTables,
		&scope.EstimatedRows,
		&scope.TotalBytes,
		&scope.RowSecurityTables,
		&scope.ForcedSecurityTables,
	); err != nil {
		return err
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT count(*)
		FROM pg_catalog.pg_attribute AS a
		JOIN pg_catalog.pg_class AS c ON c.oid = a.attrelid
		JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = $1
			AND c.relkind IN ('r', 'p')
			AND a.attnum > 0
			AND NOT a.attisdropped
	`, config.Schema).Scan(&scope.Columns); err != nil {
		return err
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE has_sequence_privilege(c.oid, 'SELECT')),
			count(*) FILTER (WHERE
				has_sequence_privilege(c.oid, 'UPDATE') OR
				has_sequence_privilege(c.oid, 'USAGE')
			)
		FROM pg_catalog.pg_class AS c
		JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind = 'S'
	`, config.Schema).Scan(
		&scope.Sequences,
		&scope.ReadableSequences,
		&scope.MutableSequences,
	); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT
			c.relname::text,
			c.relkind = 'p',
			has_table_privilege(c.oid, 'SELECT'),
			GREATEST(c.reltuples, 0)::bigint,
			pg_catalog.pg_total_relation_size(c.oid),
			c.relrowsecurity,
			c.relforcerowsecurity
		FROM pg_catalog.pg_class AS c
		JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p')
		ORDER BY pg_catalog.pg_total_relation_size(c.oid) DESC, c.relname
		LIMIT $2
	`, config.Schema, config.MaxTables)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var table TableReport
		if err := rows.Scan(
			&table.Name,
			&table.Partitioned,
			&table.Readable,
			&table.EstimatedRows,
			&table.TotalBytes,
			&table.RowSecurity,
			&table.ForcedRowSecurity,
		); err != nil {
			return err
		}
		scope.LargestTables = append(scope.LargestTables, table)
	}
	return rows.Err()
}

func evaluateReport(report *Report) {
	report.ReadOnlySession = report.Role.DefaultReadOnly && report.Role.TransactionReadOnly
	report.LeastPrivilegeRole = !report.Role.Superuser &&
		!report.Role.CreateRole &&
		!report.Role.CreateDatabase &&
		!report.Role.Replication &&
		!report.Role.BypassRLS &&
		!report.Role.DatabaseCreate &&
		!report.Scope.Create &&
		report.Scope.WritableTables == 0 &&
		report.Scope.OwnedTables == 0 &&
		report.Scope.MutableSequences == 0
	report.CompleteReadableScope = report.Scope.Exists &&
		report.Scope.Usage &&
		report.Scope.ReadableTables == report.Scope.Tables &&
		report.Scope.ReadableSequences == report.Scope.Sequences
	report.Ready = report.ReadOnlySession &&
		report.LeastPrivilegeRole &&
		report.CompleteReadableScope &&
		report.Scope.Tables > 0 &&
		report.Scope.RowSecurityTables == 0

	if !report.Role.DefaultReadOnly {
		report.Warnings = append(report.Warnings, "role default_transaction_read_only is not enabled")
	}
	if report.Role.Superuser || report.Role.CreateRole || report.Role.CreateDatabase || report.Role.Replication || report.Role.BypassRLS {
		report.Warnings = append(report.Warnings, "source role has elevated cluster privileges")
	}
	if len(report.Role.InheritedRoles) > 0 {
		report.Warnings = append(report.Warnings, "source role inherits one or more roles; effective table privileges were checked")
	}
	if report.Role.DatabaseCreate || report.Scope.Create {
		report.Warnings = append(report.Warnings, "source role can create persistent database objects")
	}
	if report.Role.DatabaseTemporary {
		report.Warnings = append(report.Warnings, "source role can create temporary objects through the database TEMP privilege")
	}
	if report.Scope.WritableTables > 0 || report.Scope.OwnedTables > 0 || report.Scope.MutableSequences > 0 {
		report.Warnings = append(report.Warnings, "source role has mutation authority over objects in the selected schema")
	}
	if !report.Scope.Exists {
		report.Warnings = append(report.Warnings, "selected schema does not exist")
	} else if !report.Scope.Usage {
		report.Warnings = append(report.Warnings, "source role lacks USAGE on the selected schema")
	}
	if report.Scope.ReadableTables != report.Scope.Tables {
		report.Warnings = append(report.Warnings, "source role cannot SELECT every table in the selected schema")
	}
	if report.Scope.ReadableSequences != report.Scope.Sequences {
		report.Warnings = append(report.Warnings, "source role cannot SELECT every sequence in the selected schema")
	}
	if report.Scope.RowSecurityTables > 0 {
		report.Warnings = append(report.Warnings, "row-level security is enabled; migration completeness requires policy review")
	}
	if report.Scope.Tables == 0 {
		report.Warnings = append(report.Warnings, "selected schema contains no ordinary or partitioned tables")
	}
}

func settingEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1":
		return true
	default:
		return false
	}
}

func sourceError(dsn, stage string, err error) error {
	message := err.Error()
	message = strings.ReplaceAll(message, dsn, "[redacted]")
	if parsed, parseErr := url.Parse(dsn); parseErr == nil && parsed.User != nil {
		if password, ok := parsed.User.Password(); ok && password != "" {
			message = strings.ReplaceAll(message, password, "[redacted]")
			message = strings.ReplaceAll(message, url.QueryEscape(password), "[redacted]")
		}
	}
	return fmt.Errorf("postgres preflight: %s: %s", stage, message)
}
