package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kitwork/engine/host"
	"github.com/kitwork/engine/logger"
)

// RunDatabase is `kitwork database` (Quốc, 08/11/2026): the database alone, as other SQL databases run, from the
// host's own application.work. It reads the manifest as Run does and opens only app.database's port: no website, no
// gateway, no proxy, no connection of app.postgresql/…, no app's code. An app's database (<domain>/<folder>) is found
// by the folders under app.root, as the port of Run finds it. It returns when a signal (Ctrl+C, SIGTERM) or the port
// stops it, the files it opened closed. A server of the same manifest takes the same port and the same files: run one
// or the other.
func RunDatabase(configFile ...string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runDatabase(ctx, nil, configFile...)
}

// runDatabase is RunDatabase until ctx ends; listening, when given, receives the address of each port once it listens.
func runDatabase(ctx context.Context, listening chan<- string, configFile ...string) error {
	cfg, file, err := commandManifest(configFile...)
	if err != nil {
		return err
	}
	if len(cfg.AppDatabases) == 0 {
		return fmt.Errorf("%s không khai app.database(…): `kitwork database` không có cổng database nào để mở", file)
	}
	if err := resolveAppDatabasePaths(cfg, file); err != nil {
		return err
	}
	if err := validateAppDatabaseConfigs(cfg.AppDatabases); err != nil {
		return err
	}
	logger.InitLogger(cfg.Logger)
	applyDatabaseManifest(cfg)

	portContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtimes, stopped, err := startAppDatabaseRuntimes(portContext, cfg.AppDatabases,
		appDatabaseApps{root: cfg.Root, layout: cfg.RootLayout})
	if err != nil {
		return err
	}
	// the banner of the database alone: the connections and proxies the manifest declares are not opened
	shown := *cfg
	shown.Databases, shown.Proxies = nil, nil
	printBanner(&shown, host.IsLocalhost(), false)
	fmt.Printf("  database only: %s opens no website, gateway, proxy or connection\n\n", file)
	for _, runtime := range runtimes {
		slog.Info("KitDB server alone", "address", runtime.listener.Addr().String())
		if listening != nil {
			listening <- runtime.listener.Addr().String()
		}
	}

	var runErr error
	select {
	case <-ctx.Done():
		slog.Info("Shutdown signal received")
	case runErr = <-stopped:
		slog.Error("KitDB server stopped", "error", runErr)
	}
	cancel()
	for index := len(runtimes) - 1; index >= 0; index-- {
		runErr = errors.Join(runErr, runtimes[index].Close())
	}
	return runErr
}
