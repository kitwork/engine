package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
)

// CheckIssue is one preparation failure found without starting the host.
type CheckIssue struct {
	Stage    string
	Identity string
	Domain   string
	File     string
	Err      error
}

func (i CheckIssue) Error() string {
	scope := i.Domain
	if scope == "" {
		scope = i.Identity
	}
	if scope == "" {
		scope = "host"
	}
	location := ""
	if i.File != "" {
		location = " (" + i.File + ")"
	}
	return fmt.Sprintf("%s: %s%s: %v", scope, i.Stage, location, i.Err)
}

// CheckReport contains every issue found during one preflight pass.
type CheckReport struct {
	Root       string
	Layout     string
	Apps       int
	Sites      int
	Valid      int
	Programs   int
	Compatible int
	Issues     []CheckIssue
	// Databases are the apps' databases held against their files, reading only (KITWORK_DATABASE.md "Database là
	// gốc", slice 4): what a database lacks or has otherwise is an issue too, what kitwork migrate writes besides is not.
	Databases []*work.DatabaseMigration
}

func (r CheckReport) OK() bool {
	return len(r.Issues) == 0
}

type checkTarget struct {
	identity string
	domain   string
	file     string
}

// Check prepares every discovered site through the same Tenant.Run production
// path, then retires it without activation. It opens no listener and starts no
// schedule or queue.
func Check(root string, maxEnergy uint64, bytecodeCacheDirectory ...string) CheckReport {
	return CheckWithLayout(
		root,
		maxEnergy,
		work.RootLayoutAuto,
		bytecodeCacheDirectory...,
	)
}

// CheckWithLayout prepares only the source shape selected by host bootstrap.
// Check remains the compatibility entrypoint for direct engine users.
func CheckWithLayout(
	root string,
	maxEnergy uint64,
	layout work.RootLayout,
	bytecodeCacheDirectory ...string,
) CheckReport {
	return CheckWithEnvironment(root, maxEnergy, layout, nil, bytecodeCacheDirectory...)
}

// CheckWithEnvironment is CheckWithLayout with what application.work shares with apps
// (app.shareEnvironment), so a site that requires a shared key is checked as the server would boot it.
func CheckWithEnvironment(
	root string,
	maxEnergy uint64,
	layout work.RootLayout,
	shared *work.SharedEnvironment,
	bytecodeCacheDirectory ...string,
) CheckReport {
	report := CheckReport{
		Root:   root,
		Layout: layout.String(),
	}
	if maxEnergy == 0 {
		maxEnergy = kitruntime.Limits().DefaultMaxEnergy
	}
	targets, discoveryErr := discoverCheckTargets(root, layout)
	if discoveryErr != nil {
		report.Issues = append(report.Issues, CheckIssue{
			Stage: "discover",
			File:  root,
			Err:   discoveryErr,
		})
		return report
	}

	runtimes := make(map[string]*app.Runtime)
	appKey := func(identity, domain string) string {
		if layout.IsSingleApp() {
			return "root-app"
		}
		if identity != "" {
			return "app:" + identity
		}
		return "site:" + domain
	}
	identities := make(map[string]struct{})
	switch layout {
	case work.RootLayoutSingle, work.RootLayoutMultiDomain:
		identities[work.RootAppIdentity] = struct{}{}
	default:
		for _, identity := range work.DiscoverAppIdentities(root) {
			identities[identity] = struct{}{}
		}
	}

	for _, target := range targets {
		key := appKey(target.identity, target.domain)
		appRuntime := runtimes[key]
		if appRuntime == nil {
			appRuntime = app.NewRuntime(target.identity)
			runtimes[key] = appRuntime
			report.Apps++
		}
		if target.identity != "" {
			identities[target.identity] = struct{}{}
		}

		siteRuntime, err := appRuntime.Site(root, target.domain)
		if err != nil {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "site runtime", Identity: target.identity,
				Domain: target.domain, File: target.file, Err: err,
			})
			continue
		}
		generation, err := siteRuntime.PrepareGeneration()
		if err != nil {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "generation", Identity: target.identity,
				Domain: target.domain, File: target.file, Err: err,
			})
			continue
		}
		if len(bytecodeCacheDirectory) > 0 && bytecodeCacheDirectory[0] != "" {
			if err := generation.SetBytecodeCache(
				compiler.NewFileCache(bytecodeCacheDirectory[0]),
			); err != nil {
				report.Issues = append(report.Issues, CheckIssue{
					Stage: "bytecode cache", Identity: target.identity,
					Domain: target.domain, File: target.file, Err: err,
				})
				generation.Retire()
				continue
			}
		}

		tenant := work.NewTenantWithRuntimeLayout(
			root,
			target.domain,
			layout,
			appRuntime,
			siteRuntime,
			generation,
		)
		tenant.MaxEnergy = maxEnergy
		tenant.SetSharedEnvironment(shared)
		report.Sites++

		if err := tenant.Run(); err != nil {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "prepare", Identity: target.identity,
				Domain: target.domain, File: target.file, Err: err,
			})
		} else {
			report.Valid++
		}
		tenant.Close()
		appRuntime.RemoveSite(target.domain)
	}

	identityList := make([]string, 0, len(identities))
	for identity := range identities {
		identityList = append(identityList, identity)
	}
	sort.Strings(identityList)
	for _, identity := range identityList {
		key := appKey(identity, "")
		if runtimes[key] == nil {
			runtimes[key] = app.NewRuntime(identity)
			report.Apps++
		}
		sourceIdentity := identity
		if layout.IsSingleApp() {
			sourceIdentity = ""
		}
		// the app's tier of its environment: what its jobs and schedules require is held against it, as the
		// app's tenant holds a file back at boot (KITWORK_ENVIRONMENT.md mục 3)
		appEnvironment, environmentErr := work.AppEnvironment(filepath.Join(root, sourceIdentity), shared.For(identity))
		if environmentErr != nil {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "environment", Identity: identity,
				File: filepath.Join(root, sourceIdentity, ".env"), Err: environmentErr,
			})
		}
		for _, issue := range work.CheckScheduleFiles(root, sourceIdentity, appEnvironment) {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "schedule compile", Identity: identity,
				File: issue.File, Err: issue.Err,
			})
		}
		for _, issue := range work.CheckQueueFiles(root, sourceIdentity, appEnvironment) {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "queue compile", Identity: identity,
				File: issue.File, Err: issue.Err,
			})
		}
	}

	for _, identity := range work.MigrationApps(root, layout) {
		plans, err := work.CheckAppDatabases(root, identity, layout, shared)
		if err != nil {
			report.Issues = append(report.Issues, CheckIssue{Stage: "database", Identity: identity, Err: err})
			continue
		}
		report.Databases = append(report.Databases, plans...)
		for _, plan := range plans {
			if len(plan.Add) > 0 {
				report.Issues = append(report.Issues, CheckIssue{Stage: "database", Identity: identity, File: plan.Database,
					Err: fmt.Errorf("chưa có %s; chạy kitwork migrate", strings.Join(plan.Add, "; "))})
			}
			if len(plan.Differ) > 0 {
				report.Issues = append(report.Issues, CheckIssue{Stage: "database", Identity: identity, File: plan.Database,
					Err: fmt.Errorf("khác code: %s; sửa table() theo database", strings.Join(plan.Differ, "; "))})
			}
		}
	}

	files, entrypointErr := profileEntrypoints(root, layout)
	if entrypointErr != nil {
		report.Issues = append(report.Issues, CheckIssue{
			Stage: "compatibility discovery",
			File:  root,
			Err:   entrypointErr,
		})
	} else {
		var artifactCache *compiler.FileCache
		if len(bytecodeCacheDirectory) > 0 && bytecodeCacheDirectory[0] != "" {
			artifactCache = compiler.NewFileCache(bytecodeCacheDirectory[0])
		}
		for _, file := range files {
			var bytecode *compiler.Bytecode
			var compileErr error
			appRoot := profileAppRoot(root, layout, file)
			switch {
			case isBackgroundModule(file):
				// a job file or a schedule file runs as the module its importers and the loader see
				// (work/queue_load.go, work/schedule_load.go); as an entry its queue.job or schedule.<rule>
				// would stand outside its folder and never compile
				bytecode, compileErr = compiler.CompileModuleFileInApp(appRoot, file)
			case artifactCache != nil:
				bytecode, compileErr = artifactCache.CompileFileInApp(appRoot, file)
			default:
				bytecode, compileErr = compiler.CompileFileInApp(appRoot, file)
			}
			// Site preparation and app checks already report compilation
			// failures with their owning runtime context.
			if compileErr != nil {
				continue
			}
			report.Programs++
			if err := compiler.ValidateArtifact(bytecode); err != nil {
				report.Issues = append(report.Issues, CheckIssue{
					Stage: "bytecode compatibility",
					File:  profileRelativePath(root, file),
					Err:   err,
				})
				continue
			}
			report.Compatible++
		}
	}

	for _, appRuntime := range runtimes {
		appRuntime.Close()
	}
	return report
}

func discoverCheckTargets(root string, layouts ...work.RootLayout) ([]checkTarget, error) {
	layout := work.RootLayoutAuto
	if len(layouts) > 0 {
		layout = layouts[0]
	}
	// A site is marked by router.kitwork.js or, written in Kitwork UI, by router.work (work.RouterMarker);
	// looking for the first name only, as this did until 15/10/2026, reported "ok" for a tree of .ui
	// sites it never opened.
	if layout == work.RootLayoutSingle {
		var targets []checkTarget
		if file := work.RouterMarker(root); file != "" {
			targets = append(targets, checkTarget{
				identity: work.RootAppIdentity,
				domain:   "localhost",
				file:     file,
			})
		}
		return targets, nil
	}
	if layout == work.RootLayoutMultiDomain {
		sites, err := work.DiscoverAppSites(root)
		if err != nil {
			return nil, err
		}
		targets := make([]checkTarget, 0, len(sites))
		for _, site := range sites {
			targets = append(targets, checkTarget{
				identity: work.RootAppIdentity,
				domain:   site.Domain,
				file:     siteRouterFile(site.Directory),
			})
		}
		return targets, nil
	}
	if layout == work.RootLayoutMultiTenant {
		sites, err := work.DiscoverTenantSites(root)
		if err != nil {
			return nil, err
		}
		targets := make([]checkTarget, 0, len(sites))
		for _, site := range sites {
			targets = append(targets, checkTarget{
				identity: site.Identity,
				domain:   site.Domain,
				file:     siteRouterFile(site.Directory),
			})
		}
		return targets, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var targets []checkTarget
	add := func(identity, domain, dir string) {
		if file := work.RouterMarker(dir); file != "" {
			targets = append(targets, checkTarget{
				identity: identity,
				domain:   domain,
				file:     file,
			})
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		first := filepath.Join(root, name)
		if name == work.SitesDirName {
			children, readErr := os.ReadDir(first)
			if readErr != nil {
				return nil, readErr
			}
			for _, child := range children {
				if child.IsDir() {
					add("", child.Name(), filepath.Join(first, child.Name()))
				}
			}
			continue
		}
		if name == "test" {
			continue
		}

		before := len(targets)
		add("", name, first)
		if len(targets) != before {
			continue
		}
		children, readErr := os.ReadDir(first)
		if readErr != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() || strings.HasPrefix(child.Name(), ".") ||
				strings.HasPrefix(child.Name(), "_") {
				continue
			}
			add(name, child.Name(), filepath.Join(first, child.Name()))
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].identity != targets[j].identity {
			return targets[i].identity < targets[j].identity
		}
		return targets[i].domain < targets[j].domain
	})
	return targets, nil
}

// siteRouterFile is the router that marks a discovered site: router.kitwork.js, or router.work for
// a Kitwork UI site. Discovery found the marker a moment ago, so the fallback only names the file an
// issue would point at if it vanished in between.
func siteRouterFile(directory string) string {
	if marker := work.RouterMarker(directory); marker != "" {
		return marker
	}
	return filepath.Join(directory, work.RouterFileName)
}
