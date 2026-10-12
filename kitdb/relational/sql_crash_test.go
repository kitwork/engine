package relational

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

// The helper owns the file and listener. Process.Kill deliberately bypasses
// all database, node, listener, and test cleanup in this process.
func TestSQLHardCrashHelper(t *testing.T) {
	root := os.Getenv("KITDB_SQL_CRASH_CHILD")
	if root == "" {
		t.Skip("subprocess helper")
	}
	node, err := OpenPostgresNode(PostgresNodeOptions{Root: root, User: "kitdb", Password: "crash-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	address, stop := startPostgresNodeTestServer(t, node)
	defer stop()
	lease, err := node.manager.Acquire(context.Background(), filepath.Join(root, "crash.kitdb"), kitdbengine.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	fmt.Println("READY " + address)
	commands := bufio.NewScanner(os.Stdin)
	for commands.Scan() {
		if commands.Text() != "checkpoint" {
			t.Fatal("unknown command")
		}
		ticket, err := node.manager.ScheduleCheckpoint(context.Background(), lease.DB().Path(), kitdbengine.OpenOptions{}, kitdbnode.MaintenanceUrgent)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("CHECKPOINT_ISSUED")
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		fmt.Println("CHECKPOINT_DONE")
	}
	if err := commands.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLHardCrashRecovery(t *testing.T) {
	for _, mode := range []string{"acknowledged", "uncommitted", "commit-window", "checkpoint-reader-window"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "crash.kitdb")
			seed, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"CREATE TABLE items(id BIGINT PRIMARY KEY, batch BIGINT NOT NULL, payload TEXT NOT NULL)", "CREATE INDEX items_batch ON items(batch)"} {
				if _, err := seed.Execute(context.Background(), query); err != nil {
					t.Fatal(err)
				}
			}
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestSQLHardCrashHelper$", "-test.timeout=35s")
			child.Env = append(os.Environ(), "KITDB_SQL_CRASH_CHILD="+root)
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			// A file keeps helper diagnostics bounded in memory and survives kill.
			log, err := os.Create(filepath.Join(root, "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			child.Stderr = log
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				_ = stdin.Close()
				if !waited {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			scanner := bufio.NewScanner(stdout)
			readSignal := func(prefix string) string {
				t.Helper()
				if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), prefix) {
					t.Fatalf("helper signal %q: %q, %v (see %s)", prefix, scanner.Text(), scanner.Err(), log.Name())
				}
				return strings.TrimPrefix(scanner.Text(), prefix)
			}
			client := openPostgresNodeTestClient(t, readSignal("READY "), "crash", "crash-fixture")
			defer client.Close()
			journal, err := os.OpenFile(filepath.Join(root, "ack.jsonl"), os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			payload := strings.Repeat("durable", 512)
			const confirmed = 32
			for batch := 1; batch <= confirmed; batch++ {
				tx, err := client.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO items VALUES($1,$2,$3),($4,$2,$3)", batch*2-1, batch, payload, batch*2); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := json.NewEncoder(journal).Encode(batch); err != nil {
					t.Fatal(err)
				}
				if err := journal.Sync(); err != nil {
					t.Fatal(err)
				}
			}
			var commitDone chan error
			commitAcknowledged := false
			switch mode {
			case "uncommitted", "commit-window":
				tx, err := client.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(ctx, "INSERT INTO items VALUES(65,33,$1),(66,33,$1)", payload); err != nil {
					t.Fatal(err)
				}
				if mode == "commit-window" {
					commitDone = make(chan error, 1)
					started := make(chan struct{})
					go func() { close(started); commitDone <- tx.Commit() }()
					<-started
				}
			case "checkpoint-reader-window":
				reader, err := client.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Rollback()
				var count int
				if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 64 {
					t.Fatalf("reader count=%d err=%v", count, err)
				}
				if _, err := fmt.Fprintln(stdin, "checkpoint"); err != nil {
					t.Fatal(err)
				}
				readSignal("CHECKPOINT_ISSUED")
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = child.Wait()
			waited = true
			if err == nil || ctx.Err() != nil {
				t.Fatalf("expected forced process death, wait=%v context=%v", err, ctx.Err())
			}
			if commitDone != nil {
				select {
				case err := <-commitDone:
					commitAcknowledged = err == nil
				case <-ctx.Done():
					t.Fatal("COMMIT did not terminate after server kill")
				}
			}
			if commitAcknowledged {
				if err := json.NewEncoder(journal).Encode(33); err != nil {
					t.Fatal(err)
				}
				if err := journal.Sync(); err != nil {
					t.Fatal(err)
				}
			}
			_ = client.Close()
			ack, err := os.Open(journal.Name())
			if err != nil {
				t.Fatal(err)
			}
			defer ack.Close()
			decoder := json.NewDecoder(ack)
			acknowledged := confirmed
			if commitAcknowledged {
				acknowledged++
			}
			for expected := 1; expected <= acknowledged; expected++ {
				var batch int
				if err := decoder.Decode(&batch); err != nil || batch != expected {
					t.Fatalf("independent ACK journal batch=%d err=%v", batch, err)
				}
			}
			recovered, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if err := recovered.database.Verify(); err != nil {
				t.Fatal(err)
			}
			indexed, err := recovered.Execute(ctx, "EXPLAIN ANALYZE SELECT id,payload FROM items WHERE batch=1 ORDER BY id")
			if err != nil || indexed.Execution == nil || indexed.Execution.Path != "index-scan" || indexed.Execution.IndexEntriesScanned != 2 {
				t.Fatalf("recovery did not exercise the secondary index: stats=%+v err=%v", indexed.Execution, err)
			}
			pool, err := OpenNativeSQL(recovered)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			var total int
			if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&total); err != nil {
				t.Fatal(err)
			}
			if total != 64 && !(mode == "commit-window" && total == 66) {
				t.Fatalf("partial transaction or unexpected rows: %d", total)
			}
			if commitAcknowledged && total != 66 {
				t.Fatal("acknowledged in-flight COMMIT lost")
			}
			for batch := 1; batch <= total/2; batch++ {
				rows, err := pool.QueryContext(ctx, "SELECT id,payload FROM items WHERE batch=$1 ORDER BY id", batch)
				if err != nil {
					t.Fatal(err)
				}
				seen := 0
				for rows.Next() {
					var id int
					var value string
					if err := rows.Scan(&id, &value); err != nil || id != batch*2-1+seen || value != payload {
						_ = rows.Close()
						t.Fatalf("index/payload mismatch batch=%d id=%d err=%v", batch, id, err)
					}
					seen++
				}
				err = rows.Err()
				_ = rows.Close()
				if err != nil || seen != 2 {
					t.Fatalf("index batch=%d rows=%d err=%v", batch, seen, err)
				}
			}
			t.Logf("confirmed=%d recovered_rows=%d late_commit_ack=%t", confirmed, total, commitAcknowledged)
		})
	}
}
