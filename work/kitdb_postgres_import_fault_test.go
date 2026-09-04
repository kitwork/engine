package work

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	kitDBImportFaultChildEnvironment = "KITDB_IMPORT_FAULT_CHILD"
	kitDBImportFaultRootEnvironment  = "KITDB_IMPORT_FAULT_ROOT"
	kitDBImportFaultStageEnvironment = "KITDB_IMPORT_FAULT_STAGE"
	kitDBImportFaultExitCode         = 88

	kitDBImportFaultBeforePublication = "before-publication"
	kitDBImportFaultAfterWAL          = "after-wal-before-ack"
	kitDBImportFaultAfterCheckpoint   = "after-checkpoint"

	kitDBImportFaultID     = "hard-crash-import"
	kitDBImportFaultSource = "hard-crash-source"
)

var kitDBImportFaultStages = []string{
	kitDBImportFaultBeforePublication,
	kitDBImportFaultAfterWAL,
	kitDBImportFaultAfterCheckpoint,
}

func TestKitDBPostgresResumableImportHardCrashMatrix(t *testing.T) {
	for _, stage := range kitDBImportFaultStages {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			root := prepareKitDBImportFaultRoot(t)
			runKitDBImportFaultChild(t, root, stage)
			recoverKitDBImportFaultRoot(t, root, stage)
		})
	}
}

func TestKitDBPostgresResumableImportHardCrashHelper(t *testing.T) {
	if os.Getenv(kitDBImportFaultChildEnvironment) != "1" {
		return
	}
	root := os.Getenv(kitDBImportFaultRootEnvironment)
	stage := os.Getenv(kitDBImportFaultStageEnvironment)
	if root == "" || !kitDBImportKnownFaultStage(stage) {
		fmt.Fprintf(os.Stderr, "invalid import fault input root=%q stage=%q\n", root, stage)
		os.Exit(89)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(90)
	}
	entries := listServes(tenant, "kitdb")
	if len(entries) != 1 || entries[0].config.database == nil {
		fmt.Fprintf(os.Stderr, "import fault serves=%#v\n", entries)
		os.Exit(91)
	}
	session := kitDBImportFaultSession(tenant, entries[0].config.database)
	request, handled, err := session.BeginCopyIn(
		context.Background(), kitDBImportFaultCopySQL(1, false), nil,
	)
	if err != nil || !handled {
		fmt.Fprintf(os.Stderr, "begin import fault COPY handled=%t err=%v\n", handled, err)
		os.Exit(92)
	}
	if err := request.Stream.Write(
		context.Background(), []byte("crash-1\tCRASH-1\tFirst crash row\t1\n"),
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(93)
	}
	if stage == kitDBImportFaultBeforePublication {
		os.Exit(kitDBImportFaultExitCode)
	}
	result, err := request.Stream.Complete(context.Background())
	if err != nil || result.CommandTag != "COPY 1" {
		fmt.Fprintf(os.Stderr, "complete import fault COPY result=%#v err=%v\n", result, err)
		os.Exit(94)
	}
	if stage == kitDBImportFaultAfterWAL {
		// Complete has durably committed rows and KIMP. No pgwire CommandComplete
		// or client acknowledgement exists on this direct fault path.
		os.Exit(kitDBImportFaultExitCode)
	}
	managed, err := kitDBForRequest(tenant, "transactions.kitdb", nil).database()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(95)
	}
	managed.writeMu.Lock()
	_, err = managed.database.Checkpoint()
	managed.writeMu.Unlock()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(96)
	}
	// Intentionally retain the tenant, managed lease, and every deferred close.
	os.Exit(kitDBImportFaultExitCode)
}

func prepareKitDBImportFaultRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique(),
  title: text().notNull(),
  price: int().default(0)
});
const db = kitdb("transactions.kitdb", { products }, { token: "fault-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	tenant.Close()
	return root
}

func runKitDBImportFaultChild(t *testing.T, root, stage string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx, os.Args[0],
		"-test.run=^TestKitDBPostgresResumableImportHardCrashHelper$", "-test.count=1",
	)
	command.Env = append(
		os.Environ(),
		kitDBImportFaultChildEnvironment+"=1",
		kitDBImportFaultRootEnvironment+"="+root,
		kitDBImportFaultStageEnvironment+"="+stage,
	)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("import fault child %q timed out: %v\n%s", stage, ctx.Err(), output)
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != kitDBImportFaultExitCode {
		exitCode := 0
		if exitError != nil {
			exitCode = exitError.ExitCode()
		}
		t.Fatalf(
			"import fault child %q = %v (exit=%d), want exit %d\n%s",
			stage, err, exitCode, kitDBImportFaultExitCode, output,
		)
	}
}

func recoverKitDBImportFaultRoot(t *testing.T, root, stage string) {
	t.Helper()
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	entries := listServes(tenant, "kitdb")
	if len(entries) != 1 || entries[0].config.database == nil {
		t.Fatalf("recovered import fault serves=%#v", entries)
	}
	session := kitDBImportFaultSession(tenant, entries[0].config.database)
	defer session.Close()

	state, found := readKitDBImportFaultState(t, tenant)
	wantFirstCommitted := stage != kitDBImportFaultBeforePublication
	if found != wantFirstCommitted {
		t.Fatalf("stage %q recovered KIMP found=%t state=%#v", stage, found, state)
	}
	if found && (state.Chunk != 1 || state.Rows != 1 || state.Offset != 32 || state.Complete) {
		t.Fatalf("stage %q recovered KIMP=%#v", stage, state)
	}
	if count := kitDBImportFaultCount(t, session); count != boolInt(wantFirstCommitted) {
		t.Fatalf("stage %q recovered rows=%d want=%d", stage, count, boolInt(wantFirstCommitted))
	}
	if !found {
		completeKitDBImportFaultChunk(t, session, 1, false, "crash-1\tCRASH-1\tFirst crash row\t1\n")
	}
	completeKitDBImportFaultChunk(t, session, 2, true, "crash-2\tCRASH-2\tSecond crash row\t2\n")

	state, found = readKitDBImportFaultState(t, tenant)
	if !found || state.Chunk != 2 || state.Rows != 2 || state.Offset != 64 || !state.Complete {
		t.Fatalf("stage %q resumed KIMP=%#v found=%t", stage, state, found)
	}
	if count := kitDBImportFaultCount(t, session); count != 2 {
		t.Fatalf("stage %q resumed rows=%d", stage, count)
	}
	result, err := session.Execute(
		context.Background(), `SELECT id FROM products ORDER BY id`, nil,
	)
	if err != nil || len(result.Rows) != 2 || string(result.Rows[0][0].Data) != "crash-1" ||
		string(result.Rows[1][0].Data) != "crash-2" {
		t.Fatalf("stage %q resumed products=%#v err=%v", stage, result.Rows, err)
	}
}

func kitDBImportFaultSession(tenant *Tenant, database *dbProxy) *kitDBPostgresSession {
	return &kitDBPostgresSession{
		tenant: tenant, database: database, databaseName: "transactions",
		storageName: "transactions.kitdb", user: "kitdb",
	}
}

func completeKitDBImportFaultChunk(
	t *testing.T,
	session *kitDBPostgresSession,
	chunk uint64,
	complete bool,
	row string,
) {
	t.Helper()
	request, handled, err := session.BeginCopyIn(
		context.Background(), kitDBImportFaultCopySQL(chunk, complete), nil,
	)
	if err != nil || !handled {
		t.Fatalf("begin resumed chunk %d handled=%t err=%v", chunk, handled, err)
	}
	if err := request.Stream.Write(context.Background(), []byte(row)); err != nil {
		t.Fatal(err)
	}
	result, err := request.Stream.Complete(context.Background())
	if err != nil || result.CommandTag != "COPY 1" {
		t.Fatalf("complete resumed chunk %d result=%#v err=%v", chunk, result, err)
	}
}

func kitDBImportFaultCopySQL(chunk uint64, complete bool) string {
	start := (chunk - 1) * 32
	end := chunk * 32
	previous := strings.Repeat(strconv.FormatUint(chunk, 10), 64)
	checksum := strings.Repeat(strconv.FormatUint(chunk+1, 10), 64)
	return fmt.Sprintf(`COPY products (id, sku, title, price) FROM STDIN WITH (
KITDB_IMPORT '%s', KITDB_SOURCE '%s', KITDB_SOURCE_FORMAT 'csv',
KITDB_CHUNK %d, KITDB_START %d, KITDB_END %d, KITDB_ROWS 1,
KITDB_PREVIOUS '%s', KITDB_CHECKSUM '%s', KITDB_COMPLETE %t)`,
		kitDBImportFaultID, kitDBImportFaultSource, chunk, start, end,
		previous, checksum, complete,
	)
}

func readKitDBImportFaultState(t *testing.T, tenant *Tenant) (kitDBImportState, bool) {
	t.Helper()
	managed, err := kitDBForRequest(tenant, "transactions.kitdb", nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	managed.writeMu.RLock()
	defer managed.writeMu.RUnlock()
	state, found, err := loadKitDBImportState(managed.database, kitDBImportFaultID)
	if err != nil {
		t.Fatal(err)
	}
	return state, found
}

func kitDBImportFaultCount(t *testing.T, session *kitDBPostgresSession) int {
	t.Helper()
	result, err := session.Execute(context.Background(), `SELECT COUNT(*) FROM products`, nil)
	if err != nil || len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
		t.Fatalf("import fault count result=%#v err=%v", result.Rows, err)
	}
	count, err := strconv.Atoi(string(result.Rows[0][0].Data))
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func kitDBImportKnownFaultStage(stage string) bool {
	for _, candidate := range kitDBImportFaultStages {
		if stage == candidate {
			return true
		}
	}
	return false
}
