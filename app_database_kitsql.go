package engine

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/kitwork/engine/kitdb/kitsql"
)

func newAppDatabaseKitSQLHandler(
	runtimes []*appDatabaseRuntime,
	next http.Handler,
	allowInsecureLocal bool,
) (http.Handler, error) {
	enabled := make([]*appDatabaseRuntime, 0, len(runtimes))
	maximumConcurrent := 0
	for _, runtime := range runtimes {
		if runtime == nil || !runtime.kitSQL {
			continue
		}
		enabled = append(enabled, runtime)
		capacity := runtime.concurrency
		if capacity == 0 {
			capacity = kitsql.DefaultMaximumConcurrent
		}
		maximumConcurrent += capacity
		if maximumConcurrent > 4_096 {
			maximumConcurrent = 4_096
		}
	}
	if len(enabled) == 0 {
		return next, nil
	}
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
		Next: next,
		Open: func(ctx context.Context, user, password, database string) (*sql.DB, func() error, error) {
			return resolveAppDatabaseKitSQL(ctx, enabled, user, password, database)
		},
		MaximumConcurrent:  maximumConcurrent,
		AllowInsecureLocal: allowInsecureLocal,
	})
	if err != nil {
		return nil, err
	}
	return handler, nil
}

func resolveAppDatabaseKitSQL(
	ctx context.Context,
	runtimes []*appDatabaseRuntime,
	user string,
	password string,
	database string,
) (*sql.DB, func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	credentialsMatched := false
	var selected *sql.DB
	for _, runtime := range runtimes {
		if runtime == nil || !runtime.kitSQL ||
			!constantTimeTextEqual(runtime.user, user) ||
			!constantTimeTextEqual(runtime.password, password) {
			continue
		}
		credentialsMatched = true
		var (
			candidate *sql.DB
			found     bool
			err       error
		)
		switch {
		case runtime.nativeRoot != nil:
			candidate, found, err = runtime.nativeRoot.resolve(database)
		case runtime.nativeDatabase != nil && strings.EqualFold(runtime.alias, strings.TrimSpace(database)):
			candidate, found = runtime.nativeDatabase, true
		}
		if err != nil {
			return nil, nil, err
		}
		if !found {
			continue
		}
		if selected != nil && selected != candidate {
			return nil, nil, fmt.Errorf("kitsql: database %q is ambiguous across configured roots", database)
		}
		selected = candidate
	}
	if selected != nil {
		return selected, func() error { return nil }, nil
	}
	if credentialsMatched {
		return nil, nil, kitsql.ErrDatabaseNotFound
	}
	return nil, nil, kitsql.ErrUnauthorized
}

func constantTimeTextEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}
