package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kitwork/engine/app"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
)

// KitDBCopyOptions is `kitwork migrate kitdb [--app <identity>] [--dry-run]`: one app, or every app of the
// root that holds a KitDB file, and whether to write.
type KitDBCopyOptions struct {
	App    string
	DryRun bool
}

// KitDBCopyResult is the copy of one app's KitDB files: what it did or would do, and why it stopped.
type KitDBCopyResult struct {
	Identity string
	Report   work.KitDBCopyReport
	Err      error
}

// CopyKitDB copies the KitDB files of each app of root into its SQLite files (work.Tenant.CopyKitDB): the
// app's database, application.kitdb, and its datastore(), storage.kitdb, the files they were until
// 29–30/10/2026. Each app's tenant is built as the server builds it, and never Run: no worker starts, no
// listener opens. An app's failure is its result's; the others still run.
func CopyKitDB(
	root string,
	maxEnergy uint64,
	layout work.RootLayout,
	shared *work.SharedEnvironment,
	options KitDBCopyOptions,
) ([]KitDBCopyResult, error) {
	identities, err := kitDBCopyIdentities(root, layout, options.App)
	if err != nil {
		return nil, err
	}
	if maxEnergy == 0 {
		maxEnergy = kitruntime.Limits().DefaultMaxEnergy
	}
	results := make([]KitDBCopyResult, 0, len(identities))
	for _, identity := range identities {
		appRuntime := app.NewRuntime(identity)
		tenant := work.NewAppTenantWithRuntimeLayout(root, identity, layout, appRuntime)
		tenant.MaxEnergy = maxEnergy
		tenant.SetSharedEnvironment(shared)
		report, err := tenant.CopyKitDB(work.KitDBCopyOptions{DryRun: options.DryRun})
		tenant.Close()
		appRuntime.Close()
		results = append(results, KitDBCopyResult{Identity: identity, Report: report, Err: err})
	}
	return results, nil
}

// kitDBCopyIdentities are the apps of root that hold a KitDB file to copy: the one app of a root of one app,
// or each folder of a root of many that holds .database/application/application.kitdb or
// .database/storage/storage.kitdb. A named app is that app alone, which must be there.
func kitDBCopyIdentities(root string, layout work.RootLayout, named string) ([]string, error) {
	holds := func(folder string) bool {
		for _, file := range []string{
			filepath.Join(folder, ".database", "application", "application.kitdb"),
			filepath.Join(folder, ".database", "storage", "storage.kitdb"),
		} {
			if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
				return true
			}
		}
		return false
	}
	if layout.IsSingleApp() {
		if named != "" && named != work.RootAppIdentity {
			return nil, fmt.Errorf("root %s là một app (layout %s): bỏ --app, hay viết --app %s", root, layout, work.RootAppIdentity)
		}
		if !holds(root) {
			return nil, nil
		}
		return []string{work.RootAppIdentity}, nil
	}
	if named != "" {
		folder := filepath.Join(root, named)
		if info, err := os.Stat(folder); err != nil || !info.IsDir() || named == "." || named == ".." ||
			filepath.Base(filepath.Clean(named)) != named {
			return nil, fmt.Errorf("không có app %s trong %s", named, root)
		}
		if !holds(folder) {
			return nil, nil
		}
		return []string{named}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var identities []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == work.SitesDirName {
			continue
		}
		if holds(filepath.Join(root, entry.Name())) {
			identities = append(identities, entry.Name())
		}
	}
	sort.Strings(identities)
	return identities, nil
}
