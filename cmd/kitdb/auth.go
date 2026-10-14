package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kitwork/engine/kitdb/managed"
)

// Operator commands require exclusive offline root ownership. Passwords come
// from the environment rather than command-line arguments or generated output.
func runRootAuth(command string, args []string) (result any, err error) {
	flags := newFlagSet(command)
	role := flags.String("role", "reader", "admin, reader, writer (DML), or developer (DML and DDL)")
	permissions := flags.String("permissions", "", "explicit comma-separated connect,read,write,schema; alternative to -role")
	databases := flags.String("databases", "", "comma-separated registered database names")
	disabled := flags.Bool("disabled", false, "disable this account")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if command != "auth-user" && flags.NFlag() != 0 {
		return nil, fmt.Errorf("%s does not accept account flags", command)
	}
	want := 2
	if command == "auth-list" {
		want = 1
	}
	if flags.NArg() != want {
		arguments := "ROOT USER"
		if want == 1 {
			arguments = "ROOT"
		}
		return nil, fmt.Errorf("%s requires %s (offline)", command, arguments)
	}
	root, err := managed.Open(flags.Arg(0))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if command == "auth-list" {
		return root.Users(), nil
	}
	password := os.Getenv("KITDB_AUTH_PASSWORD")
	if command == "auth-init" {
		if err := root.EnableAuth(flags.Arg(1), password); err != nil {
			return nil, err
		}
	} else {
		info := managed.UserInfo{Name: flags.Arg(1), Disabled: *disabled}
		explicitRole := false
		flags.Visit(func(flag *flag.Flag) {
			if flag.Name == "role" {
				explicitRole = true
			}
		})
		if *permissions != "" && explicitRole {
			return nil, fmt.Errorf("-permissions and -role are mutually exclusive")
		}
		if *role == "admin" {
			info.Admin = true
		} else {
			if *role != "reader" && *role != "writer" && *role != "developer" {
				return nil, fmt.Errorf("role must be admin, reader, writer or developer")
			}
			if *databases == "" {
				return nil, fmt.Errorf("reader/writer requires -databases")
			}
			for _, database := range strings.Split(*databases, ",") {
				grant := managed.DatabaseGrant{Database: strings.TrimSpace(database), Role: *role}
				if *permissions != "" {
					grant.Role = ""
					for _, permission := range strings.Split(*permissions, ",") {
						grant.Permissions = append(grant.Permissions, strings.TrimSpace(permission))
					}
				}
				info.Grants = append(info.Grants, grant)
			}
		}
		if info.Admin && *databases != "" {
			return nil, fmt.Errorf("admin does not take per-database grants")
		}
		if err := root.SetUser(info, password); err != nil {
			return nil, err
		}
	}
	return root.Users(), nil
}
