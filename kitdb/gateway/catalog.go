package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
	sqlite "modernc.org/sqlite"
)

// The catalog (KITWORK_KITDB.md question 13.5): what a database tool reads to show a database's schemas, tables,
// columns, keys and indexes is PostgreSQL's own catalog, pg_catalog and information_schema. A session keeps one of
// its own as TEMP tables of its SQLite connection, filled from the attached files: sqlite_schema, PRAGMA table_xinfo,
// index_list, index_xinfo and foreign_key_list, the kinds of Columns, sqlite_stat1. pg_catalog.pg_class is
// temp.pg_class and information_schema.tables is temp.information_schema_tables (translate.go writes those names; a
// bare pg_class finds the TEMP table first, as PostgreSQL finds pg_catalog first). The tables are filled again before
// a statement when a file's schema_version moved, outside any transaction, so a transaction block reads the catalog of
// the moment it began. The catalog names every table of the database, as PostgreSQL's does; reading a table's rows is
// still checked against the user's grants. Nothing of it is ever written to a file.

// PostgreSQL's type OIDs the catalog uses beyond those pgwire names.
const (
	oidChar              = pgwire.OIDChar
	oidName              = pgwire.OIDName
	oidInt2Vector uint32 = 22
	oidRegproc    uint32 = 24
	oidXID        uint32 = 28
	oidOIDVector  uint32 = 30
	oidNodeTree          = pgwire.OIDNodeTree
	oidCharArray  uint32 = 1002
	oidNameArray  uint32 = 1003
	oidInt2Array  uint32 = 1005
	oidTextArray  uint32 = 1009
	oidOIDArray   uint32 = 1028
	oidACLArray   uint32 = 1034
)

// Fixed OIDs of what every PostgreSQL has.
const (
	namespaceCatalog     uint32 = 11
	namespacePublic      uint32 = 2200
	namespaceInformation uint32 = 13183
	ownerOID             uint32 = 10 // the session's user, as the bootstrap superuser is in PostgreSQL
	databaseOID          uint32 = 16384
	firstSchemaOID       uint32 = 16385 // a schema other than public: 16385 + its place
	firstObjectOID       uint32 = 16400 // tables, views, indexes and constraints, in the order the session meets them
	accessMethodBTree    uint32 = 403
	accessMethodHeap     uint32 = 2
	tablespaceDefault    uint32 = 1663
	encodingUTF8                = 6
)

// catalogTypes are the column types of the catalog tables: the PostgreSQL type each column answers with.
var catalogTypes = map[string]uint32{
	"oid": pgwire.OIDOID, "name": oidName, "text": pgwire.OIDText, "varchar": pgwire.OIDVarchar, "char": oidChar,
	"bool": pgwire.OIDBool, "int2": pgwire.OIDInt2, "int4": pgwire.OIDInt4, "int8": pgwire.OIDInt8,
	"float4": pgwire.OIDFloat4, "float8": pgwire.OIDFloat8, "xid": oidXID, "regproc": oidRegproc,
	"int2vector": oidInt2Vector, "oidvector": oidOIDVector, "pg_node_tree": oidNodeTree,
	"char[]": oidCharArray, "name[]": oidNameArray, "int2[]": oidInt2Array, "text[]": oidTextArray,
	"oid[]": oidOIDArray, "aclitem[]": oidACLArray, "timestamptz": pgwire.OIDTimestampTZ,
}

// catalogDefinitions are the catalog tables, as "name type, …". A pg_catalog table keeps its PostgreSQL name; an
// information_schema view is information_schema_<name>; kitwork_pg_lookup holds what a reg* cast and
// pg_get_indexdef, pg_get_constraintdef, pg_get_viewdef answer (translate.go's lookup).
var catalogDefinitions = []struct{ name, columns string }{
	{"pg_namespace", "oid oid, nspname name, nspowner oid, nspacl aclitem[]"},
	{"pg_class", "oid oid, relname name, relnamespace oid, reltype oid, reloftype oid, relowner oid, relam oid, " +
		"relfilenode oid, reltablespace oid, relpages int4, reltuples float4, relallvisible int4, reltoastrelid oid, " +
		"relhasindex bool, relisshared bool, relpersistence char, relkind char, relnatts int2, relchecks int2, " +
		"relhasrules bool, relhastriggers bool, relhassubclass bool, relrowsecurity bool, relforcerowsecurity bool, " +
		"relispopulated bool, relreplident char, relispartition bool, relrewrite oid, relfrozenxid xid, " +
		"relminmxid xid, relacl aclitem[], reloptions text[], relpartbound pg_node_tree"},
	{"pg_attribute", "attrelid oid, attname name, atttypid oid, attlen int2, attnum int2, attcacheoff int4, " +
		"atttypmod int4, attndims int2, attbyval bool, attalign char, attstorage char, attcompression char, " +
		"attnotnull bool, atthasdef bool, atthasmissing bool, attidentity char, attgenerated char, attisdropped bool, " +
		"attislocal bool, attinhcount int2, attstattarget int2, attcollation oid, attacl aclitem[], attoptions text[], " +
		"attfdwoptions text[], attmissingval text"},
	{"pg_type", "oid oid, typname name, typnamespace oid, typowner oid, typlen int2, typbyval bool, typtype char, " +
		"typcategory char, typispreferred bool, typisdefined bool, typdelim char, typrelid oid, typsubscript regproc, " +
		"typelem oid, typarray oid, typinput regproc, typoutput regproc, typreceive regproc, typsend regproc, " +
		"typmodin regproc, typmodout regproc, typanalyze regproc, typalign char, typstorage char, typnotnull bool, " +
		"typbasetype oid, typtypmod int4, typndims int4, typcollation oid, typdefaultbin pg_node_tree, typdefault text, " +
		"typacl aclitem[]"},
	{"pg_index", "indexrelid oid, indrelid oid, indnatts int2, indnkeyatts int2, indisunique bool, " +
		"indnullsnotdistinct bool, indisprimary bool, indisexclusion bool, indimmediate bool, indisclustered bool, " +
		"indisvalid bool, indcheckxmin bool, indisready bool, indislive bool, indisreplident bool, indkey int2vector, " +
		"indcollation oidvector, indclass oidvector, indoption int2vector, indexprs pg_node_tree, indpred pg_node_tree"},
	{"pg_am", "oid oid, amname name, amhandler regproc, amtype char"},
	{"pg_constraint", "oid oid, conname name, connamespace oid, contype char, condeferrable bool, condeferred bool, " +
		"convalidated bool, conrelid oid, contypid oid, conindid oid, conparentid oid, confrelid oid, confupdtype char, " +
		"confdeltype char, confmatchtype char, conislocal bool, coninhcount int2, connoinherit bool, conkey int2[], " +
		"confkey int2[], conpfeqop oid[], conppeqop oid[], conffeqop oid[], confdelsetcols int2[], conexclop oid[], " +
		"conbin pg_node_tree, consrc text"},
	{"pg_attrdef", "oid oid, adrelid oid, adnum int2, adbin pg_node_tree, adsrc text"},
	{"pg_description", "objoid oid, classoid oid, objsubid int4, description text"},
	{"pg_shdescription", "objoid oid, classoid oid, description text"},
	{"pg_proc", "oid oid, proname name, pronamespace oid, proowner oid, prolang oid, procost float4, prorows float4, " +
		"provariadic oid, prosupport regproc, prokind char, prosecdef bool, proleakproof bool, proisstrict bool, " +
		"proretset bool, provolatile char, proparallel char, pronargs int2, pronargdefaults int2, prorettype oid, " +
		"proargtypes oidvector, proallargtypes oid[], proargmodes char[], proargnames text[], " +
		"proargdefaults pg_node_tree, protrftypes oid[], prosrc text, probin text, prosqlbody pg_node_tree, " +
		"proconfig text[], proacl aclitem[], proisagg bool, proiswindow bool"},
	{"pg_aggregate", "aggfnoid regproc, aggkind char, aggnumdirectargs int2, aggtransfn regproc, aggfinalfn regproc"},
	{"pg_language", "oid oid, lanname name, lanowner oid, lanispl bool, lanpltrusted bool, lanplcallfoid oid, " +
		"laninline oid, lanvalidator oid, lanacl aclitem[]"},
	{"pg_database", "oid oid, datname name, datdba oid, encoding int4, datlocprovider char, datistemplate bool, " +
		"datallowconn bool, datconnlimit int4, datfrozenxid xid, datminmxid xid, dattablespace oid, datcollate text, " +
		"datctype text, daticulocale text, daticurules text, datcollversion text, datacl aclitem[]"},
	{"pg_roles", "rolname name, rolsuper bool, rolinherit bool, rolcreaterole bool, rolcreatedb bool, " +
		"rolcanlogin bool, rolreplication bool, rolconnlimit int4, rolpassword text, rolvaliduntil timestamptz, " +
		"rolbypassrls bool, rolconfig text[], oid oid"},
	{"pg_user", "usename name, usesysid oid, usecreatedb bool, usesuper bool, userepl bool, usebypassrls bool, " +
		"passwd text, valuntil timestamptz, useconfig text[]"},
	{"pg_auth_members", "oid oid, roleid oid, member oid, grantor oid, admin_option bool"},
	{"pg_settings", "name text, setting text, unit text, category text, short_desc text, extra_desc text, " +
		"context text, vartype text, source text, min_val text, max_val text, enumvals text[], boot_val text, " +
		"reset_val text, sourcefile text, sourceline int4, pending_restart bool"},
	{"pg_tablespace", "oid oid, spcname name, spcowner oid, spcacl aclitem[], spcoptions text[]"},
	{"pg_inherits", "inhrelid oid, inhparent oid, inhseqno int4, inhdetachpending bool"},
	{"pg_trigger", "oid oid, tgrelid oid, tgparentid oid, tgname name, tgfoid oid, tgtype int2, tgenabled char, " +
		"tgisinternal bool, tgconstrrelid oid, tgconstrindid oid, tgconstraint oid, tgdeferrable bool, " +
		"tginitdeferred bool, tgnargs int2, tgattr int2vector, tgargs text, tgqual pg_node_tree, tgoldtable name, " +
		"tgnewtable name"},
	{"pg_event_trigger", "oid oid, evtname name, evtevent name, evtowner oid, evtfoid oid, evtenabled char, evttags text[]"},
	{"pg_rewrite", "oid oid, rulename name, ev_class oid, ev_type char, ev_enabled char, is_instead bool, " +
		"ev_qual pg_node_tree, ev_action pg_node_tree"},
	{"pg_policy", "oid oid, polname name, polrelid oid, polcmd char, polpermissive bool, polroles oid[], " +
		"polqual pg_node_tree, polwithcheck pg_node_tree"},
	{"pg_extension", "oid oid, extname name, extowner oid, extnamespace oid, extrelocatable bool, extversion text, " +
		"extconfig oid[], extcondition text[]"},
	{"pg_enum", "oid oid, enumtypid oid, enumsortorder float4, enumlabel name"},
	{"pg_sequence", "seqrelid oid, seqtypid oid, seqstart int8, seqincrement int8, seqmax int8, seqmin int8, " +
		"seqcache int8, seqcycle bool"},
	{"pg_depend", "classid oid, objid oid, objsubid int4, refclassid oid, refobjid oid, refobjsubid int4, deptype char"},
	{"pg_collation", "oid oid, collname name, collnamespace oid, collowner oid, collprovider char, " +
		"collisdeterministic bool, collencoding int4, collcollate text, collctype text, colliculocale text, " +
		"collicurules text, collversion text"},
	{"pg_partitioned_table", "partrelid oid, partstrat char, partnatts int2, partdefid oid, partattrs int2vector, " +
		"partclass oidvector, partcollation oidvector, partexprs pg_node_tree"},
	{"pg_foreign_table", "ftrelid oid, ftserver oid, ftoptions text[]"},
	{"pg_statistic_ext", "oid oid, stxrelid oid, stxname name, stxnamespace oid, stxowner oid, stxkeys int2vector, stxkind char[]"},
	{"pg_publication", "oid oid, pubname name, pubowner oid, puballtables bool"},
	{"pg_subscription", "oid oid, subdbid oid, subname name, subowner oid, subenabled bool"},
	{"pg_stat_activity", "datid oid, datname name, pid int4, usesysid oid, usename name, application_name text, " +
		"client_addr text, client_port int4, backend_start timestamptz, xact_start timestamptz, " +
		"query_start timestamptz, state_change timestamptz, wait_event_type text, wait_event text, state text, " +
		"query text, backend_type text"},
	{"pg_stat_user_tables", "relid oid, schemaname name, relname name, seq_scan int8, idx_scan int8, " +
		"n_tup_ins int8, n_tup_upd int8, n_tup_del int8, n_live_tup int8, n_dead_tup int8, last_vacuum timestamptz, " +
		"last_autovacuum timestamptz, last_analyze timestamptz, last_autoanalyze timestamptz"},
	{"pg_locks", "locktype text, database oid, relation oid, pid int4, mode text, granted bool"},
	{"pg_tables", "schemaname name, tablename name, tableowner name, tablespace name, hasindexes bool, " +
		"hasrules bool, hastriggers bool, rowsecurity bool"},
	{"pg_views", "schemaname name, viewname name, viewowner name, definition text"},
	{"pg_matviews", "schemaname name, matviewname name, matviewowner name, tablespace name, hasindexes bool, " +
		"ispopulated bool, definition text"},
	{"pg_indexes", "schemaname name, tablename name, indexname name, tablespace name, indexdef text"},
	{"pg_sequences", "schemaname name, sequencename name, sequenceowner name, data_type text, start_value int8, " +
		"min_value int8, max_value int8, increment_by int8, cycle bool, cache_size int8, last_value int8"},
	{"information_schema_schemata", "catalog_name name, schema_name name, schema_owner name, " +
		"default_character_set_catalog name, default_character_set_schema name, default_character_set_name name, " +
		"sql_path varchar"},
	{"information_schema_tables", "table_catalog name, table_schema name, table_name name, table_type varchar, " +
		"self_referencing_column_name name, reference_generation varchar, user_defined_type_catalog name, " +
		"user_defined_type_schema name, user_defined_type_name name, is_insertable_into varchar, is_typed varchar, " +
		"commit_action varchar"},
	{"information_schema_columns", "table_catalog name, table_schema name, table_name name, column_name name, " +
		"ordinal_position int4, column_default varchar, is_nullable varchar, data_type varchar, " +
		"character_maximum_length int4, character_octet_length int4, numeric_precision int4, " +
		"numeric_precision_radix int4, numeric_scale int4, datetime_precision int4, interval_type varchar, " +
		"interval_precision int4, character_set_catalog name, character_set_schema name, character_set_name name, " +
		"collation_catalog name, collation_schema name, collation_name name, domain_catalog name, domain_schema name, " +
		"domain_name name, udt_catalog name, udt_schema name, udt_name name, scope_catalog name, scope_schema name, " +
		"scope_name name, maximum_cardinality int4, dtd_identifier name, is_self_referencing varchar, " +
		"is_identity varchar, identity_generation varchar, identity_start varchar, identity_increment varchar, " +
		"identity_maximum varchar, identity_minimum varchar, identity_cycle varchar, is_generated varchar, " +
		"generation_expression varchar, is_updatable varchar"},
	{"information_schema_views", "table_catalog name, table_schema name, table_name name, view_definition varchar, " +
		"check_option varchar, is_updatable varchar, is_insertable_into varchar, is_trigger_updatable varchar, " +
		"is_trigger_deletable varchar, is_trigger_insertable_into varchar"},
	{"information_schema_table_constraints", "constraint_catalog name, constraint_schema name, constraint_name name, " +
		"table_catalog name, table_schema name, table_name name, constraint_type varchar, is_deferrable varchar, " +
		"initially_deferred varchar, enforced varchar, nulls_distinct varchar"},
	{"information_schema_key_column_usage", "constraint_catalog name, constraint_schema name, constraint_name name, " +
		"table_catalog name, table_schema name, table_name name, column_name name, ordinal_position int4, " +
		"position_in_unique_constraint int4"},
	{"information_schema_constraint_column_usage", "table_catalog name, table_schema name, table_name name, " +
		"column_name name, constraint_catalog name, constraint_schema name, constraint_name name"},
	{"information_schema_constraint_table_usage", "table_catalog name, table_schema name, table_name name, " +
		"constraint_catalog name, constraint_schema name, constraint_name name"},
	{"information_schema_referential_constraints", "constraint_catalog name, constraint_schema name, " +
		"constraint_name name, unique_constraint_catalog name, unique_constraint_schema name, " +
		"unique_constraint_name name, match_option varchar, update_rule varchar, delete_rule varchar"},
	{"information_schema_check_constraints", "constraint_catalog name, constraint_schema name, constraint_name name, " +
		"check_clause varchar"},
	{"information_schema_routines", "specific_catalog name, specific_schema name, specific_name name, " +
		"routine_catalog name, routine_schema name, routine_name name, routine_type varchar, data_type varchar, " +
		"type_udt_catalog name, type_udt_schema name, type_udt_name name, routine_body varchar, " +
		"routine_definition varchar, external_language varchar, is_deterministic varchar, security_type varchar"},
	{"information_schema_parameters", "specific_catalog name, specific_schema name, specific_name name, " +
		"ordinal_position int4, parameter_mode varchar, parameter_name name, data_type varchar, udt_name name, " +
		"parameter_default varchar"},
	{"information_schema_triggers", "trigger_catalog name, trigger_schema name, trigger_name name, " +
		"event_manipulation varchar, event_object_catalog name, event_object_schema name, event_object_table name, " +
		"action_order int4, action_condition varchar, action_statement varchar, action_orientation varchar, " +
		"action_timing varchar"},
	{"information_schema_sequences", "sequence_catalog name, sequence_schema name, sequence_name name, " +
		"data_type varchar, numeric_precision int4, numeric_precision_radix int4, numeric_scale int4, " +
		"start_value varchar, minimum_value varchar, maximum_value varchar, increment varchar, cycle_option varchar"},
	{"information_schema_character_sets", "character_set_catalog name, character_set_schema name, " +
		"character_set_name name, character_repertoire name, form_of_use name, default_collate_catalog name, " +
		"default_collate_schema name, default_collate_name name"},
	{"information_schema_enabled_roles", "role_name name"},
	{"information_schema_applicable_roles", "grantee name, role_name name, is_grantable varchar"},
	{"information_schema_table_privileges", "grantor name, grantee name, table_catalog name, table_schema name, " +
		"table_name name, privilege_type varchar, is_grantable varchar, with_hierarchy varchar"},
	{"information_schema_domains", "domain_catalog name, domain_schema name, domain_name name, data_type varchar"},
	{"kitwork_pg_lookup", "kitwork_kind text, kitwork_key text, kitwork_value text"},
}

// catalogTable is one catalog table: its columns in order and the PostgreSQL type of each.
type catalogTable struct {
	name    string
	columns []string
	types   map[string]uint32
}

var catalogTables, catalogTablesByName = func() ([]catalogTable, map[string]catalogTable) {
	tables := make([]catalogTable, 0, len(catalogDefinitions))
	byName := make(map[string]catalogTable, len(catalogDefinitions))
	for _, definition := range catalogDefinitions {
		table := catalogTable{name: definition.name, types: map[string]uint32{}}
		for _, column := range strings.Split(definition.columns, ",") {
			parts := strings.Fields(column)
			oid, known := catalogTypes[parts[1]]
			if len(parts) != 2 || !known {
				panic("gateway: catalog column " + definition.name + "." + column)
			}
			table.columns = append(table.columns, parts[0])
			table.types[parts[0]] = oid
		}
		tables = append(tables, table)
		byName[table.name] = table
	}
	return tables, byName
}()

// catalogColumnType is the PostgreSQL type of a column of a catalog table, for typing a result column read from it.
func catalogColumnType(table, column string) (uint32, bool) {
	definition, known := catalogTablesByName[table]
	if !known {
		return 0, false
	}
	oid, known := definition.types[column]
	return oid, known
}

// isCatalogTable reports a name translate.go qualifies with temp: a catalog table.
func isCatalogTable(name string) bool {
	_, known := catalogTablesByName[strings.ToLower(name)]
	return known
}

// sqliteType is the type a catalog column is declared with in its TEMP table: what SQLite compares it as.
func sqliteType(oid uint32) string {
	switch oid {
	case pgwire.OIDOID, pgwire.OIDBool, pgwire.OIDInt2, pgwire.OIDInt4, pgwire.OIDInt8, oidXID:
		return "INTEGER"
	case pgwire.OIDFloat4, pgwire.OIDFloat8:
		return "REAL"
	}
	return "TEXT"
}

// createCatalog makes the catalog's TEMP tables, empty, before the session is made read-only.
func (session *Session) createCatalog(ctx context.Context) error {
	for _, table := range catalogTables {
		columns := make([]string, len(table.columns))
		for index, name := range table.columns {
			columns[index] = quoteIdentifier(name) + " " + sqliteType(table.types[name])
		}
		definition := "CREATE TEMP TABLE " + quoteIdentifier(table.name) + " (" + strings.Join(columns, ", ")
		if table.name == "kitwork_pg_lookup" {
			// No rowid: an oid in a client's expression must never mean this table's rowid.
			definition += ", PRIMARY KEY (kitwork_kind, kitwork_key)) WITHOUT ROWID"
		} else {
			definition += ")"
		}
		if _, err := session.connection.ExecContext(ctx, definition); err != nil {
			return fmt.Errorf("gateway: catalog table %s: %w", table.name, err)
		}
	}
	return nil
}

// catalogRow is one row of a catalog table by column name; a column it leaves out is false, zero or empty for a
// bool, number or char column, NULL for the others (PostgreSQL's catalog has no NULL bool, number or char).
type catalogRow map[string]any

// refreshCatalog fills the catalog again when a schema file's version moved since it was filled, before a statement
// and outside any transaction.
func (session *Session) refreshCatalog(ctx context.Context) error {
	versions := make([]int64, len(session.order))
	changed := session.catalogVersions == nil
	for index, name := range session.order {
		if err := session.connection.QueryRowContext(ctx, "PRAGMA "+quoteIdentifier(name)+".schema_version").Scan(&versions[index]); err != nil {
			return sqliteError(err)
		}
		if !changed && session.catalogVersions[index] != versions[index] {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	// The bytes a value may have bound what a statement returns, not what the catalog reads of the files (a CREATE
	// statement, a default) or writes: SQLite's own limit while it is filled, the session's again after.
	bound, err := sqlite.Limit(session.connection, limitLength, sqliteMaxLength)
	if err != nil {
		return fmt.Errorf("gateway: SQLite length limit: %w", err)
	}
	defer func() { _, _ = sqlite.Limit(session.connection, limitLength, bound) }()
	rows, err := session.catalogRows(ctx)
	if err != nil {
		return err
	}
	if err := session.writeCatalog(ctx, rows); err != nil {
		return err
	}
	session.catalogVersions = versions
	return nil
}

// sqliteMaxLength is SQLite's own bound on a string or blob (SQLITE_MAX_LENGTH).
const sqliteMaxLength = 1_000_000_000

// writeCatalog replaces the rows of every catalog table in one transaction of the TEMP database. The session is
// read-only (query_only) for everything else; it is writable only here, while no statement of the client runs.
func (session *Session) writeCatalog(ctx context.Context, rows map[string][]catalogRow) (err error) {
	connection := session.connection
	if _, err := connection.ExecContext(ctx, "PRAGMA query_only=0"); err != nil {
		return sqliteError(err)
	}
	defer func() {
		if _, restore := connection.ExecContext(context.WithoutCancel(ctx), "PRAGMA query_only=1"); restore != nil && err == nil {
			err = sqliteError(restore)
		}
	}()
	if _, err := connection.ExecContext(ctx, "BEGIN"); err != nil {
		return sqliteError(err)
	}
	defer func() {
		if err != nil {
			_, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	for _, table := range catalogTables {
		if _, err := connection.ExecContext(ctx, "DELETE FROM temp."+quoteIdentifier(table.name)); err != nil {
			return sqliteError(err)
		}
		if len(rows[table.name]) == 0 {
			continue
		}
		quoted := make([]string, len(table.columns))
		marks := make([]string, len(table.columns))
		for index, name := range table.columns {
			quoted[index], marks[index] = quoteIdentifier(name), "?"
		}
		insert, err := connection.PrepareContext(ctx, "INSERT INTO temp."+quoteIdentifier(table.name)+
			" ("+strings.Join(quoted, ", ")+") VALUES ("+strings.Join(marks, ", ")+")")
		if err != nil {
			return sqliteError(err)
		}
		for _, row := range rows[table.name] {
			values := make([]any, len(table.columns))
			for index, name := range table.columns {
				value, given := row[name]
				if !given {
					value = catalogDefault(table.types[name])
				}
				values[index] = value
			}
			if _, err := insert.ExecContext(ctx, values...); err != nil {
				_ = insert.Close()
				return fmt.Errorf("gateway: catalog row of %s: %w", table.name, err)
			}
		}
		if err := insert.Close(); err != nil {
			return sqliteError(err)
		}
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return sqliteError(err)
	}
	return nil
}

func catalogDefault(oid uint32) any {
	switch oid {
	case pgwire.OIDBool, pgwire.OIDOID, pgwire.OIDInt2, pgwire.OIDInt4, pgwire.OIDInt8, oidXID:
		return int64(0)
	case pgwire.OIDFloat4, pgwire.OIDFloat8:
		return float64(0)
	case oidChar:
		return ""
	}
	return nil
}

// catalogRelation is one table, view or index of a schema file, as the catalog shows it.
type catalogRelation struct {
	oid       uint32
	schema    string
	namespace uint32
	name      string
	kind      string // "r" table, "v" view
	sql       string
	columns   []catalogAttribute
	tuples    float64
	indexes   []*catalogIndex
	keys      []*catalogKey
}

type catalogAttribute struct {
	number    int
	name      string
	notNull   bool
	defaulted sql.NullString
	primary   int
	generated string // "s" for a generated column
	kind      columnType
}

type catalogIndex struct {
	oid        uint32
	name       string
	unique     bool
	primary    bool
	columns    []int // attribute numbers; 0 for an expression
	names      []string
	descending []bool
	predicate  string
	definition string
}

type catalogKey struct {
	oid        uint32
	name       string
	kind       string // "p", "u", "f"
	columns    []int
	names      []string
	index      uint32
	references *catalogRelation
	referenced []int
	refNames   []string
	onUpdate   string
	onDelete   string
	definition string
}

// objectOID is the OID of a schema object, the same for the session's life: the next free one the first time the
// session meets it.
func (session *Session) objectOID(kind, schema, name string) uint32 {
	key := kind + "\x00" + schema + "\x00" + name
	if oid, known := session.oids[key]; known {
		return oid
	}
	oid := firstObjectOID + uint32(len(session.oids))
	session.oids[key] = oid
	return oid
}

func schemaNamespace(index int, name string) uint32 {
	if name == "public" {
		return namespacePublic
	}
	return firstSchemaOID + uint32(index)
}

// readRelations reads the tables, views and indexes of every attached schema.
func (session *Session) readRelations(ctx context.Context) ([]*catalogRelation, error) {
	var relations []*catalogRelation
	for index, schema := range session.order {
		found, err := session.readSchema(ctx, index, schema)
		if err != nil {
			return nil, err
		}
		relations = append(relations, found...)
	}
	return relations, nil
}

func (session *Session) readSchema(ctx context.Context, index int, schema string) ([]*catalogRelation, error) {
	quoted := quoteIdentifier(schema)
	rows, err := session.connection.QueryContext(ctx, "SELECT type, name, coalesce(sql, '') FROM "+quoted+
		".sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\' ORDER BY rowid")
	if err != nil {
		return nil, sqliteError(err)
	}
	var relations []*catalogRelation
	for rows.Next() {
		var kind, name, text string
		if err := rows.Scan(&kind, &name, &text); err != nil {
			_ = rows.Close()
			return nil, sqliteError(err)
		}
		relation := &catalogRelation{schema: schema, namespace: schemaNamespace(index, schema), name: name, sql: text,
			kind: "r", tuples: -1}
		if kind == "view" {
			relation.kind = "v"
		}
		relation.oid = session.objectOID("relation", schema, name)
		relations = append(relations, relation)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, sqliteError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, sqliteError(err)
	}
	statistics, err := session.readStatistics(ctx, schema)
	if err != nil {
		return nil, err
	}
	byName := map[string]*catalogRelation{}
	for _, relation := range relations {
		byName[strings.ToLower(relation.name)] = relation
		if err := session.readColumns(ctx, relation); err != nil {
			return nil, err
		}
		if tuples, known := statistics[strings.ToLower(relation.name)]; known && relation.kind == "r" {
			relation.tuples = tuples
		}
	}
	for _, relation := range relations {
		if relation.kind != "r" {
			continue
		}
		if err := session.readIndexes(ctx, relation); err != nil {
			return nil, err
		}
		if err := session.readForeignKeys(ctx, relation, byName); err != nil {
			return nil, err
		}
	}
	return relations, nil
}

// readStatistics reads the row count ANALYZE left in sqlite_stat1 for each table (reltuples); none without it. Each
// row of a table's indexes starts with the rows its index holds: the most of them is the table's (a partial index
// holds fewer).
func (session *Session) readStatistics(ctx context.Context, schema string) (map[string]float64, error) {
	quoted := quoteIdentifier(schema)
	var present int
	if err := session.connection.QueryRowContext(ctx, "SELECT count(*) FROM "+quoted+
		".sqlite_schema WHERE type = 'table' AND name = 'sqlite_stat1'").Scan(&present); err != nil {
		return nil, sqliteError(err)
	}
	result := map[string]float64{}
	if present == 0 {
		return result, nil
	}
	rows, err := session.connection.QueryContext(ctx, "SELECT tbl, stat FROM "+quoted+".sqlite_stat1")
	if err != nil {
		return nil, sqliteError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, stat sql.NullString
		if err := rows.Scan(&table, &stat); err != nil {
			return nil, sqliteError(err)
		}
		key := strings.ToLower(table.String)
		first, err := strconv.ParseFloat(strings.Fields(stat.String + " x")[0], 64)
		if current, known := result[key]; err == nil && (!known || first > current) {
			result[key] = first
		}
	}
	return result, sqliteError(rows.Err())
}

func (session *Session) readColumns(ctx context.Context, relation *catalogRelation) error {
	rows, err := session.connection.QueryContext(ctx,
		`SELECT cid, name, coalesce(type, ''), "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?, ?) ORDER BY cid`,
		relation.name, relation.schema)
	if err != nil {
		return sqliteError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primary, hidden int
		var name, declared string
		var defaulted sql.NullString
		if err := rows.Scan(&cid, &name, &declared, &notNull, &defaulted, &primary, &hidden); err != nil {
			return sqliteError(err)
		}
		if hidden == 1 {
			continue // a hidden column of a virtual table
		}
		attribute := catalogAttribute{number: len(relation.columns) + 1, name: name, notNull: notNull != 0 || primary > 0,
			defaulted: defaulted, primary: primary}
		if hidden == 2 || hidden == 3 {
			attribute.generated = "s"
		}
		attribute.kind = session.columnTypeOf(relation.schema, relation.name, name, declared)
		relation.columns = append(relation.columns, attribute)
	}
	return sqliteError(rows.Err())
}

func (relation *catalogRelation) attribute(name string) (catalogAttribute, bool) {
	for _, attribute := range relation.columns {
		if strings.EqualFold(attribute.name, name) {
			return attribute, true
		}
	}
	return catalogAttribute{}, false
}

// readIndexes reads a table's indexes, its primary key and its UNIQUE constraints. SQLite names the index of a
// PRIMARY KEY or UNIQUE constraint sqlite_autoindex_…; the catalog gives it PostgreSQL's name (<table>_pkey,
// <table>_<columns>_key). A rowid table whose INTEGER PRIMARY KEY is the rowid has no index for it: the catalog shows
// the one PostgreSQL would have.
func (session *Session) readIndexes(ctx context.Context, relation *catalogRelation) error {
	type listed struct {
		name, origin string
		unique       bool
	}
	rows, err := session.connection.QueryContext(ctx,
		`SELECT name, "unique", origin FROM pragma_index_list(?, ?) ORDER BY seq DESC`, relation.name, relation.schema)
	if err != nil {
		return sqliteError(err)
	}
	var indexes []listed
	for rows.Next() {
		var item listed
		var unique int
		if err := rows.Scan(&item.name, &unique, &item.origin); err != nil {
			_ = rows.Close()
			return sqliteError(err)
		}
		item.unique = unique != 0
		indexes = append(indexes, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return sqliteError(err)
	}
	if err := rows.Close(); err != nil {
		return sqliteError(err)
	}
	var primaryColumns []catalogAttribute
	for _, attribute := range relation.columns {
		if attribute.primary > 0 {
			primaryColumns = append(primaryColumns, attribute)
		}
	}
	sort.SliceStable(primaryColumns, func(left, right int) bool { return primaryColumns[left].primary < primaryColumns[right].primary })
	var primaryIndex *catalogIndex
	for _, item := range indexes {
		index := &catalogIndex{unique: item.unique, primary: item.origin == "pk"}
		if err := session.readIndexColumns(ctx, relation, item.name, index); err != nil {
			return err
		}
		var text string
		_ = session.connection.QueryRowContext(ctx, "SELECT coalesce(sql, '') FROM "+quoteIdentifier(relation.schema)+
			".sqlite_schema WHERE type = 'index' AND name = ?", item.name).Scan(&text)
		switch item.origin {
		case "pk":
			index.name = relation.name + "_pkey"
			primaryIndex = index
		case "u":
			index.name = relation.name + "_" + strings.Join(index.names, "_") + "_key"
		default:
			index.name = item.name
		}
		index.predicate = indexPredicate(text)
		index.oid = session.objectOID("index", relation.schema, index.name)
		index.definition = indexDefinition(relation, index, text)
		relation.indexes = append(relation.indexes, index)
		if item.origin == "u" {
			key := &catalogKey{name: index.name, kind: "u", columns: index.columns, names: index.names, index: index.oid}
			key.oid = session.objectOID("constraint", relation.schema, relation.name+"\x00"+key.name)
			key.definition = "UNIQUE (" + quotedList(key.names) + ")"
			relation.keys = append(relation.keys, key)
		}
	}
	if len(primaryColumns) > 0 {
		if primaryIndex == nil {
			primaryIndex = &catalogIndex{name: relation.name + "_pkey", unique: true, primary: true}
			for _, attribute := range primaryColumns {
				primaryIndex.columns = append(primaryIndex.columns, attribute.number)
				primaryIndex.names = append(primaryIndex.names, attribute.name)
				primaryIndex.descending = append(primaryIndex.descending, false)
			}
			primaryIndex.oid = session.objectOID("index", relation.schema, primaryIndex.name)
			primaryIndex.definition = indexDefinition(relation, primaryIndex, "")
			relation.indexes = append([]*catalogIndex{primaryIndex}, relation.indexes...)
		}
		key := &catalogKey{name: relation.name + "_pkey", kind: "p", columns: primaryIndex.columns, names: primaryIndex.names,
			index: primaryIndex.oid}
		key.oid = session.objectOID("constraint", relation.schema, relation.name+"\x00"+key.name)
		key.definition = "PRIMARY KEY (" + quotedList(key.names) + ")"
		relation.keys = append([]*catalogKey{key}, relation.keys...)
	}
	return nil
}

func (session *Session) readIndexColumns(ctx context.Context, relation *catalogRelation, name string, index *catalogIndex) error {
	rows, err := session.connection.QueryContext(ctx,
		`SELECT cid, name, "desc" FROM pragma_index_xinfo(?, ?) WHERE key = 1 ORDER BY seqno`, name, relation.schema)
	if err != nil {
		return sqliteError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, descending int
		var column sql.NullString
		if err := rows.Scan(&cid, &column, &descending); err != nil {
			return sqliteError(err)
		}
		number := 0
		if attribute, found := relation.attribute(column.String); found && column.Valid {
			number = attribute.number
		}
		index.columns = append(index.columns, number)
		index.names = append(index.names, column.String)
		index.descending = append(index.descending, descending != 0)
	}
	return sqliteError(rows.Err())
}

func (session *Session) readForeignKeys(ctx context.Context, relation *catalogRelation, tables map[string]*catalogRelation) error {
	rows, err := session.connection.QueryContext(ctx,
		`SELECT id, "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list(?, ?) ORDER BY id, seq`,
		relation.name, relation.schema)
	if err != nil {
		return sqliteError(err)
	}
	defer rows.Close()
	keys := map[int]*catalogKey{}
	var order []int
	for rows.Next() {
		var id int
		var table, from string
		var to sql.NullString
		var onUpdate, onDelete string
		if err := rows.Scan(&id, &table, &from, &to, &onUpdate, &onDelete); err != nil {
			return sqliteError(err)
		}
		key, seen := keys[id]
		if !seen {
			key = &catalogKey{kind: "f", references: tables[strings.ToLower(table)], onUpdate: foreignAction(onUpdate),
				onDelete: foreignAction(onDelete)}
			keys[id] = key
			order = append(order, id)
		}
		number := 0
		if attribute, found := relation.attribute(from); found {
			number = attribute.number
		}
		key.columns = append(key.columns, number)
		key.names = append(key.names, from)
		if key.references != nil {
			referencedName := to.String
			if !to.Valid || referencedName == "" {
				// REFERENCES t without columns: t's primary key, in order.
				if position := len(key.refNames); position < len(key.references.primaryNames()) {
					referencedName = key.references.primaryNames()[position]
				}
			}
			referenced := 0
			if attribute, found := key.references.attribute(referencedName); found {
				referenced = attribute.number
			}
			key.referenced = append(key.referenced, referenced)
			key.refNames = append(key.refNames, referencedName)
		}
	}
	if err := rows.Err(); err != nil {
		return sqliteError(err)
	}
	for _, id := range order {
		key := keys[id]
		if key.references == nil {
			continue // a key to a table the file does not have
		}
		key.name = relation.name + "_" + strings.Join(key.names, "_") + "_fkey"
		key.oid = session.objectOID("constraint", relation.schema, relation.name+"\x00"+key.name)
		key.definition = "FOREIGN KEY (" + quotedList(key.names) + ") REFERENCES " + quoteName(key.references.name) +
			"(" + quotedList(key.refNames) + ")" + actionClause("UPDATE", key.onUpdate) + actionClause("DELETE", key.onDelete)
		relation.keys = append(relation.keys, key)
	}
	return nil
}

func (relation *catalogRelation) primaryNames() []string {
	var primary []catalogAttribute
	for _, attribute := range relation.columns {
		if attribute.primary > 0 {
			primary = append(primary, attribute)
		}
	}
	sort.SliceStable(primary, func(left, right int) bool { return primary[left].primary < primary[right].primary })
	names := make([]string, len(primary))
	for index, attribute := range primary {
		names[index] = attribute.name
	}
	return names
}

// foreignAction is SQLite's ON UPDATE / ON DELETE as pg_constraint's confupdtype / confdeltype.
func foreignAction(action string) string {
	switch strings.ToUpper(action) {
	case "RESTRICT":
		return "r"
	case "CASCADE":
		return "c"
	case "SET NULL":
		return "n"
	case "SET DEFAULT":
		return "d"
	}
	return "a"
}

func actionClause(event, action string) string {
	words := map[string]string{"r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}
	if word, known := words[action]; known {
		return " ON " + event + " " + word
	}
	return ""
}

func actionRule(action string) string {
	words := map[string]string{"r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}
	if word, known := words[action]; known {
		return word
	}
	return "NO ACTION"
}

// quoteName is an identifier as PostgreSQL writes it: bare when it is lower case and plain, quoted otherwise.
func quoteName(name string) string {
	if pgPlainName(name) {
		return name
	}
	return quoteIdentifier(name)
}

func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = quoteName(name)
	}
	return strings.Join(quoted, ", ")
}

// indexPredicate is the WHERE clause of a partial index's CREATE INDEX, without the WHERE.
func indexPredicate(text string) string {
	tokens, err := scan(text)
	if err != nil {
		return ""
	}
	depth := 0
	for index, candidate := range tokens {
		switch candidate.text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 && candidate.word() == "WHERE" && index+1 < len(tokens) {
			return strings.TrimSpace(text[tokens[index+1].start:])
		}
	}
	return ""
}

// indexColumnsText is what the parentheses of a CREATE INDEX hold (columns, expressions, COLLATE, DESC), as written.
func indexColumnsText(text string) string {
	tokens, err := scan(text)
	if err != nil {
		return ""
	}
	for index, candidate := range tokens {
		if candidate.word() != "ON" {
			continue
		}
		for open := index + 1; open < len(tokens); open++ {
			if tokens[open].text != "(" {
				continue
			}
			if close := matchingClose(tokens, open); close > open {
				return strings.TrimSpace(text[tokens[open].end:tokens[close].start])
			}
			return ""
		}
	}
	return ""
}

// indexDefinition is what pg_get_indexdef answers: CREATE [UNIQUE] INDEX name ON schema.table USING btree (…).
func indexDefinition(relation *catalogRelation, index *catalogIndex, text string) string {
	columns := indexColumnsText(text)
	if columns == "" {
		parts := make([]string, len(index.names))
		for position, name := range index.names {
			parts[position] = quoteName(name)
			if position < len(index.descending) && index.descending[position] {
				parts[position] += " DESC"
			}
		}
		columns = strings.Join(parts, ", ")
	}
	definition := "CREATE "
	if index.unique {
		definition += "UNIQUE "
	}
	definition += "INDEX " + quoteName(index.name) + " ON " + quoteName(relation.schema) + "." + quoteName(relation.name) +
		" USING btree (" + columns + ")"
	if index.predicate != "" {
		definition += " WHERE " + index.predicate
	}
	return definition
}

// viewDefinition is the SELECT of a CREATE VIEW, as written.
func viewDefinition(text string) string {
	tokens, err := scan(text)
	if err != nil {
		return ""
	}
	for index, candidate := range tokens {
		if candidate.word() == "AS" && index+1 < len(tokens) {
			return strings.TrimSpace(text[tokens[index+1].start:])
		}
	}
	return ""
}

// columnType is what the catalog says of one column of a schema file: its PostgreSQL type and how
// information_schema.columns describes it.
type columnType struct {
	oid       uint32
	modifier  int32 // atttypmod; -1 for none
	typeName  string
	dataType  string
	precision sql.NullInt64
	radix     sql.NullInt64
	scale     sql.NullInt64
}

// columnTypeOf decides a column's type as the result of a statement reading it does (result.go): its kind from
// Columns (decimal, bool, json), else its declared type's affinity.
func (session *Session) columnTypeOf(schema, table, column, declared string) columnType {
	if session.columns != nil {
		if kind, precision, scale, found := session.columns.Column(schema, table, column); found {
			switch kind {
			case "decimal":
				if precision <= 0 {
					precision = defaultDecimalDigits
				}
				scale = max(scale, 0)
				precision = max(precision, scale)
				return columnType{oid: pgwire.OIDNumeric, modifier: int32(precision<<16|scale) + numericHeaderSize,
					typeName: "numeric", dataType: "numeric", precision: validInteger(precision), radix: validInteger(10),
					scale: validInteger(scale)}
			case "bool":
				return columnType{oid: pgwire.OIDBool, modifier: -1, typeName: "bool", dataType: "boolean"}
			case "json":
				return columnType{oid: oidJSON, modifier: -1, typeName: "json", dataType: "json"}
			}
		}
	}
	upper := strings.ToUpper(declared)
	switch declaredType(declared) {
	case pgwire.OIDInt8:
		return columnType{oid: pgwire.OIDInt8, modifier: -1, typeName: "int8", dataType: "bigint",
			precision: validInteger(64), radix: validInteger(2), scale: validInteger(0)}
	case pgwire.OIDFloat8:
		return columnType{oid: pgwire.OIDFloat8, modifier: -1, typeName: "float8", dataType: "double precision",
			precision: validInteger(53), radix: validInteger(2)}
	case pgwire.OIDBytea:
		return columnType{oid: pgwire.OIDBytea, modifier: -1, typeName: "bytea", dataType: "bytea"}
	case pgwire.OIDText:
		return columnType{oid: pgwire.OIDText, modifier: -1, typeName: "text", dataType: "text"}
	}
	if upper == "" || strings.Contains(upper, "DATE") || strings.Contains(upper, "TIME") {
		// No declared type, or a date or time SQLite keeps as text or a number: the values decide, written as text.
		return columnType{oid: pgwire.OIDText, modifier: -1, typeName: "text", dataType: "text"}
	}
	return columnType{oid: pgwire.OIDNumeric, modifier: -1, typeName: "numeric", dataType: "numeric", radix: validInteger(10)}
}

func validInteger(value int) sql.NullInt64 { return sql.NullInt64{Int64: int64(value), Valid: true} }

func nullable(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

// catalogRows builds every row of the catalog from the attached schemas.
func (session *Session) catalogRows(ctx context.Context) (map[string][]catalogRow, error) {
	relations, err := session.readRelations(ctx)
	if err != nil {
		return nil, err
	}
	rows := map[string][]catalogRow{}
	looked := map[[2]string]bool{}
	namespaceNames := map[uint32]string{}
	var add func(table string, row catalogRow)
	add = func(table string, row catalogRow) {
		if table == "kitwork_pg_lookup" {
			// The first entry of a key wins: pg_catalog's names come before the files', as PostgreSQL searches it first.
			key := [2]string{row["kitwork_kind"].(string), row["kitwork_key"].(string)}
			if looked[key] {
				return
			}
			looked[key] = true
		}
		switch table {
		case "pg_namespace":
			namespaceNames[row["oid"].(uint32)] = row["nspname"].(string)
		case "pg_class":
			for _, entry := range classLookups(row, namespaceNames) {
				add("kitwork_pg_lookup", entry)
			}
		}
		rows[table] = append(rows[table], row)
	}
	database, user := session.target.Name, session.options.User
	if user == "" {
		user = "kitwork"
	}

	add("pg_database", catalogRow{"oid": databaseOID, "datname": database, "datdba": ownerOID, "encoding": encodingUTF8,
		"datlocprovider": "c", "datallowconn": true, "datconnlimit": -1, "dattablespace": tablespaceDefault,
		"datcollate": "C", "datctype": "C"})
	add("pg_roles", catalogRow{"rolname": user, "rolinherit": true, "rolcanlogin": true, "rolconnlimit": -1,
		"rolpassword": "********", "oid": ownerOID})
	add("kitwork_pg_lookup", lookupRow("role", strconv.FormatUint(uint64(ownerOID), 10), user))
	add("kitwork_pg_lookup", lookupRow("role oid", user, strconv.FormatUint(uint64(ownerOID), 10)))
	add("pg_user", catalogRow{"usename": user, "usesysid": ownerOID, "passwd": "********"})
	add("information_schema_enabled_roles", catalogRow{"role_name": user})
	add("pg_am", catalogRow{"oid": accessMethodHeap, "amname": "heap", "amhandler": "heap_tableam_handler", "amtype": "t"})
	add("pg_am", catalogRow{"oid": accessMethodBTree, "amname": "btree", "amhandler": "bthandler", "amtype": "i"})
	for _, language := range []struct {
		oid  uint32
		name string
	}{{12, "internal"}, {13, "c"}, {14, "sql"}} {
		add("pg_language", catalogRow{"oid": language.oid, "lanname": language.name, "lanowner": ownerOID,
			"lanpltrusted": language.name == "sql"})
	}
	add("pg_tablespace", catalogRow{"oid": tablespaceDefault, "spcname": "pg_default", "spcowner": ownerOID})
	add("pg_tablespace", catalogRow{"oid": uint32(1664), "spcname": "pg_global", "spcowner": ownerOID})
	add("pg_collation", catalogRow{"oid": uint32(100), "collname": "default", "collnamespace": namespaceCatalog,
		"collowner": ownerOID, "collprovider": "d", "collisdeterministic": true, "collencoding": -1})
	add("pg_collation", catalogRow{"oid": uint32(950), "collname": "C", "collnamespace": namespaceCatalog,
		"collowner": ownerOID, "collprovider": "c", "collisdeterministic": true, "collencoding": -1,
		"collcollate": "C", "collctype": "C"})
	add("information_schema_character_sets", catalogRow{"character_set_catalog": database,
		"character_set_schema": nil, "character_set_name": "UTF8", "character_repertoire": "UCS", "form_of_use": "UTF8",
		"default_collate_catalog": database, "default_collate_schema": "pg_catalog", "default_collate_name": "C"})
	names := make([]string, 0, len(settings))
	for name := range settings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		setting := settings[name]
		add("pg_settings", catalogRow{"name": setting[0], "setting": setting[1], "category": "Kitwork",
			"short_desc": setting[0], "context": "user", "vartype": "string", "source": "default",
			"boot_val": setting[1], "reset_val": setting[1]})
	}
	for _, typ := range pgTypes {
		add("pg_type", typ.row())
	}

	namespaces := []struct {
		oid  uint32
		name string
	}{{namespaceCatalog, "pg_catalog"}, {namespaceInformation, "information_schema"}}
	for index, schema := range session.order {
		namespaces = append(namespaces, struct {
			oid  uint32
			name string
		}{schemaNamespace(index, schema), schema})
	}
	for _, namespace := range namespaces {
		add("pg_namespace", catalogRow{"oid": namespace.oid, "nspname": namespace.name, "nspowner": ownerOID})
		add("kitwork_pg_lookup", lookupRow("namespace", strconv.FormatUint(uint64(namespace.oid), 10), namespace.name))
		add("kitwork_pg_lookup", lookupRow("namespace oid", namespace.name, strconv.FormatUint(uint64(namespace.oid), 10)))
		add("information_schema_schemata", catalogRow{"catalog_name": database, "schema_name": namespace.name,
			"schema_owner": user, "default_character_set_name": nil})
	}
	catalogNames := make([]string, 0, len(catalogRelationOIDs))
	for name := range catalogRelationOIDs {
		catalogNames = append(catalogNames, name)
	}
	sort.Strings(catalogNames)
	for _, name := range catalogNames {
		oid := catalogRelationOIDs[name]
		add("pg_class", catalogRow{"oid": oid, "relname": name, "relnamespace": namespaceCatalog, "relowner": ownerOID,
			"relam": accessMethodHeap, "relkind": "r", "relpersistence": "p", "relispopulated": true,
			"relreplident": "n", "reltuples": float64(-1), "relisshared": name == "pg_database"})
	}

	for _, relation := range relations {
		session.addRelationRows(relation, database, user, add)
	}
	return rows, nil
}

// catalogRelationOIDs are pg_class rows for the catalog tables themselves, at PostgreSQL's OIDs, so a client's
// 'pg_class'::regclass finds 1259 as in any PostgreSQL.
var catalogRelationOIDs = map[string]uint32{
	"pg_class": 1259, "pg_attribute": 1249, "pg_type": 1247, "pg_proc": 1255, "pg_namespace": 2615,
	"pg_index": 2610, "pg_constraint": 2606, "pg_database": 1262, "pg_description": 2609, "pg_attrdef": 2604,
	"pg_am": 2601, "pg_trigger": 2620, "pg_inherits": 2611, "pg_depend": 2608, "pg_language": 2612,
	"pg_tablespace": 1213, "pg_collation": 3456, "pg_enum": 3501, "pg_extension": 3079, "pg_sequence": 2224,
	"pg_rewrite": 2618, "pg_shdescription": 2396, "pg_partitioned_table": 3350, "pg_aggregate": 2600,
	"pg_auth_members": 1261, "pg_event_trigger": 3466, "pg_policy": 3256,
}

func (session *Session) addRelationRows(relation *catalogRelation, database, user string, add func(string, catalogRow)) {
	schema := relation.schema
	add("pg_class", catalogRow{"oid": relation.oid, "relname": relation.name, "relnamespace": relation.namespace,
		"relowner": ownerOID, "relam": map[bool]uint32{true: accessMethodHeap}[relation.kind == "r"],
		"relfilenode": relation.oid, "reltuples": relation.tuples, "relhasindex": len(relation.indexes) > 0,
		"relpersistence": "p", "relkind": relation.kind, "relnatts": len(relation.columns), "relispopulated": true,
		"relreplident": map[bool]string{true: "d", false: "n"}[relation.kind == "r"],
		"relhasrules":  relation.kind == "v"})
	tableType := "BASE TABLE"
	if relation.kind == "v" {
		tableType = "VIEW"
		definition := viewDefinition(relation.sql)
		add("pg_views", catalogRow{"schemaname": schema, "viewname": relation.name, "viewowner": user, "definition": definition})
		add("information_schema_views", catalogRow{"table_catalog": database, "table_schema": schema,
			"table_name": relation.name, "view_definition": definition, "check_option": "NONE", "is_updatable": "NO",
			"is_insertable_into": "NO", "is_trigger_updatable": "NO", "is_trigger_deletable": "NO",
			"is_trigger_insertable_into": "NO"})
		add("kitwork_pg_lookup", lookupRow("definition", strconv.FormatUint(uint64(relation.oid), 10), definition))
	} else {
		add("pg_tables", catalogRow{"schemaname": schema, "tablename": relation.name, "tableowner": user,
			"hasindexes": len(relation.indexes) > 0})
		add("pg_stat_user_tables", catalogRow{"relid": relation.oid, "schemaname": schema, "relname": relation.name,
			"n_live_tup": int64(max(relation.tuples, 0))})
	}
	add("information_schema_tables", catalogRow{"table_catalog": database, "table_schema": schema,
		"table_name": relation.name, "table_type": tableType, "is_insertable_into": "NO", "is_typed": "NO"})

	for _, attribute := range relation.columns {
		kind := attribute.kind
		size := int64(-1)
		if typ, known := pgTypesByOID[kind.oid]; known {
			size = int64(typ.length)
		}
		add("pg_attribute", catalogRow{"attrelid": relation.oid, "attname": attribute.name, "atttypid": kind.oid,
			"attlen": size, "attnum": attribute.number, "attcacheoff": -1, "atttypmod": kind.modifier,
			"attbyval": size > 0 && size <= 8, "attalign": "d", "attstorage": "p", "attnotnull": attribute.notNull,
			"atthasdef": attribute.defaulted.Valid, "attgenerated": attribute.generated, "attislocal": true,
			"attstattarget": -1, "attcollation": map[bool]uint32{true: 100}[kind.oid == pgwire.OIDText]})
		var defaulted any
		if attribute.defaulted.Valid {
			defaulted = attribute.defaulted.String
			add("pg_attrdef", catalogRow{"oid": session.objectOID("default", schema, relation.name+"\x00"+attribute.name),
				"adrelid": relation.oid, "adnum": attribute.number, "adbin": attribute.defaulted.String,
				"adsrc": attribute.defaulted.String})
		}
		generated, expression := "NEVER", any(nil)
		if attribute.generated != "" {
			generated, expression, defaulted = "ALWAYS", defaulted, nil
		}
		add("information_schema_columns", catalogRow{"table_catalog": database, "table_schema": schema,
			"table_name": relation.name, "column_name": attribute.name, "ordinal_position": attribute.number,
			"column_default": defaulted, "is_nullable": map[bool]string{true: "NO", false: "YES"}[attribute.notNull],
			"data_type": kind.dataType, "numeric_precision": nullable(kind.precision),
			"numeric_precision_radix": nullable(kind.radix), "numeric_scale": nullable(kind.scale),
			"udt_catalog": database, "udt_schema": "pg_catalog", "udt_name": kind.typeName,
			"dtd_identifier": strconv.Itoa(attribute.number), "is_self_referencing": "NO", "is_identity": "NO",
			"identity_cycle": "NO", "is_generated": generated, "generation_expression": expression, "is_updatable": "NO"})
	}

	for _, index := range relation.indexes {
		keys := make([]string, len(index.columns))
		options := make([]string, len(index.columns))
		classes := make([]string, len(index.columns))
		collations := make([]string, len(index.columns))
		for position, number := range index.columns {
			keys[position] = strconv.Itoa(number)
			options[position] = map[bool]string{true: "3", false: "0"}[position < len(index.descending) && index.descending[position]]
			classes[position], collations[position] = "0", "0"
		}
		add("pg_class", catalogRow{"oid": index.oid, "relname": index.name, "relnamespace": relation.namespace,
			"relowner": ownerOID, "relam": accessMethodBTree, "relfilenode": index.oid, "reltuples": float64(-1),
			"relpersistence": "p", "relkind": "i", "relnatts": len(index.columns), "relispopulated": true,
			"relreplident": "n"})
		var predicate any
		if index.predicate != "" {
			predicate = index.predicate
		}
		add("pg_index", catalogRow{"indexrelid": index.oid, "indrelid": relation.oid, "indnatts": len(index.columns),
			"indnkeyatts": len(index.columns), "indisunique": index.unique, "indisprimary": index.primary,
			"indimmediate": true, "indisvalid": true, "indisready": true, "indislive": true,
			"indkey": strings.Join(keys, " "), "indcollation": strings.Join(collations, " "),
			"indclass": strings.Join(classes, " "), "indoption": strings.Join(options, " "), "indpred": predicate})
		add("pg_indexes", catalogRow{"schemaname": schema, "tablename": relation.name, "indexname": index.name,
			"indexdef": index.definition})
		add("kitwork_pg_lookup", lookupRow("definition", strconv.FormatUint(uint64(index.oid), 10), index.definition))
	}

	for _, key := range relation.keys {
		row := catalogRow{"oid": key.oid, "conname": key.name, "connamespace": relation.namespace, "contype": key.kind,
			"convalidated": true, "conrelid": relation.oid, "conindid": key.index, "confupdtype": " ",
			"confdeltype": " ", "confmatchtype": " ", "conislocal": true, "connoinherit": true,
			"conkey": pgArray(intStrings(key.columns))}
		constraintType := map[string]string{"p": "PRIMARY KEY", "u": "UNIQUE", "f": "FOREIGN KEY"}[key.kind]
		if key.kind == "f" {
			row["confrelid"] = key.references.oid
			row["confupdtype"], row["confdeltype"], row["confmatchtype"] = key.onUpdate, key.onDelete, "s"
			row["confkey"] = pgArray(intStrings(key.referenced))
			row["conindid"] = uint32(0)
			for _, candidate := range key.references.keys {
				if candidate.kind != "f" && sameNames(candidate.names, key.refNames) {
					row["conindid"] = candidate.index
					add("information_schema_referential_constraints", catalogRow{"constraint_catalog": database,
						"constraint_schema": schema, "constraint_name": key.name, "unique_constraint_catalog": database,
						"unique_constraint_schema": schema, "unique_constraint_name": candidate.name,
						"match_option": "NONE", "update_rule": actionRule(key.onUpdate), "delete_rule": actionRule(key.onDelete)})
					break
				}
			}
		}
		add("pg_constraint", row)
		add("kitwork_pg_lookup", lookupRow("definition", strconv.FormatUint(uint64(key.oid), 10), key.definition))
		add("information_schema_table_constraints", catalogRow{"constraint_catalog": database,
			"constraint_schema": schema, "constraint_name": key.name, "table_catalog": database, "table_schema": schema,
			"table_name": relation.name, "constraint_type": constraintType, "is_deferrable": "NO",
			"initially_deferred": "NO", "enforced": "YES", "nulls_distinct": map[bool]any{true: "YES"}[key.kind == "u"]})
		add("information_schema_constraint_table_usage", catalogRow{"table_catalog": database, "table_schema": schema,
			"table_name":         map[bool]string{true: key.referencedTable(), false: relation.name}[key.kind == "f"],
			"constraint_catalog": database, "constraint_schema": schema, "constraint_name": key.name})
		for position, name := range key.names {
			row := catalogRow{"constraint_catalog": database, "constraint_schema": schema, "constraint_name": key.name,
				"table_catalog": database, "table_schema": schema, "table_name": relation.name, "column_name": name,
				"ordinal_position": position + 1}
			if key.kind == "f" {
				row["position_in_unique_constraint"] = position + 1
			}
			add("information_schema_key_column_usage", row)
		}
		usageTable, usageColumns := relation.name, key.names
		if key.kind == "f" {
			usageTable, usageColumns = key.references.name, key.refNames
		}
		for _, name := range usageColumns {
			add("information_schema_constraint_column_usage", catalogRow{"table_catalog": database,
				"table_schema": schema, "table_name": usageTable, "column_name": name, "constraint_catalog": database,
				"constraint_schema": schema, "constraint_name": key.name})
		}
	}
}

func lookupRow(kind, key, value string) catalogRow {
	return catalogRow{"kitwork_kind": kind, "kitwork_key": key, "kitwork_value": value}
}

// classLookups are the lookups of one pg_class row: its OID to its name (bare in pg_catalog and public, qualified
// elsewhere, as regclass writes it), and its qualified name, and its bare name in pg_catalog and public, to its OID.
func classLookups(row catalogRow, namespaces map[uint32]string) []catalogRow {
	oid := strconv.FormatUint(uint64(row["oid"].(uint32)), 10)
	name := row["relname"].(string)
	schemaName := namespaces[row["relnamespace"].(uint32)]
	written := quoteName(schemaName) + "." + quoteName(name)
	entries := []catalogRow{lookupRow("class oid", schemaName+"."+name, oid)}
	if schemaName == "public" || schemaName == "pg_catalog" {
		written = quoteName(name)
		entries = append(entries, lookupRow("class oid", name, oid))
	}
	return append(entries, lookupRow("class", oid, written))
}

func (key *catalogKey) referencedTable() string {
	if key.references == nil {
		return ""
	}
	return key.references.name
}

func sameNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !strings.EqualFold(left[index], right[index]) {
			return false
		}
	}
	return true
}

func intStrings(values []int) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strconv.Itoa(value)
	}
	return result
}

// pgType is one row of pg_type.
type pgType struct {
	oid      uint32
	name     string
	length   int
	kind     string // typtype: b base, p pseudo
	category string
	element  uint32
	array    uint32
	format   string // what format_type answers
	data     string // information_schema's data_type
}

func (typ pgType) row() catalogRow {
	input, output := typ.name+"in", typ.name+"out"
	if typ.category == "A" {
		input, output = "array_in", "array_out"
	}
	return catalogRow{"oid": typ.oid, "typname": typ.name, "typnamespace": namespaceCatalog, "typowner": ownerOID,
		"typlen": typ.length, "typbyval": typ.length > 0 && typ.length <= 8, "typtype": typ.kind,
		"typcategory": typ.category, "typisdefined": true, "typdelim": ",", "typelem": typ.element,
		"typarray": typ.array, "typinput": input, "typoutput": output, "typalign": "i", "typstorage": "p",
		"typtypmod": -1, "typcollation": map[bool]uint32{true: 100}[typ.category == "S"]}
}

var pgTypes = func() []pgType {
	base := []pgType{
		{16, "bool", 1, "b", "B", 0, 1000, "boolean", "boolean"},
		{17, "bytea", -1, "b", "U", 0, 1001, "bytea", "bytea"},
		{18, "char", 1, "b", "Z", 0, 1002, `"char"`, `"char"`},
		{19, "name", 64, "b", "S", 0, 1003, "name", "name"},
		{20, "int8", 8, "b", "N", 0, 1016, "bigint", "bigint"},
		{21, "int2", 2, "b", "N", 0, 1005, "smallint", "smallint"},
		{22, "int2vector", -1, "b", "A", 21, 1006, "int2vector", "ARRAY"},
		{23, "int4", 4, "b", "N", 0, 1007, "integer", "integer"},
		{24, "regproc", 4, "b", "N", 0, 1008, "regproc", "regproc"},
		{25, "text", -1, "b", "S", 0, 1009, "text", "text"},
		{26, "oid", 4, "b", "N", 0, 1028, "oid", "oid"},
		{28, "xid", 4, "b", "U", 0, 1011, "xid", "xid"},
		{30, "oidvector", -1, "b", "A", 26, 1013, "oidvector", "ARRAY"},
		{114, "json", -1, "b", "U", 0, 199, "json", "json"},
		{194, "pg_node_tree", -1, "b", "Z", 0, 0, "pg_node_tree", "pg_node_tree"},
		{700, "float4", 4, "b", "N", 0, 1021, "real", "real"},
		{701, "float8", 8, "b", "N", 0, 1022, "double precision", "double precision"},
		{705, "unknown", -2, "p", "X", 0, 0, "unknown", "unknown"},
		{1033, "aclitem", 12, "b", "U", 0, 1034, "aclitem", "aclitem"},
		{1042, "bpchar", -1, "b", "S", 0, 1014, "character", "character"},
		{1043, "varchar", -1, "b", "S", 0, 1015, "character varying", "character varying"},
		{1082, "date", 4, "b", "D", 0, 1182, "date", "date"},
		{1083, "time", 8, "b", "D", 0, 1183, "time without time zone", "time without time zone"},
		{1114, "timestamp", 8, "b", "D", 0, 1115, "timestamp without time zone", "timestamp without time zone"},
		{1184, "timestamptz", 8, "b", "D", 0, 1185, "timestamp with time zone", "timestamp with time zone"},
		{1186, "interval", 16, "b", "T", 0, 1187, "interval", "interval"},
		{1700, "numeric", -1, "b", "N", 0, 1231, "numeric", "numeric"},
		{2205, "regclass", 4, "b", "N", 0, 2210, "regclass", "regclass"},
		{2206, "regtype", 4, "b", "N", 0, 2211, "regtype", "regtype"},
		{2249, "record", -1, "p", "P", 0, 2287, "record", "record"},
		{2276, "any", 4, "p", "P", 0, 0, `"any"`, `"any"`},
		{2278, "void", 4, "p", "P", 0, 0, "void", "void"},
		{2279, "trigger", 4, "p", "P", 0, 0, "trigger", "trigger"},
		{2950, "uuid", 16, "b", "U", 0, 2951, "uuid", "uuid"},
		{3802, "jsonb", -1, "b", "U", 0, 3807, "jsonb", "jsonb"},
	}
	types := append([]pgType(nil), base...)
	for _, element := range base {
		if element.array == 0 {
			continue
		}
		types = append(types, pgType{element.array, "_" + element.name, -1, "b", "A", element.oid, 0,
			element.format + "[]", "ARRAY"})
	}
	return types
}()

var pgTypesByOID, pgTypesByName = func() (map[uint32]pgType, map[string]pgType) {
	byOID := make(map[uint32]pgType, len(pgTypes))
	byName := make(map[string]pgType, len(pgTypes))
	for _, typ := range pgTypes {
		byOID[typ.oid] = typ
		byName[typ.name] = typ
	}
	return byOID, byName
}()

// formatType is PostgreSQL's format_type(oid, typmod): the SQL name of a type, with its modifier.
func formatType(oid int64, modifier int64, hasModifier bool) any {
	typ, known := pgTypesByOID[uint32(oid)]
	if !known || oid <= 0 || oid > int64(^uint32(0)) {
		return "???"
	}
	name := typ.format
	if hasModifier && modifier >= numericHeaderSize {
		switch uint32(oid) {
		case pgwire.OIDNumeric:
			value := modifier - numericHeaderSize
			return fmt.Sprintf("numeric(%d,%d)", value>>16, value&0xffff)
		case pgwire.OIDVarchar:
			return fmt.Sprintf("character varying(%d)", modifier-numericHeaderSize)
		case pgwire.OIDBPChar:
			return fmt.Sprintf("character(%d)", modifier-numericHeaderSize)
		}
	}
	return name
}
