package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

// app.shareEnvironment (docs/KITWORK_ENVIRONMENT.md mục 2, 22/10/2026): nothing of the host's environment
// reaches an app unless application.work names the key. The manifest writes names, never values; the values
// stay where the host keeps them (the .env beside the manifest, a file file.env names, the operating system).
//
//	app.shareEnvironment(["SMTP_URL", "SENTRY_DSN"]);                  // every app receives both
//	app.shareEnvironment({ shop: ["SMTP_URL"], blog: ["SENTRY_DSN"] }); // by app, its folder in apps/
//
// A key shared that the host has not, and an app named that the host has not, stop the boot naming the key or
// the app. An app whose own .env declares a shared key wins, and the boot says so once (work/environment.go).

// shareEnvironmentShape is the error of anything but a list of keys or an object of lists by app.
const shareEnvironmentShape = `app.shareEnvironment nhận một danh sách tên khoá, app.shareEnvironment(["SMTP_URL", "SENTRY_DSN"]), hay một object theo app, app.shareEnvironment({ shop: ["SMTP_URL"] }) (KITWORK_ENVIRONMENT.md mục 2)`

// ShareEnvironment records the keys the manifest shares, for every app or by app; called twice, the lists
// add up.
func (b *ServerBuilder) ShareEnvironment(values ...value.Value) *ServerBuilder {
	if len(values) != 1 {
		b.err = shareEnvironmentShape
		return b
	}
	keys := func(list value.Value, owner string) ([]string, bool) {
		if list.K != value.Array {
			return nil, false
		}
		var names []string
		for _, item := range list.Array() {
			name := item.Text()
			if item.K != value.String || name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "= ") {
				b.err = fmt.Sprintf("app.shareEnvironment%s: %q không phải tên khoá; tên khoá viết là chuỗi, như \"SMTP_URL\" (KITWORK_ENVIRONMENT.md mục 2)", owner, item.Text())
				return nil, true
			}
			names = append(names, name)
		}
		return names, true
	}
	given := values[0]
	switch {
	case given.K == value.Array:
		names, ok := keys(given, "")
		if !ok {
			b.err = shareEnvironmentShape
		}
		if b.err == "" {
			b.shareAll = append(b.shareAll, names...)
		}
	case given.IsMap():
		apps := make([]string, 0, len(given.Map()))
		for app := range given.Map() {
			apps = append(apps, app)
		}
		sort.Strings(apps)
		for _, app := range apps {
			if strings.TrimSpace(app) == "" || strings.ContainsAny(app, `/\`) || app == "." || app == ".." {
				b.err = fmt.Sprintf("app.shareEnvironment: %q không phải tên app; tên app là tên thư mục của nó trong apps/ (KITWORK_ENVIRONMENT.md mục 2)", app)
				return b
			}
			names, ok := keys(given.Map()[app], "({ "+app+": … })")
			if !ok {
				b.err = shareEnvironmentShape
			}
			if b.err != "" {
				return b
			}
			if b.shareApps == nil {
				b.shareApps = map[string][]string{}
			}
			b.shareApps[app] = append(b.shareApps[app], names...)
		}
	default:
		b.err = shareEnvironmentShape
	}
	return b
}

// resolveSharedEnvironment takes the values of the keys shared from the host's: a key the host has not, or
// has empty, stops the boot, by name only.
func (b *ServerBuilder) resolveSharedEnvironment(host map[string]work.EnvironmentEntry) error {
	if len(b.shareAll) == 0 && len(b.shareApps) == 0 {
		return nil
	}
	var missing []string
	take := func(names []string) map[string]work.EnvironmentEntry {
		taken := make(map[string]work.EnvironmentEntry, len(names))
		for _, name := range names {
			entry, ok := host[name]
			if !ok || !entry.Present() {
				missing = append(missing, name)
				continue
			}
			taken[name] = entry
		}
		return taken
	}
	shared := &work.SharedEnvironment{All: take(b.shareAll), Apps: map[string]map[string]work.EnvironmentEntry{}}
	for app, names := range b.shareApps {
		shared.Apps[app] = take(names)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		missing = compactNames(missing)
		return fmt.Errorf("app.shareEnvironment: host không có %s; khai trong .env cạnh application.work, file file.env nêu, hay biến môi trường của hệ điều hành (KITWORK_ENVIRONMENT.md mục 2)", strings.Join(missing, ", "))
	}
	b.sharedEnvironment = shared
	return nil
}

// compactNames drops the repeats of a sorted list.
func compactNames(names []string) []string {
	out := names[:0]
	for index, name := range names {
		if index == 0 || name != names[index-1] {
			out = append(out, name)
		}
	}
	return out
}

// validateSharedApps refuses an app app.shareEnvironment names that the host's root does not hold: a folder
// of apps/ for many apps, "app" for the one app of app/.
func validateSharedApps(shared *work.SharedEnvironment, root string, layout work.RootLayout) error {
	if shared == nil {
		return nil
	}
	apps := make([]string, 0, len(shared.Apps))
	for app := range shared.Apps {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	for _, app := range apps {
		if layout.IsSingleApp() {
			if app != work.RootAppIdentity {
				return fmt.Errorf("app.shareEnvironment: không có app `%s`; host này chạy một app, tên nó là %q (KITWORK_ENVIRONMENT.md mục 2)", app, work.RootAppIdentity)
			}
			continue
		}
		if info, err := os.Stat(filepath.Join(root, app)); err != nil || !info.IsDir() {
			return fmt.Errorf("app.shareEnvironment: không có app `%s` (thư mục %s); tên app là tên thư mục của nó (KITWORK_ENVIRONMENT.md mục 2)", app, filepath.ToSlash(filepath.Join(filepath.Base(root), app)))
		}
	}
	return nil
}
