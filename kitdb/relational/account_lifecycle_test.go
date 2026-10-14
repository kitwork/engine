package relational

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/kitwork/engine/kitdb/managed"
)

func TestSQLAccountLifecycleHardExitRecovery(t *testing.T) {
	const adminPassword = "crash-admin-password"
	const oldPassword = "crash-original-password"
	const newPassword = "crash-rotated-password"
	if path := os.Getenv("KITDB_SQL_ACCOUNT_CRASH_ROOT"); path != "" {
		node, err := OpenPostgresNode(PostgresNodeOptions{Root: path})
		if err != nil {
			t.Fatal(err)
		}
		pool, err := node.OpenAuthenticatedDatabase(context.Background(), "admin", adminPassword, "shop")
		if err != nil {
			t.Fatal(err)
		}
		var source string
		var args []any
		switch os.Getenv("KITDB_SQL_ACCOUNT_CRASH_STAGE") {
		case "create":
			source = "CREATE USER created WITH PASSWORD $1"
			args = []any{newPassword}
		case "password":
			source = "ALTER USER subject WITH PASSWORD $1"
			args = []any{newPassword}
		case "nologin":
			source = "ALTER USER subject NOLOGIN"
		case "login":
			source = "ALTER USER subject WITH PASSWORD $1 LOGIN"
			args = []any{newPassword}
		case "drop":
			source = "DROP USER subject"
		default:
			t.Fatal("invalid crash stage")
		}
		if _, err := pool.Exec(source, args...); err != nil {
			t.Fatal(err)
		}
		os.Exit(73) // ACK observed: no pool/node/root Close or checkpoint.
	}
	for _, stage := range []string{"create", "password", "nologin", "login", "drop"} {
		t.Run(stage, func(t *testing.T) {
			path := createManagedNodeRoot(t, "shop")
			root, err := managed.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := root.EnableAuth("admin", adminPassword); err != nil {
				t.Fatal(err)
			}
			if err := root.SetUser(managed.UserInfo{Name: "subject", Disabled: stage == "login", Grants: []managed.DatabaseGrant{{Database: "shop", Role: "reader"}}}, oldPassword); err != nil {
				t.Fatal(err)
			}
			if err := root.Close(); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestSQLAccountLifecycleHardExitRecovery$")
			command.Env = append(os.Environ(), "KITDB_SQL_ACCOUNT_CRASH_ROOT="+path, "KITDB_SQL_ACCOUNT_CRASH_STAGE="+stage)
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("child: %v/%s", err, output)
			}
			root, err = managed.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx := context.Background()
			switch stage {
			case "create":
				user, err := root.Authenticate(ctx, "created", newPassword)
				if err != nil || len(user.Grants) != 0 {
					t.Fatalf("created account: %+v/%v", user, err)
				}
			case "password", "login":
				user, err := root.Authenticate(ctx, "subject", newPassword)
				if err != nil || user.Disabled || !user.Permissions("shop").Read {
					t.Fatalf("altered account: %+v/%v", user, err)
				}
				if _, err := root.Authenticate(ctx, "subject", oldPassword); !errors.Is(err, managed.ErrAuthentication) {
					t.Fatal("old password survived rotation")
				}
			case "nologin":
				if _, err := root.Authenticate(ctx, "subject", oldPassword); !errors.Is(err, managed.ErrAuthentication) {
					t.Fatal("NOLOGIN lost")
				}
				users := root.Users()
				index := slices.IndexFunc(users, func(user managed.UserInfo) bool { return user.Name == "subject" })
				if index < 0 || !users[index].Disabled || len(users[index].Grants) != 1 {
					t.Fatal("status/grants diverged")
				}
			case "drop":
				if _, err := root.Authenticate(ctx, "subject", oldPassword); !errors.Is(err, managed.ErrAuthentication) {
					t.Fatal("dropped account authenticated")
				}
				if slices.ContainsFunc(root.Users(), func(user managed.UserInfo) bool { return user.Name == "subject" }) {
					t.Fatal("dropped account/grants remained")
				}
			}
		})
	}
}
