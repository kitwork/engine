package compiler

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kitwork/engine/database/statement"
)

// A database of an app is declared in its .shared/schema/ (KITWORK_DATABASE.md mục 1): a file-level const
// whose value is connection().tables({ … }), exported, imported by the files that use it with
// import { db } from "app/.shared/schema/shop.work". The bundler reads each schema file it bundles, and
// finds the tables and columns in its table({ … }) literals; the resolver then translates every
// statement written on an imported handle (db.list((orders) => …)) into one call of the handle with a
// template (database/statement), checking each name against what the bundler read. The lambdas of a
// statement are never compiled.

// DatabaseBundle is what the bundler found for the resolver: which bindings are database handles, and
// which module functions are schema files.
type DatabaseBundle struct {
	// Imports maps the variable an import of a schema file's database declares (the db of
	// const db = __kw_mod_1.db) to the database it names.
	Imports map[*Identifier]*DatabaseImport
	// SchemaModules maps the function a schema file is wrapped in to its name from the app's folder.
	SchemaModules map[*FunctionLiteral]string
}

// DatabaseImport is one import of a database: the schema file, the name it exports the database under,
// and the tables the bundler read.
type DatabaseImport struct {
	// Module is the schema file from the app's folder (.shared/schema/shop.work).
	Module string
	// Export is the name the file exports the database under (db).
	Export   string
	Database *statement.Database
	// Transaction marks the handle db.transaction((database) => …) gives its function: its statements run inside
	// the transaction, and it begins none of its own.
	Transaction bool
}

// SchemaCatalog reads the databases a schema file declares, by the name each is exported under, as the
// bundler reads them. appRoot is the app's folder; the file must lie in its .shared/schema/.
func SchemaCatalog(appRoot, path string) (map[string]*statement.Database, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	root, err := filepath.Abs(appRoot)
	if err != nil {
		root = appRoot
	}
	b := &bundler{sources: map[string]string{}, debugRoot: filepath.Dir(absolute), appRoot: root}
	if _, ok := b.schemaModule(absolute); !ok {
		return nil, fmt.Errorf("%s không nằm trong .shared/schema/ của app %s", path, appRoot)
	}
	source, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	name := b.sourceName(absolute)
	b.sources[name] = string(source)
	program, err := parseProgramSource(string(source), name)
	if err != nil {
		return nil, err
	}
	return readSchemaModule(program, b.place)
}
