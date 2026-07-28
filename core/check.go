package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/app"
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
	Apps   int
	Sites  int
	Valid  int
	Issues []CheckIssue
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
func Check(root string, maxEnergy uint64) CheckReport {
	if maxEnergy == 0 {
		maxEnergy = 10_000_000
	}
	targets, discoveryErr := discoverCheckTargets(root)
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
		if identity != "" {
			return "app:" + identity
		}
		return "site:" + domain
	}
	identities := make(map[string]struct{})
	for _, identity := range work.DiscoverAppIdentities(root) {
		identities[identity] = struct{}{}
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

		tenant := work.NewTenantWithRuntime(
			root,
			target.domain,
			appRuntime,
			siteRuntime,
			generation,
		)
		tenant.MaxEnergy = maxEnergy
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
		for _, issue := range work.CheckCronFiles(root, identity) {
			report.Issues = append(report.Issues, CheckIssue{
				Stage: "cron compile", Identity: identity,
				File: issue.File, Err: issue.Err,
			})
		}
	}

	for _, appRuntime := range runtimes {
		appRuntime.Close()
	}
	return report
}

func discoverCheckTargets(root string) ([]checkTarget, error) {
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
