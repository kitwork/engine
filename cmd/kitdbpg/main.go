// Command kitdbpg serves one KitDB file or a bounded directory-backed database
// node through the PostgreSQL wire profile. It has no Kitwork tenant, VM or
// app-folder dependency; Kitwork is one client of the same catalog and format.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/relational"
)

func main() {
	file := flag.String("file", "app.kitdb", "KitDB file to open or create")
	root := flag.String("root", "", "serve every non-hidden .kitdb file immediately below this directory")
	database := flag.String("database", "", "logical PostgreSQL database name; defaults to the file name")
	maintenanceDatabase := flag.String("maintenance-database", "kitdb", "logical maintenance database used by node mode")
	user := flag.String("user", "kitdb", "PostgreSQL login user")
	password := flag.String("password", os.Getenv("KITDB_TOKEN"), "PostgreSQL password; defaults to KITDB_TOKEN")
	readOnly := flag.Bool("readonly", false, "reject SQL writes")
	verifyOnOpen := flag.Bool("verify-on-open", false, "verify every active storage page before serving")
	retainHistory := flag.Bool("retain-history", false, "retain checkpointed WAL history for recovery")
	maximumResultRows := flag.Int("max-result-rows", relational.DefaultMaximumResultRows, "maximum rows materialized by one query")
	maximumMutationRows := flag.Int("max-mutation-rows", relational.DefaultMaximumMutationRows, "maximum rows changed by one UPDATE or DELETE")
	searchRoot := flag.String("search-root", "", "search projection directory; defaults beside the KitDB file")
	searchNamespace := flag.String("search-namespace", "", "stable search projection namespace; defaults from the database identity")
	maximumSearchResults := flag.Int("max-search-results", 0, "maximum ranked rows returned by one SEARCH; default is min(max-result-rows, 10000)")
	maximumSearchCandidates := flag.Int("max-search-candidates", relational.DefaultMaximumSearchCandidates, "maximum source candidates inspected by one filtered SEARCH")
	searchForegroundWait := flag.Duration("search-foreground-wait", relational.DefaultSearchForegroundWait, "time a query waits for a projection build before retrying later")
	maximumDiscoveredDatabases := flag.Int("max-discovered-databases", relational.DefaultMaximumDiscoveredDatabases, "maximum .kitdb files exposed by node discovery")
	maximumOpenDatabases := flag.Int("max-open-databases", 64, "maximum lazily opened databases in node mode")
	maximumPageCacheBytes := flag.Int64("max-page-cache-bytes", 256<<20, "fleet page-cache reservation ceiling in node mode")
	databasePageCacheBytes := flag.Int64("database-page-cache-bytes", 1<<20, "page-cache reservation for each opened database in node mode")
	maximumConcurrentOpens := flag.Int("max-concurrent-opens", 4, "maximum concurrent database opens in node mode")
	databaseAcquireTimeout := flag.Duration("database-acquire-timeout", relational.DefaultDatabaseAcquireTimeout, "maximum wait for a busy database slot in node mode")
	listen := flag.String("listen", "127.0.0.1:5433", "loopback TCP listen address")
	logQueries := flag.Bool("log-queries", false, "log SQL text and parameter counts")
	maxConnections := flag.Int("max-connections", 64, "maximum concurrent PostgreSQL connections")
	idleTimeout := flag.Duration("idle-timeout", 30*time.Minute, "maximum idle PostgreSQL connection lifetime")
	queryTimeout := flag.Duration("query-timeout", 30*time.Second, "maximum lifetime of one PostgreSQL statement")
	flag.Parse()

	if strings.TrimSpace(*password) == "" {
		fatalf("password is required; pass -password or set KITDB_TOKEN")
	}
	if strings.TrimSpace(*root) != "" {
		if strings.TrimSpace(*database) != "" {
			fatalf("-database selects single-file mode and cannot be combined with -root")
		}
		if strings.TrimSpace(*searchNamespace) != "" {
			fatalf("-search-namespace cannot be shared by node databases; omit it to derive one namespace per file")
		}
		runPostgresNode(postgresNodeCommandOptions{
			root: *root, maintenanceDatabase: *maintenanceDatabase,
			user: *user, password: *password, readOnly: *readOnly,
			verifyOnOpen: *verifyOnOpen, retainHistory: *retainHistory,
			maximumResultRows: *maximumResultRows, maximumMutationRows: *maximumMutationRows,
			searchRoot: *searchRoot, maximumSearchResults: *maximumSearchResults,
			maximumSearchCandidates:    *maximumSearchCandidates,
			searchForegroundWait:       *searchForegroundWait,
			maximumDiscoveredDatabases: *maximumDiscoveredDatabases,
			maximumOpenDatabases:       *maximumOpenDatabases,
			maximumPageCacheBytes:      *maximumPageCacheBytes,
			databasePageCacheBytes:     *databasePageCacheBytes,
			maximumConcurrentOpens:     *maximumConcurrentOpens,
			databaseAcquireTimeout:     *databaseAcquireTimeout,
			listen:                     *listen, logQueries: *logQueries,
			maxConnections: *maxConnections, idleTimeout: *idleTimeout,
			queryTimeout: *queryTimeout,
		})
		return
	}
	absoluteFile, err := filepath.Abs(strings.TrimSpace(*file))
	if err != nil {
		fatalf("resolve database file: %v", err)
	}
	databaseEngine, err := relational.OpenWithOptions(absoluteFile, relational.Options{
		MaximumResultRows: *maximumResultRows, MaximumMutationRows: *maximumMutationRows,
		SearchRoot: *searchRoot, SearchNamespace: *searchNamespace,
		MaximumSearchResults:    *maximumSearchResults,
		MaximumSearchCandidates: *maximumSearchCandidates,
		SearchForegroundWait:    *searchForegroundWait,
		Kernel: kitdbengine.OpenOptions{
			VerifyOnOpen:  *verifyOnOpen,
			RetainHistory: *retainHistory,
		},
	})
	if err != nil {
		fatalf("open database: %v", err)
	}
	defer databaseEngine.Close()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fatalf("listen: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logicalName := strings.TrimSpace(*database)
	if logicalName == "" {
		logicalName = strings.TrimSuffix(filepath.Base(absoluteFile), filepath.Ext(absoluteFile))
	}
	fmt.Printf("KitDB standalone PostgreSQL profile listening on %s\n", listener.Addr())
	fmt.Printf("database=%s user=%s file=%s readonly=%t\n", logicalName, *user, absoluteFile, *readOnly)
	fmt.Println("local cleartext profile: connect with sslmode=disable")
	var trace func(string, int)
	if *logQueries {
		trace = func(source string, parameterCount int) {
			fmt.Printf("[KitDB pgwire] parameters=%d sql=%q\n", parameterCount, source)
		}
	}
	if err := databaseEngine.ServePostgres(ctx, listener, relational.PostgresServerOptions{
		PostgresOptions: relational.PostgresOptions{
			Database: logicalName, User: *user, Password: *password,
			ReadOnly: *readOnly, Trace: trace,
		},
		MaxConnections: *maxConnections,
		IdleTimeout:    *idleTimeout,
		QueryTimeout:   *queryTimeout,
	}); err != nil {
		fatalf("serve: %v", err)
	}
}

type postgresNodeCommandOptions struct {
	root                       string
	maintenanceDatabase        string
	user                       string
	password                   string
	readOnly                   bool
	verifyOnOpen               bool
	retainHistory              bool
	maximumResultRows          int
	maximumMutationRows        int
	searchRoot                 string
	maximumSearchResults       int
	maximumSearchCandidates    int
	searchForegroundWait       time.Duration
	maximumDiscoveredDatabases int
	maximumOpenDatabases       int
	maximumPageCacheBytes      int64
	databasePageCacheBytes     int64
	maximumConcurrentOpens     int
	databaseAcquireTimeout     time.Duration
	listen                     string
	logQueries                 bool
	maxConnections             int
	idleTimeout                time.Duration
	queryTimeout               time.Duration
}

func runPostgresNode(options postgresNodeCommandOptions) {
	absoluteRoot, err := filepath.Abs(strings.TrimSpace(options.root))
	if err != nil {
		fatalf("resolve database root: %v", err)
	}
	var trace func(database, source string, parameterCount int)
	if options.logQueries {
		trace = func(database, source string, parameterCount int) {
			fmt.Printf("[KitDB pgwire] database=%s parameters=%d sql=%q\n", database, parameterCount, source)
		}
	}
	node, err := relational.OpenPostgresNode(relational.PostgresNodeOptions{
		Root: absoluteRoot, MaintenanceDatabase: options.maintenanceDatabase,
		User: options.user, Password: options.password,
		ReadOnly: options.readOnly, MaximumDiscoveredDatabases: options.maximumDiscoveredDatabases,
		DatabaseAcquireTimeout: options.databaseAcquireTimeout,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases:      options.maximumOpenDatabases,
			MaxPageCacheBytes:     options.maximumPageCacheBytes,
			DefaultPageCacheBytes: options.databasePageCacheBytes,
			MaxConcurrentOpens:    options.maximumConcurrentOpens,
		},
		Relational: relational.Options{
			MaximumResultRows:       options.maximumResultRows,
			MaximumMutationRows:     options.maximumMutationRows,
			SearchRoot:              options.searchRoot,
			MaximumSearchResults:    options.maximumSearchResults,
			MaximumSearchCandidates: options.maximumSearchCandidates,
			SearchForegroundWait:    options.searchForegroundWait,
			Kernel: kitdbengine.OpenOptions{
				PageCacheBytes: options.databasePageCacheBytes,
				VerifyOnOpen:   options.verifyOnOpen,
				RetainHistory:  options.retainHistory,
			},
		},
		Trace: trace,
	})
	if err != nil {
		fatalf("open database node: %v", err)
	}
	defer node.Close()
	listener, err := net.Listen("tcp", options.listen)
	if err != nil {
		fatalf("listen: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	databaseNames, err := node.Databases()
	if err != nil {
		fatalf("discover databases: %v", err)
	}
	fmt.Printf("KitDB standalone PostgreSQL node listening on %s\n", listener.Addr())
	fmt.Printf(
		"maintenance_database=%s databases=%d user=%s root=%s readonly=%t\n",
		strings.TrimSpace(options.maintenanceDatabase), len(databaseNames)-1,
		options.user, absoluteRoot, options.readOnly,
	)
	fmt.Println("local cleartext profile: connect with sslmode=disable")
	if err := node.ServePostgres(ctx, listener, relational.PostgresServerOptions{
		MaxConnections: options.maxConnections,
		IdleTimeout:    options.idleTimeout,
		QueryTimeout:   options.queryTimeout,
	}); err != nil {
		fatalf("serve node: %v", err)
	}
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "kitdbpg: "+format+"\n", arguments...)
	os.Exit(1)
}
