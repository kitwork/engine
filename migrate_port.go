package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/work"
	"github.com/lib/pq"
)

// kitwork migrate through a database port (KITWORK_DATABASE.md "Database là gốc", slice 4, 05/11/2026). A file a
// server holds (one writer per database) cannot be opened by kitwork migrate; the server's port can change it, as it
// would for TablePlus. So a plan of a held file is given the port application.work declares (app.database), as its
// admin, and the database the port names the file by: <folder> of the host's .database/, <domain>/<folder> of an app's,
// the file's schema in the statements themselves (billing.invoices). A file no port serves keeps its plan unapplied,
// said as such.

// routeThroughPort gives each plan of a held file the way to its server's port, when there is one.
func routeThroughPort(cfg *Config, plans []*work.DatabaseMigration) {
	if len(cfg.AppDatabases) == 0 {
		return
	}
	entry := cfg.AppDatabases[0]
	for _, plan := range plans {
		if !plan.Held {
			continue
		}
		database, ok := portDatabaseOf(entry.Path, cfg.Root, cfg.RootLayout, plan.Path)
		if !ok {
			continue
		}
		address := net.JoinHostPort(portHost(entry.Host), strconv.Itoa(entry.Port))
		plan.ThroughPort(fmt.Sprintf("cổng database %s (database %s)", address, database), portSender(address, entry.User, entry.Password, database))
	}
}

// portHost is the address a client reaches a port listening on host by: the loopback for every address.
func portHost(host string) string {
	switch strings.TrimSpace(host) {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "::", "[::]":
		return "::1"
	}
	return host
}

// portDatabaseOf is the database the port names the file at path by: its folder's name in the host's .database/, or
// <domain>/<folder> in an app's, a domain of that app; ok is false for a file the port does not serve.
func portDatabaseOf(hostDatabase, root string, layout work.RootLayout, path string) (string, bool) {
	absolute := func(path string) string {
		if resolved, err := filepath.Abs(path); err == nil {
			return filepath.Clean(resolved)
		}
		return filepath.Clean(path)
	}
	folder := filepath.Dir(absolute(path))
	cluster, name := filepath.Dir(folder), strings.ToLower(filepath.Base(folder))
	if hostDatabase != "" && strings.EqualFold(cluster, absolute(hostDatabase)) {
		return name, true
	}
	if root == "" || !strings.EqualFold(filepath.Base(cluster), ".database") {
		return "", false
	}
	app := filepath.Dir(cluster)
	identity := ""
	if !layout.IsSingleApp() {
		if !strings.EqualFold(filepath.Dir(app), absolute(root)) {
			return "", false
		}
		identity = filepath.Base(app)
	} else if !strings.EqualFold(app, absolute(root)) {
		return "", false
	}
	entries, err := os.ReadDir(app)
	if err != nil {
		return "", false
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		domain := strings.ToLower(entry.Name())
		if !entry.IsDir() || strings.HasPrefix(domain, ".") || strings.HasPrefix(domain, "_") {
			continue
		}
		if !layout.IsSingleApp() {
			if found, err := work.ResolveIdentityForLayout(root, domain, layout); err != nil || found != identity {
				continue
			}
		}
		return domain + "/" + name, true
	}
	return "", false
}

// portSender sends statements to the port at address, as user, on database, one by one: a schema statement through
// the port commits by itself (KITWORK_KITDB.md câu 13.5), so a failure leaves the statements before it done.
func portSender(address, user, password, database string) func(context.Context, []string) error {
	return func(ctx context.Context, statements []string) error {
		target := url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: address, Path: "/" + database,
			RawQuery: "sslmode=disable&application_name=" + url.QueryEscape("kitwork migrate")}
		connector, err := pq.NewConnector(target.String())
		if err != nil {
			return err
		}
		client := sql.OpenDB(connector)
		defer client.Close()
		client.SetMaxOpenConns(1)
		for _, statement := range statements {
			if _, err := client.ExecContext(ctx, statement); err != nil {
				first, _, _ := strings.Cut(statement, "\n")
				return fmt.Errorf("%s: %w", strings.TrimSuffix(first, " ("), err)
			}
		}
		return nil
	}
}
