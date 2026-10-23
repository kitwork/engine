package file

import (
	"path"
	"strings"
)

// Kind is what read() gives for a file and what write() takes, by the name's extension (mục 4, mục 5). The
// table is the default of this version, not a look at the content: an extension proves nothing, so text(),
// bytes() and json() say exactly what to read.
type Kind int

const (
	// Binary is bytes: an image, a document, an archive, an extension the table has not, no extension.
	Binary Kind = iota
	// Text is a string of UTF-8.
	Text
	// JSON is a value read from JSON, written as JSON.
	JSON
)

var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".html": true, ".css": true, ".js": true, ".work": true, ".ui": true,
	".csv": true, ".xml": true, ".svg": true,
}

// KindOf is the kind of the file name.
func KindOf(name string) Kind {
	extension := Extension(name)
	switch {
	case extension == ".json":
		return JSON
	case textExtensions[extension]:
		return Text
	}
	return Binary
}

// Extension is the name's extension in lower case, with its dot; "" for a name without one (LICENSE,
// .env).
func Extension(name string) string {
	extension := path.Ext(name)
	if extension == name {
		return "" // a name that is only an extension (.env) has none
	}
	return strings.ToLower(extension)
}
