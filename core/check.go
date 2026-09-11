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
	Apps       int
	Sites      int
	Valid      int
	Programs   int
	Compatible int
	Issues     []CheckIssue
	Migrations []work.TablePlan // per-table migration preview (dry-run) for schemas declared at Run
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
// cron scheduler.
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
	if maxEnergy == 0 {
		maxEnergy = kitruntime.Limits().DefaultMaxEnergy
	}
	targets, discoveryErr := discoverCheckTargets(root, layout)
	report := CheckReport{}
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
		report.Sites++

		// A malformed design token cannot fail at runtime — the browser drops the
		// declaration in silence — so this is the only place it can be caught.
		for _, issue := range checkColorTokenFormat(filepath.Dir(target.file)) {
			issue.Identity, issue.Domain = target.identity, target.domain
			report.Issues = append(report.Issues, issue)
		}
		if err := tenant.Run(); err != nil {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "prepare", Identity: target.identity,
				Domain: target.domain, File: target.file, Err: err,
			})
		} else {
			report.Valid++
			// Schemas registered during Run — preview their migration (dry-run, applies nothing).
			report.Migrations = append(report.Migrations, work.MigrationPlansFor(tenant)...)
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
		for _, issue := range work.CheckCronFiles(root, sourceIdentity) {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "cron compile", Identity: identity,
				File: issue.File, Err: issue.Err,
			})
		}
		for _, issue := range work.CheckQueueFiles(root, sourceIdentity) {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "queue compile", Identity: identity,
				File: issue.File, Err: issue.Err,
			})
		}
	}

	files, entrypointErr := profileEntrypoints(root)
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
			if artifactCache != nil {
				bytecode, compileErr = artifactCache.CompileFile(file)
			} else {
				bytecode, compileErr = compiler.CompileFile(file)
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
	if layout == work.RootLayoutSingle {
		var targets []checkTarget
		file := filepath.Join(root, work.RouterFileName)
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			targets = append(targets, checkTarget{
				identity: work.RootAppIdentity,
				domain:   "localhost",
				file:     file,
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
				file:     filepath.Join(site.Directory, work.RouterFileName),
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
		file := filepath.Join(dir, work.RouterFileName)
		if info, statErr := os.Stat(file); statErr == nil && !info.IsDir() {
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
		if layout == work.RootLayoutMultiDomain {
			add(work.RootAppIdentity, name, first)
			continue
		}
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
