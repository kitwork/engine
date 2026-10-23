package work

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/kitwork/engine/compiler"
	envhelper "github.com/kitwork/engine/utilities/env"
)

// The environment of an app (docs/KITWORK_ENVIRONMENT.md, 22/10/2026): each app sees its own and nothing
// else, never the host's .env nor the operating system's.
//
//   - mục 2: the keys application.work shares with the app (app.shareEnvironment), values of the host;
//   - mục 1: the app's .env at the app's folder (app/, apps/<identity>/) over them, the app winning, said
//     once at boot;
//   - mục 1: the site's .env (<domain>/.env) over that, key by key.
//
// Both files are read through an os.Root of the app's folder: a .env reached through a link (a symlink, on
// Windows a junction too, the site's folder as one included) is refused, not read, wherever the link points. A .env in a branch of a site (shop.com/quan-ly/.env) stops
// the boot: configuration by branch is config.work's. Every environment.require("KEY") the app's code
// writes is checked at boot, once for all of them (environmentMissing).

// SharedEnvironment is what application.work shares with apps (app.shareEnvironment, mục 2): the host's
// values by key, for every app (All) and for one app by its name (Apps, by identity: the folder of
// apps/<identity>/, "app" for the one app of app/). It never changes once made.
type SharedEnvironment struct {
	All  map[string]EnvironmentEntry
	Apps map[string]map[string]EnvironmentEntry
}

// For is what an app of that identity receives: the keys shared with every app, then those named for it.
func (s *SharedEnvironment) For(identity string) map[string]EnvironmentEntry {
	if s == nil {
		return nil
	}
	shared := make(map[string]EnvironmentEntry, len(s.All)+len(s.Apps[identity]))
	for key, entry := range s.All {
		shared[key] = entry
	}
	for key, entry := range s.Apps[identity] {
		shared[key] = entry
	}
	return shared
}

// SetSharedEnvironment hands the tenant what application.work shares (core.Engine), before Run.
func (t *Tenant) SetSharedEnvironment(shared *SharedEnvironment) {
	if t != nil {
		t.sharedEnvironment = shared
	}
}

// environmentFiles are the .env files of this tenant, app first: one when the site's folder is the app's.
func (t *Tenant) environmentFiles() []string {
	app, site := t.resolveApp(".env"), t.resolve(".env")
	if filepath.Clean(app) == filepath.Clean(site) {
		return []string{app}
	}
	return []string{app, site}
}

// buildEnvironment is this tenant's environment: what the host shares, the app's .env over it, the site's
// over that. The keys of the app that override a shared one are said once.
func (t *Tenant) buildEnvironment() (map[string]EnvironmentEntry, error) {
	vars := map[string]EnvironmentEntry{}
	shared := t.sharedEnvironment.For(t.entity.Identity)
	for key, entry := range shared {
		vars[key] = entry
	}
	for index, file := range t.environmentFiles() {
		entries, err := t.readEnvironmentFile(file)
		if err != nil {
			return nil, err
		}
		for key, entry := range entries {
			if _, fromHost := shared[key]; fromHost && index == 0 {
				environmentOverride(t.entity.Identity, key)
			}
			vars[key] = entry
		}
	}
	return vars, nil
}

// environmentOverrides are the overrides said already, by app and key: a line once per process, not once
// per generation.
var environmentOverrides sync.Map

// environmentSay writes a line of the boot about the environment; a test hears it.
var environmentSay = func(line string) { fmt.Println(line) }

// environmentOverride says that an app's .env overrides a key the host shares (mục 2): the name, never a
// value.
func environmentOverride(identity, key string) {
	if _, said := environmentOverrides.LoadOrStore(identity+"\x00"+key, true); said {
		return
	}
	environmentSay(fmt.Sprintf("[environment] app `%s` đè `%s` host chia: .env của app thắng (KITWORK_ENVIRONMENT.md mục 2)", identity, key))
}

// readEnvironmentFile reads a .env of this tenant through an os.Root of the app's folder (of the site's, for
// a site that lies outside it). A missing file holds nothing; one there behind a link is an error naming the
// file, never its content.
func (t *Tenant) readEnvironmentFile(file string) (map[string]EnvironmentEntry, error) {
	return readEnvironmentUnder(t.resolveApp(), file)
}

// AppEnvironment is the app's tier of its environment (mục 1, 2): what the host shares with it, the app's .env
// over it, what its .shared/queue/ and .schedule/ read. kitwork check holds the environment.require of those
// files against it (CheckScheduleFiles, CheckQueueFiles), as the app's tenant does at boot.
func AppEnvironment(appRoot string, shared map[string]EnvironmentEntry) (map[string]EnvironmentEntry, error) {
	vars := make(map[string]EnvironmentEntry, len(shared))
	for key, entry := range shared {
		vars[key] = entry
	}
	entries, err := readEnvironmentUnder(appRoot, filepath.Join(appRoot, ".env"))
	if err != nil {
		return nil, err
	}
	for key, entry := range entries {
		vars[key] = entry
	}
	return vars, nil
}

// readEnvironmentUnder reads file, a .env, through an os.Root of the app's folder appRoot (of the file's own
// folder when it lies outside it).
func readEnvironmentUnder(appRoot, file string) (map[string]EnvironmentEntry, error) {
	base := appRoot
	relative, err := filepath.Rel(base, file)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		base = filepath.Dir(file)
		relative = filepath.Base(file)
	}
	root, err := os.OpenRoot(base)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]EnvironmentEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("environment: không mở được thư mục của app: %v", err)
	}
	defer root.Close()
	opened, err := root.Open(relative)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return map[string]EnvironmentEntry{}, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "path escapes from parent") {
			// a link on the way: os.Root follows none that is absolute, and a Windows junction always is, so
			// one that stays inside the app is refused too. Only a .env that is there behind it is an error;
			// a site's folder that is a link and holds none boots as before.
			if _, statErr := os.Stat(file); errors.Is(statErr, fs.ErrNotExist) || errors.Is(statErr, syscall.ENOTDIR) {
				return map[string]EnvironmentEntry{}, nil
			}
			return nil, fmt.Errorf("environment: %s nằm sau một liên kết (symlink, junction); Kitwork không đọc .env qua liên kết, kể cả liên kết trỏ vào trong app: đặt file thật ở thư mục của app hay của site (KITWORK_ENVIRONMENT.md mục 1)", filepath.ToSlash(relative))
		}
		return nil, fmt.Errorf("environment: không đọc được %s: %v", filepath.ToSlash(relative), err)
	}
	defer opened.Close()
	if info, err := opened.Stat(); err == nil && info.IsDir() {
		return nil, fmt.Errorf("environment: %s là một thư mục, cần một file", filepath.ToSlash(relative))
	}
	return envhelper.ParseEntries(opened), nil
}

// branchEnvironment is the error of a .env in a branch of a site (mục 1): never read, configuration by branch
// is config.work's.
func branchEnvironment(folder, relative string) error {
	info, err := os.Stat(filepath.Join(folder, ".env"))
	if err != nil || info.IsDir() {
		return nil
	}
	return fmt.Errorf("thư mục /%s: .env ở nhánh không được đọc; biến môi trường đặt ở .env của site hay của app, cấu hình theo nhánh đặt trong config.work (KITWORK_ENVIRONMENT.md mục 1)", relative)
}

// environmentMissing is the error of keys environment.require names that the app's environment has not
// (mục 3): each with where it is written, every one in one report, never a value.
type environmentMissing struct {
	uses []compiler.NameUse
}

func (e *environmentMissing) Error() string {
	return environmentMissingText(e.uses)
}

// environmentMissingText is "environment.require: thiếu STRIPE_SECRET (router.work:3:35), SMTP_URL
// (.shared/mail.work:2:30); khai trong .env của app (…)": the keys in order of name, each place a key is
// required, every key once.
func environmentMissingText(uses []compiler.NameUse) string {
	places := map[string][]string{}
	for _, use := range uses {
		place := use.Place()
		seen := false
		for _, known := range places[use.Name] {
			seen = seen || known == place
		}
		if !seen {
			places[use.Name] = append(places[use.Name], place)
		}
	}
	keys := make([]string, 0, len(places))
	for key := range places {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s (%s)", key, strings.Join(places[key], ", ")))
	}
	return fmt.Sprintf("environment.require: thiếu %s; khai trong .env của app hay của site, hay chia từ host bằng app.shareEnvironment trong application.work (KITWORK_ENVIRONMENT.md mục 3)", strings.Join(parts, ", "))
}

// placeEntry writes the entry's place from the site's folder: router.work of /quan-ly/ is quan-ly/router.work.
func (e *environmentMissing) placeEntry(base, relative string) {
	for index := range e.uses {
		if e.uses[index].File == base {
			e.uses[index].File = relative
		}
	}
}

// missingEnvironment is every key bytecode requires that this tenant's environment has not; nil when
// none is missing.
func (t *Tenant) missingEnvironment(bytecode *compiler.Bytecode) *environmentMissing {
	vars, _ := t.env.V.(*envVars)
	return environmentMissingFrom(bytecode, func(key string) bool { return vars != nil && vars.has(key) })
}

// environmentMissingFrom is every key bytecode requires that has does not find; nil when none is missing.
func environmentMissingFrom(bytecode *compiler.Bytecode, has func(key string) bool) *environmentMissing {
	if bytecode == nil || len(bytecode.EnvironmentRequires) == 0 {
		return nil
	}
	var missing []compiler.NameUse
	for _, use := range bytecode.EnvironmentRequires {
		if !has(use.Name) {
			missing = append(missing, use)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &environmentMissing{uses: missing}
}
