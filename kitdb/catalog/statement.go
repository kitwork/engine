package catalog

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// Actions of an account statement.
const (
	actionCreateUser   = "create user"
	actionCreateRole   = "create role"
	actionAlterUser    = "alter user"
	actionDrop         = "drop"
	actionGrantConnect = "grant connect"
	actionGrantSelect  = "grant select"
	actionGrantRole    = "grant role"
)

// statement is one account statement, parsed. Its password, when it has one, is never part of an error message.
type statement struct {
	action   string
	name     string // the user or role CREATE, ALTER and DROP name; the grantee of GRANT and REVOKE
	ifExists bool
	revoke   bool
	password *string
	login    *bool
	database string // CONNECT's database; a role's database ('' every database)
	schema   string // SELECT's schema
	table    string // SELECT's table; '' every table of the schema
	role     string // the role GRANT role and REVOKE role name
}

// tag is Postgres's command tag of the statement.
func (statement *statement) tag() string {
	switch statement.action {
	case actionCreateUser, actionCreateRole:
		return "CREATE ROLE"
	case actionAlterUser:
		return "ALTER ROLE"
	case actionDrop:
		return "DROP ROLE"
	}
	if statement.revoke {
		return "REVOKE"
	}
	return "GRANT"
}

// reservedWords are Postgres's reserved key words: none is a name unless quoted.
var reservedWords = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`all analyse analyze and any array as asc asymmetric both case cast check
		collate column constraint create current_catalog current_date current_role current_time current_timestamp
		current_user default deferrable desc distinct do else end except false fetch for foreign from grant group
		having in initially intersect into lateral leading limit localtime localtimestamp not null offset on only or
		order placing primary references returning select session_user some symmetric system_user table then to
		trailing true union unique user using variadic when where window with`) {
		reservedWords[word] = true
	}
}

// unsupportedOptions are Postgres's role options the catalog does not take.
var unsupportedOptions = map[string]bool{
	"superuser": true, "nosuperuser": true, "createdb": true, "nocreatedb": true, "createrole": true,
	"nocreaterole": true, "inherit": true, "noinherit": true, "replication": true, "noreplication": true,
	"bypassrls": true, "nobypassrls": true, "connection": true, "valid": true, "in": true, "role": true,
	"admin": true, "user": true, "sysid": true, "unencrypted": true, "rename": true, "set": true, "reset": true,
}

// otherPrivileges are Postgres's privileges besides CONNECT and SELECT: a read-only port grants none of them.
var otherPrivileges = map[string]bool{
	"insert": true, "update": true, "delete": true, "truncate": true, "references": true, "trigger": true,
	"create": true, "temporary": true, "temp": true, "execute": true, "usage": true, "all": true, "maintain": true,
}

func syntaxError(message string) error {
	return pgwire.NewError("42601", "syntax error in account statement: "+message)
}

func unsupported(message string) error {
	return pgwire.NewError("0A000", message)
}

// parseStatement parses source as one account statement. handled is false when source is not one (its first words
// are not CREATE USER, CREATE ROLE, ALTER USER, ALTER ROLE, DROP USER, DROP ROLE, GRANT or REVOKE); then err is nil.
func parseStatement(source string) (result *statement, handled bool, err error) {
	parser := &scanner{source: source}
	if parser.space() != nil {
		return nil, false, nil
	}
	first := parser.word()
	switch first {
	case "create", "alter", "drop":
		second := parser.word()
		if second != "user" && second != "role" {
			return nil, false, nil
		}
		if second == "user" {
			// CREATE USER MAPPING, ALTER USER MAPPING and DROP USER MAPPING are Postgres's foreign servers.
			mark := parser.position
			if parser.word() == "mapping" {
				if next := parser.word(); next == "for" || next == "if" {
					return nil, false, nil
				}
			}
			parser.position = mark
		}
		result = &statement{}
		switch first {
		case "create":
			err = parser.create(result, second)
		case "alter":
			err = parser.alter(result)
		default:
			err = parser.drop(result)
		}
	case "grant", "revoke":
		result = &statement{revoke: first == "revoke"}
		err = parser.grant(result)
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

func (parser *scanner) create(result *statement, kind string) error {
	name, err := parser.identifier()
	if err != nil {
		return err
	}
	if err := checkNewName(name); err != nil {
		return err
	}
	result.name = name
	if err := parser.options(result); err != nil {
		return err
	}
	if kind == "user" {
		result.action = actionCreateUser
		if result.password == nil {
			return syntaxError("CREATE USER needs PASSWORD '…'")
		}
		return nil
	}
	result.action = actionCreateRole
	if result.password != nil || (result.login != nil && *result.login) {
		return unsupported("CREATE ROLE makes a group that cannot log in; make an account with CREATE USER … WITH PASSWORD '…'")
	}
	return nil
}

func (parser *scanner) alter(result *statement) error {
	name, err := parser.identifier()
	if err != nil {
		return err
	}
	result.action, result.name = actionAlterUser, name
	if err := parser.options(result); err != nil {
		return err
	}
	if result.password == nil && result.login == nil {
		return syntaxError("ALTER USER needs PASSWORD '…', LOGIN or NOLOGIN")
	}
	return nil
}

func (parser *scanner) drop(result *statement) error {
	result.action = actionDrop
	if parser.keyword("if") {
		if !parser.keyword("exists") {
			return syntaxError("expected EXISTS after IF")
		}
		result.ifExists = true
	}
	name, err := parser.identifier()
	if err != nil {
		return err
	}
	result.name = name
	if parser.symbol(',') {
		return unsupported("drop one role at a time")
	}
	if !parser.end() {
		return syntaxError("expected the end of the statement after the name")
	}
	return nil
}

// options reads [WITH] PASSWORD '…' | ENCRYPTED PASSWORD '…' | LOGIN | NOLOGIN, each at most once, to the end.
func (parser *scanner) options(result *statement) error {
	parser.keyword("with")
	for !parser.end() {
		word := parser.word()
		switch word {
		case "encrypted":
			if !parser.keyword("password") {
				return syntaxError("expected PASSWORD after ENCRYPTED")
			}
			fallthrough
		case "password":
			if result.password != nil {
				return syntaxError("conflicting or redundant options")
			}
			if parser.keyword("null") {
				return unsupported("PASSWORD NULL is not supported: a user needs a password")
			}
			password, err := parser.literal()
			if err != nil {
				return err
			}
			result.password = &password
		case "login", "nologin":
			if result.login != nil {
				return syntaxError("conflicting or redundant options")
			}
			login := word == "login"
			result.login = &login
		case "":
			return syntaxError("expected a role option")
		default:
			if unsupportedOptions[word] {
				return unsupported("role option " + strings.ToUpper(word) + " is not supported")
			}
			return syntaxError("unknown role option " + strconv.Quote(word))
		}
	}
	return nil
}

func (parser *scanner) grant(result *statement) error {
	if result.revoke {
		mark := parser.position
		if word := parser.word(); (word == "grant" || word == "admin") && parser.keyword("option") {
			return unsupported("REVOKE … OPTION FOR is not supported: only an admin grants")
		}
		parser.position = mark
	}
	mark := parser.position
	word := parser.word()
	if otherPrivileges[word] {
		return unsupported("only CONNECT and SELECT can be granted: the port is read-only")
	}
	if (word == "connect" || word == "select") && parser.keyword("on") {
		if word == "connect" {
			if !parser.keyword("database") {
				return unsupported("CONNECT is granted ON DATABASE only")
			}
			database, err := parser.databaseName()
			if err != nil {
				return err
			}
			result.action, result.database = actionGrantConnect, database
		} else if err := parser.selectTarget(result); err != nil {
			return err
		}
	} else {
		if word == "connect" || word == "select" {
			if parser.symbol(',') {
				return unsupported("grant one privilege at a time")
			}
		}
		parser.position = mark
		role, err := parser.identifier()
		if err != nil {
			return err
		}
		result.action, result.role = actionGrantRole, role
		if parser.symbol(',') {
			return unsupported("grant one role at a time")
		}
		if parser.keyword("on") {
			if !parser.keyword("database") {
				return syntaxError("expected DATABASE after ON")
			}
			database, err := parser.databaseName()
			if err != nil {
				return err
			}
			result.database = database
		}
	}
	direction := "to"
	if result.revoke {
		direction = "from"
	}
	if !parser.keyword(direction) {
		return syntaxError("expected " + strings.ToUpper(direction))
	}
	parser.keyword("group")
	grantee, err := parser.identifier()
	if err != nil {
		return err
	}
	if grantee == "public" {
		return unsupported("granting to PUBLIC is not supported: grant to a user or a role")
	}
	result.name = grantee
	if parser.symbol(',') {
		return unsupported("grant to one user or role at a time")
	}
	if result.revoke {
		if !parser.keyword("restrict") {
			parser.keyword("cascade")
		}
	} else if parser.keyword("with") || parser.keyword("granted") {
		return unsupported("WITH … OPTION and GRANTED BY are not supported: only an admin grants")
	}
	if !parser.end() {
		return syntaxError("expected the end of the statement")
	}
	return nil
}

// selectTarget reads what SELECT is granted on: [TABLE] [schema.]table, or ALL TABLES IN SCHEMA schema.
func (parser *scanner) selectTarget(result *statement) error {
	result.action = actionGrantSelect
	if parser.keyword("all") {
		if !parser.keyword("tables") || !parser.keyword("in") || !parser.keyword("schema") {
			return syntaxError("expected ALL TABLES IN SCHEMA")
		}
		schema, err := parser.identifier()
		if err != nil {
			return err
		}
		if parser.symbol(',') {
			return unsupported("grant on one schema at a time")
		}
		result.schema = schema
		return nil
	}
	mark := parser.position
	if word := parser.word(); word == "sequence" || word == "schema" || word == "database" || word == "function" ||
		word == "procedure" || word == "routine" || word == "language" || word == "domain" || word == "type" ||
		word == "foreign" || word == "tablespace" || word == "large" || word == "parameter" {
		return unsupported("SELECT is granted on tables only")
	}
	parser.position = mark
	parser.keyword("table")
	first, err := parser.identifier()
	if err != nil {
		return err
	}
	result.schema, result.table = "public", first
	if parser.symbol('.') {
		second, err := parser.identifier()
		if err != nil {
			return err
		}
		result.schema, result.table = first, second
		if parser.symbol('.') {
			return unsupported("cross-database references are not implemented: grant on the database you are connected to")
		}
	}
	if parser.symbol(',') {
		return unsupported("grant on one table at a time")
	}
	return nil
}

// scanner reads an account statement from its text, one piece at a time.
type scanner struct {
	source   string
	position int
}

// space skips white space and comments (-- to the end of the line, and /* */, nested as Postgres nests them).
func (parser *scanner) space() error {
	for parser.position < len(parser.source) {
		rest := parser.source[parser.position:]
		switch {
		case strings.ContainsRune(" \t\n\r\f\v", rune(rest[0])):
			parser.position++
		case strings.HasPrefix(rest, "--"):
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				parser.position = len(parser.source)
			} else {
				parser.position += end + 1
			}
		case strings.HasPrefix(rest, "/*"):
			depth, index := 0, 0
			for index < len(rest) {
				if strings.HasPrefix(rest[index:], "/*") {
					depth, index = depth+1, index+2
				} else if strings.HasPrefix(rest[index:], "*/") {
					depth, index = depth-1, index+2
					if depth == 0 {
						break
					}
				} else {
					index++
				}
			}
			if depth != 0 {
				return syntaxError("unterminated /* comment")
			}
			parser.position += index
		default:
			return nil
		}
	}
	return nil
}

func identifierStart(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_' || character >= 0x80
}

func identifierPart(character byte) bool {
	return identifierStart(character) || character >= '0' && character <= '9' || character == '$'
}

// foldCase lowers ASCII letters only, as Postgres folds an unquoted name.
func foldCase(text string) string {
	return strings.Map(func(character rune) rune {
		if character >= 'A' && character <= 'Z' {
			return character + ('a' - 'A')
		}
		return character
	}, text)
}

// word reads an unquoted word, folded; "" (nothing read) when the next piece is not one.
func (parser *scanner) word() string {
	if parser.space() != nil {
		return ""
	}
	start := parser.position
	if start >= len(parser.source) || !identifierStart(parser.source[start]) {
		return ""
	}
	end := start + 1
	for end < len(parser.source) && identifierPart(parser.source[end]) {
		end++
	}
	parser.position = end
	return foldCase(parser.source[start:end])
}

// keyword reads word when it is next; otherwise it reads nothing.
func (parser *scanner) keyword(word string) bool {
	mark := parser.position
	if parser.word() == word {
		return true
	}
	parser.position = mark
	return false
}

// symbol reads character when it is next; otherwise it reads nothing.
func (parser *scanner) symbol(character byte) bool {
	mark := parser.position
	if parser.space() == nil && parser.position < len(parser.source) && parser.source[parser.position] == character {
		parser.position++
		return true
	}
	parser.position = mark
	return false
}

// end reads an optional semicolon and reports whether nothing but space follows; otherwise it reads nothing.
func (parser *scanner) end() bool {
	mark := parser.position
	parser.symbol(';')
	if parser.space() == nil && parser.position == len(parser.source) {
		return true
	}
	parser.position = mark
	return false
}

// quoted reads a "quoted" name ("" is one quote); found is false when the next piece does not start with a quote.
func (parser *scanner) quoted() (name string, found bool, err error) {
	if err := parser.space(); err != nil {
		return "", false, err
	}
	if parser.position >= len(parser.source) || parser.source[parser.position] != '"' {
		return "", false, nil
	}
	var builder strings.Builder
	for index := parser.position + 1; index < len(parser.source); index++ {
		if parser.source[index] != '"' {
			builder.WriteByte(parser.source[index])
			continue
		}
		if index+1 < len(parser.source) && parser.source[index+1] == '"' {
			builder.WriteByte('"')
			index++
			continue
		}
		parser.position = index + 1
		if builder.Len() == 0 {
			return "", true, syntaxError("zero-length delimited identifier")
		}
		return builder.String(), true, nil
	}
	return "", true, syntaxError("unterminated quoted identifier")
}

// identifier reads a user, role, schema or table name: "quoted" as it is, unquoted folded to lower case.
func (parser *scanner) identifier() (string, error) {
	name, found, err := parser.quoted()
	if err != nil {
		return "", err
	}
	if !found {
		mark := parser.position
		name = parser.word()
		if name == "" {
			return "", syntaxError("expected a name")
		}
		if reservedWords[name] {
			parser.position = mark
			return "", syntaxError("at or near " + strconv.Quote(name) + ": a reserved word is a name only when quoted")
		}
	}
	return name, checkName(name)
}

// databaseName reads a database as clients name it on the port: "quoted", or unquoted letters, digits and _ $ . - /
// folded to lower case ("shopping", demo.kitwork.io/application).
func (parser *scanner) databaseName() (string, error) {
	name, found, err := parser.quoted()
	if err != nil {
		return "", err
	}
	if !found {
		start := parser.position
		end := start
		for end < len(parser.source) {
			character := parser.source[end]
			if !identifierPart(character) && character != '.' && character != '-' && character != '/' {
				break
			}
			end++
		}
		name = foldCase(parser.source[start:end])
		if name == "" || reservedWords[name] {
			return "", syntaxError("expected a database name")
		}
		parser.position = end
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", pgwire.NewError("42602", "a database name cannot contain NUL")
	}
	if len(name) > maximumDatabaseNameBytes {
		return "", pgwire.NewError("42622", "a database name is at most 512 bytes")
	}
	return name, nil
}

// literal reads a standard string literal, two single quotes in it standing for one. Its text never reaches an
// error message.
func (parser *scanner) literal() (string, error) {
	if err := parser.space(); err != nil {
		return "", err
	}
	if parser.position >= len(parser.source) || parser.source[parser.position] != '\'' {
		return "", syntaxError("expected a string literal in single quotes")
	}
	var builder strings.Builder
	for index := parser.position + 1; index < len(parser.source); index++ {
		if parser.source[index] != '\'' {
			builder.WriteByte(parser.source[index])
			continue
		}
		if index+1 < len(parser.source) && parser.source[index+1] == '\'' {
			builder.WriteByte('\'')
			index++
			continue
		}
		parser.position = index + 1
		return builder.String(), nil
	}
	return "", syntaxError("unterminated string literal")
}

// checkName refuses a name Postgres would not keep: empty, with NUL, or over 63 bytes.
func checkName(name string) error {
	switch {
	case name == "":
		return pgwire.NewError("42602", "a name cannot be empty")
	case strings.IndexByte(name, 0) >= 0:
		return pgwire.NewError("42602", "a name cannot contain NUL")
	case len(name) > maximumIdentifierBytes:
		return pgwire.NewError("42622", "a name is at most 63 bytes")
	}
	return nil
}

// checkNewName refuses, besides checkName, the names Postgres reserves for itself: public, none, and pg_….
func checkNewName(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if name == "public" || name == "none" || strings.HasPrefix(name, "pg_") {
		return pgwire.NewError("42939", "role name "+strconv.Quote(name)+" is reserved")
	}
	return nil
}

// parseVerifier reads a SCRAM-SHA-256 verifier in Postgres's text form,
// SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey> (base64), as psql's \password sends it.
func parseVerifier(text string) (pgwire.SCRAMVerifier, bool) {
	rest, found := strings.CutPrefix(text, "SCRAM-SHA-256$")
	if !found {
		return pgwire.SCRAMVerifier{}, false
	}
	parameters, keys, found := strings.Cut(rest, "$")
	if !found {
		return pgwire.SCRAMVerifier{}, false
	}
	iterationsText, saltText, found := strings.Cut(parameters, ":")
	if !found {
		return pgwire.SCRAMVerifier{}, false
	}
	storedText, serverText, found := strings.Cut(keys, ":")
	if !found {
		return pgwire.SCRAMVerifier{}, false
	}
	iterations, err := strconv.Atoi(iterationsText)
	if err != nil || iterations < minimumIterations || iterations > maximumIterations {
		return pgwire.SCRAMVerifier{}, false
	}
	salt, saltErr := base64.StdEncoding.DecodeString(saltText)
	stored, storedErr := base64.StdEncoding.DecodeString(storedText)
	server, serverErr := base64.StdEncoding.DecodeString(serverText)
	if saltErr != nil || storedErr != nil || serverErr != nil || len(salt) < minimumSaltBytes || len(salt) > maximumSaltBytes ||
		len(stored) != len(pgwire.SCRAMVerifier{}.StoredKey) || len(server) != len(pgwire.SCRAMVerifier{}.ServerKey) {
		return pgwire.SCRAMVerifier{}, false
	}
	verifier := pgwire.SCRAMVerifier{Salt: salt, Iterations: iterations}
	copy(verifier.StoredKey[:], stored)
	copy(verifier.ServerKey[:], server)
	return verifier, true
}

// md5Hash reports whether text is a Postgres MD5 password hash ("md5" and 32 hexadecimal digits).
func md5Hash(text string) bool {
	if len(text) != 35 || !strings.HasPrefix(text, "md5") {
		return false
	}
	for _, character := range []byte(text[3:]) {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
