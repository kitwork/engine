package engine

import (
	"fmt"
	"path/filepath"

	"github.com/kitwork/engine/database"
)

// manifestConnection is one connection the chain declared, kept as it was written: the boot names
// it back to the person as app.mysql("legacy"), by method and alias, not by the config map it became.
type manifestConnection struct {
	method   string // app.mysql, app.redis, app.connect …
	alias    string
	provider string // the canonical kind: mysql, redis, postgres, sqlite
}

// unlinkedConnectionWarnings names each declared connection whose driver this build does not link:
// app.redis() has no connector in any build yet, app.mysql() has none in this one. The manifest keeps
// accepting them so a manifest is portable between builds, and the boot says so once, by the method
// and alias as written, instead of the first query failing with the same words after the server is
// up — or, for the "system" alias, the boot stopping on a connect error that never named the line.
func (b *ServerBuilder) unlinkedConnectionWarnings() []string {
	var warnings []string
	for _, connection := range b.connections {
		if database.DriverLinked(connection.provider) {
			continue
		}
		kind := ""
		if connection.method != "app."+connection.provider {
			kind = " (" + connection.provider + ")"
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s(%q)%s: no driver linked in this build — the alias is declared, but nothing can open it",
			connection.method, connection.alias, kind,
		))
	}
	return warnings
}

// bootNotices is every one-line notice the manifest earned while its chain ran: the methods this
// engine does not have and the connections this build cannot open. None of them stops the boot.
func (b *ServerBuilder) bootNotices() []string {
	var notices []string
	if warning := b.unknownMethodWarning(); warning != "" {
		notices = append(notices, warning)
	}
	return append(notices, b.unlinkedConnectionWarnings()...)
}

// printBootNotices prints the manifest's notices, one line each, prefixed by the file:
// "[app] app.kitwork.js: app.x() is not a method …". Every path that runs a manifest calls it right
// after the chain ran — Run, the check and profile commands (commandConfig) and a legacy
// server.run("file.js") — because until 15/10/2026 only the legacy path did, while MissingMember's
// comment promised the line on the main boot too: a typo in app.kitwork.js was ignored in silence.
func printBootNotices(file string, b *ServerBuilder) {
	for _, notice := range b.bootNotices() {
		fmt.Printf("[app] %s: %s\n", filepath.Base(file), notice)
	}
}
