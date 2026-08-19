package work

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/value"
)

// JitComponent declares one generation-managed tenant component. The source
// path is relative to the router.kitwork.js folder that makes the declaration.
// It snapshots browser JavaScript during preparation; request handling never
// reads tenant component source from disk.
//
// Deprecated: declare components in the root router.jitjs manifest. This
// compatibility method deliberately delegates to the same confined loader.
//
//	router.jitComponent("counter", "1.0.0", "components/counter.js")
func (f *FolderRouter) JitComponent(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 3 {
		return f, fmt.Errorf("router.jitComponent expects exactly three string arguments: name, exactVersion, relativeSourcePath")
	}
	for index, argument := range args {
		if argument.K != value.String {
			return f, fmt.Errorf("router.jitComponent argument %d must be a string; got %s", index+1, argument.K)
		}
	}
	name := args[0].Text()
	version := args[1].Text()
	relativeSource := args[2].Text()
	if f == nil || f.tenant == nil || f.node == nil {
		return f, fmt.Errorf("router.jitComponent requires a pending site generation")
	}
	if err := f.addJITComponent("router.jitComponent", name, version, relativeSource, f.node.diskPath()); err != nil {
		return f, err
	}
	return f, nil
}

// addJITComponent validates, snapshots, and watches one tenant browser
// component. Both the canonical root jitjs manifest and the deprecated
// per-router declaration use this path so confinement and hot reload cannot
// drift between the two APIs.
func (f *FolderRouter) addJITComponent(label, name, version, relativeSource, sourceDirectory string) error {
	if name == "" || name != strings.TrimSpace(name) {
		return fmt.Errorf("%s name must be a non-empty canonical component name", label)
	}
	if !kitjavascript.ValidStagedComponentName(name) {
		return fmt.Errorf("%s name %q cannot be represented by a staged asset suffix (maximum %d bytes)", label,
			name, kitjavascript.MaxStagedPackageSuffixBytes)
	}
	if !kitjavascript.ValidExactSemVer(version) {
		return fmt.Errorf("%s version must be an exact SemVer; got %q", label, version)
	}
	if relativeSource == "" || relativeSource != strings.TrimSpace(relativeSource) || strings.IndexByte(relativeSource, 0) >= 0 {
		return fmt.Errorf("%s source path must be a non-empty relative path", label)
	}
	if filepath.IsAbs(relativeSource) || filepath.VolumeName(relativeSource) != "" ||
		strings.HasPrefix(relativeSource, "/") || strings.HasPrefix(relativeSource, `\`) {
		return fmt.Errorf("%s source path %q must be relative to its router folder", label, relativeSource)
	}
	if f == nil || f.tenant == nil || f.tenant.generation == nil || sourceDirectory == "" {
		return fmt.Errorf("%s requires a pending site generation", label)
	}

	declared, err := filepath.Abs(filepath.Join(sourceDirectory, filepath.FromSlash(relativeSource)))
	if err != nil {
		return fmt.Errorf("resolve %s source %q: %w", label, relativeSource, err)
	}
	resolved, err := filepath.EvalSymlinks(declared)
	if err != nil {
		return fmt.Errorf("resolve %s source %q: %w", label, relativeSource, err)
	}
	if !f.tenant.insideSiteRoot(resolved) {
		return fmt.Errorf("%s source %q escapes the tenant site", label, relativeSource)
	}
	if !strings.EqualFold(filepath.Ext(resolved), ".js") {
		return fmt.Errorf("%s source %q must be a .js file", label, relativeSource)
	}

	rawSource, sourceSnapshot, err := readJITComponentSource(f.tenant, declared, resolved)
	if err != nil {
		return fmt.Errorf("read %s source %q: %w", label, relativeSource, err)
	}
	javaScript, err := canonicalJITComponentJavaScript(rawSource)
	if err != nil {
		return fmt.Errorf("prepare %s source %q: %w", label, relativeSource, err)
	}
	if !sourceSnapshot.unchanged(f.tenant, declared) {
		return fmt.Errorf("%s source %q changed while it was being prepared", label, relativeSource)
	}

	manifest := f.tenant.generation.Sources()
	if err := manifest.WatchConfinedFileContent(declared, f.tenant.resolve(), sourceSnapshot.resolved, rawSource); err != nil {
		return err
	}
	if err := f.tenant.presentation().AddJITComponent(site.JITComponentSource{
		Name:       name,
		Version:    version,
		Filename:   sourceSnapshot.resolved,
		JavaScript: javaScript,
	}); err != nil {
		return fmt.Errorf("%s %q: %w", label, name, err)
	}
	return nil
}

// canonicalJITComponentJavaScript converts an authored browser file to the
// exact classic-script package bytes that will be hashed and executed. Raw
// bytes remain in SourceManifest solely as the hot-reload identity.
func canonicalJITComponentJavaScript(source []byte) ([]byte, error) {
	if len(bytes.TrimSpace(source)) == 0 {
		return nil, fmt.Errorf("must contain JavaScript")
	}
	if bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) || !utf8.Valid(source) {
		return nil, fmt.Errorf("must be UTF-8 without BOM")
	}
	canonical := bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	if bytes.IndexByte(canonical, '\r') >= 0 {
		return nil, fmt.Errorf("must use LF or CRLF line endings")
	}
	additional := 0
	if canonical[0] != ';' {
		additional++
	}
	if canonical[len(canonical)-1] != '\n' {
		additional++
	}
	capacity := len(canonical) + additional
	if capacity > site.MaxJITComponentSourceBytes {
		return nil, fmt.Errorf("canonical JavaScript exceeds %d-byte limit", site.MaxJITComponentSourceBytes)
	}
	output := make([]byte, 0, capacity)
	if canonical[0] != ';' {
		output = append(output, ';')
	}
	output = append(output, canonical...)
	if output[len(output)-1] != '\n' {
		output = append(output, '\n')
	}
	return output, nil
}

type jitComponentSourceSnapshot struct {
	root     string
	relative string
	resolved string
	rootInfo os.FileInfo
	fileInfo os.FileInfo
}

// readJITComponentSource opens the prepared canonical target through os.Root.
// The authored alias is only re-resolved for identity checks; a symlink,
// junction, or parent reparse-point race can therefore never make the source
// read cross the prepared tenant root.
func readJITComponentSource(tenant *Tenant, declared, resolved string) ([]byte, jitComponentSourceSnapshot, error) {
	if tenant == nil {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("tenant is unavailable")
	}
	canonicalRoot, err := filepath.EvalSymlinks(tenant.resolve())
	if err != nil {
		return nil, jitComponentSourceSnapshot{}, err
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return nil, jitComponentSourceSnapshot{}, err
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, jitComponentSourceSnapshot{}, err
	}
	resolved = filepath.Clean(resolved)
	if !tenant.insideSiteRoot(canonicalRoot) || !tenant.insideSiteRoot(resolved) {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("source escapes the prepared tenant root")
	}
	relative, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("source cannot be opened through the prepared tenant root")
	}

	root, rootInfo, err := openJITComponentRoot(tenant, canonicalRoot, nil)
	if err != nil {
		return nil, jitComponentSourceSnapshot{}, err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return nil, jitComponentSourceSnapshot{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, jitComponentSourceSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("must be a regular file")
	}
	if info.Size() > site.MaxJITComponentSourceBytes {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("has %d bytes; limit is %d", info.Size(), site.MaxJITComponentSourceBytes)
	}
	snapshot := jitComponentSourceSnapshot{
		root: canonicalRoot, relative: relative, resolved: resolved,
		rootInfo: rootInfo, fileInfo: info,
	}
	if !snapshot.matches(tenant, declared, root) {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("source changed before its confined read")
	}
	source, err := io.ReadAll(io.LimitReader(file, site.MaxJITComponentSourceBytes+1))
	if err != nil {
		return nil, jitComponentSourceSnapshot{}, err
	}
	if len(source) > site.MaxJITComponentSourceBytes {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("exceeds %d-byte limit", site.MaxJITComponentSourceBytes)
	}
	if !snapshot.unchanged(tenant, declared) {
		return nil, jitComponentSourceSnapshot{}, fmt.Errorf("source changed during its confined read")
	}
	return source, snapshot, nil
}

func openJITComponentRoot(tenant *Tenant, canonicalRoot string, expected os.FileInfo) (*os.Root, os.FileInfo, error) {
	if expected == nil {
		var err error
		expected, err = os.Stat(canonicalRoot)
		if err != nil {
			return nil, nil, err
		}
	}
	root, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return nil, nil, err
	}
	openedInfo, err := root.Stat(".")
	if err != nil || !openedInfo.IsDir() || !os.SameFile(expected, openedInfo) {
		root.Close()
		return nil, nil, fmt.Errorf("tenant root changed while component source was being prepared")
	}
	currentRoot, err := filepath.EvalSymlinks(tenant.resolve())
	if err != nil {
		root.Close()
		return nil, nil, fmt.Errorf("tenant root changed while component source was being prepared")
	}
	currentRoot, err = filepath.Abs(currentRoot)
	if err != nil || filepath.Clean(currentRoot) != canonicalRoot || !tenant.insideSiteRoot(currentRoot) {
		root.Close()
		return nil, nil, fmt.Errorf("tenant root changed while component source was being prepared")
	}
	currentInfo, err := os.Stat(currentRoot)
	if err != nil || !os.SameFile(openedInfo, currentInfo) {
		root.Close()
		return nil, nil, fmt.Errorf("tenant root changed while component source was being prepared")
	}
	return root, openedInfo, nil
}

func (snapshot jitComponentSourceSnapshot) matches(tenant *Tenant, declared string, root *os.Root) bool {
	if !snapshot.authoredPathMatches(tenant, declared) {
		return false
	}
	currentInfo, err := root.Stat(snapshot.relative)
	return err == nil && os.SameFile(snapshot.fileInfo, currentInfo) && snapshot.authoredPathMatches(tenant, declared)
}

func (snapshot jitComponentSourceSnapshot) authoredPathMatches(tenant *Tenant, declared string) bool {
	currentResolved, err := filepath.EvalSymlinks(declared)
	if err != nil {
		return false
	}
	currentResolved, err = filepath.Abs(currentResolved)
	if err != nil || filepath.Clean(currentResolved) != snapshot.resolved || !tenant.insideSiteRoot(currentResolved) {
		return false
	}
	return true
}

func (snapshot jitComponentSourceSnapshot) unchanged(tenant *Tenant, declared string) bool {
	root, _, err := openJITComponentRoot(tenant, snapshot.root, snapshot.rootInfo)
	if err != nil {
		return false
	}
	defer root.Close()
	if !snapshot.matches(tenant, declared, root) {
		return false
	}
	return true
}
