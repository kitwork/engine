// Command kitdbdist prepares local RC bundles. It does not publish releases.
package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb"
)

const modulePath = "github.com/kitwork/engine"

var commands = []string{"kitdb", "kitdbpg", "kitdbimport", "kitdbcanary"}
var rcVersion = regexp.MustCompile(`^v1\.0\.0-rc\.[1-9][0-9]*$`)

type artifact struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Format        string                     `json:"format"`
	Version       string                     `json:"version"`
	Commit        string                     `json:"commit"`
	SourceModule  string                     `json:"source_module"`
	GoVersion     string                     `json:"go_version"`
	Target        string                     `json:"target"`
	CGOEnabled    bool                       `json:"cgo_enabled"`
	Qualification string                     `json:"qualification"`
	Kernel        kitdb.CompatibilityProfile `json:"kernel"`
	Files         []artifact                 `json:"files"`
}

func main() {
	version := flag.String("version", "v1.0.0-rc.1", "candidate version (v1.0.0-rc.N)")
	output := flag.String("output", "", "new output directory; existing directories are refused")
	targets := flag.String("targets", "windows/amd64,linux/amd64", "comma-separated RC platforms")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	root, err := os.Getwd()
	if err == nil {
		err = distribute(ctx, root, *output, *version, strings.Split(*targets, ","))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kitdbdist:", err)
		os.Exit(1)
	}
	fmt.Println("kitdbdist: RC bundles prepared; platform release gates and deployment qualification are separate")
}

func validate(version string, targets []string) error {
	if !rcVersion.MatchString(version) {
		return fmt.Errorf("only v1.0.0-rc.N candidates may be packaged; stable promotion requires reviewed evidence")
	}
	if len(targets) < 1 || len(targets) > 2 {
		return fmt.Errorf("choose one or both supported platforms")
	}
	for i, target := range targets {
		if target != "windows/amd64" && target != "linux/amd64" {
			return fmt.Errorf("unsupported RC platform %q", target)
		}
		if slices.Contains(targets[:i], target) {
			return fmt.Errorf("duplicate target %q", target)
		}
	}
	return nil
}

func distribute(ctx context.Context, root, output, version string, targets []string) (err error) {
	if err = validate(version, targets); err != nil {
		return err
	}
	if output == "" {
		return fmt.Errorf("--output is required")
	}
	commit, err := cleanCommit(ctx, root)
	if err != nil {
		return err
	}
	mod, err := run(ctx, root, nil, "go", "list", "-m")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(mod)) != modulePath {
		return fmt.Errorf("run kitdbdist from the Engine module root")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	if err = os.Mkdir(output, 0o755); err != nil {
		return fmt.Errorf("reserve new output directory: %w", err)
	}
	// Only this exclusively created directory is removed on failure.
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(output))
		}
	}()
	var archives []artifact
	for _, target := range targets {
		fmt.Println("kitdbdist: building", target)
		entry, buildErr := buildTarget(ctx, root, output, version, commit, target)
		if buildErr != nil {
			return buildErr
		}
		archives = append(archives, entry)
	}
	if after, checkErr := cleanCommit(ctx, root); checkErr != nil || after != commit {
		return fmt.Errorf("source changed during packaging: %w", errors.Join(checkErr, fmt.Errorf("expected %s, got %s", commit, after)))
	}
	var checksums strings.Builder
	for _, entry := range archives {
		fmt.Fprintf(&checksums, "%s  %s\n", entry.SHA256, entry.Name)
	}
	return os.WriteFile(filepath.Join(output, "SHA256SUMS"), []byte(checksums.String()), 0o644)
}

func buildTarget(ctx context.Context, root, output, version, commit, target string) (artifact, error) {
	parts := strings.Split(target, "/")
	name := "kitdb-" + version + "-" + parts[0] + "-" + parts[1]
	directory := filepath.Join(output, name)
	if err := os.Mkdir(directory, 0o755); err != nil {
		return artifact{}, err
	}
	environment := map[string]string{
		"GOOS": parts[0], "GOARCH": parts[1], "GOAMD64": "v1", "CGO_ENABLED": "0",
		"GOFLAGS": "", "GOWORK": "off", "GOEXPERIMENT": "",
	}
	link := "-X " + modulePath + "/kitdb/buildinfo.version=" + version + " -X " + modulePath + "/kitdb/buildinfo.commit=" + commit
	packages := []string{"list", "-mod=readonly", "-buildvcs=false", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}"}
	for _, command := range commands {
		packages = append(packages, "./cmd/"+command)
	}
	dependencies, err := run(ctx, root, environment, "go", packages...)
	if err != nil {
		return artifact{}, err
	}
	for _, dependency := range strings.Fields(string(dependencies)) {
		if !allowedPackage(dependency) {
			return artifact{}, fmt.Errorf("standalone bundle imports unreviewed package %s", dependency)
		}
	}
	report := manifest{Format: "kitdb-distribution/v1", Version: version, Commit: commit, SourceModule: modulePath,
		Target: target, Qualification: "release-candidate; deployment qualification pending", Kernel: kitdb.CurrentCompatibility()}
	for _, command := range commands {
		filename := command
		if parts[0] == "windows" {
			filename += ".exe"
		}
		path := filepath.Join(directory, filename)
		if _, err := run(ctx, root, environment, "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", link, "-o", path, "./cmd/"+command); err != nil {
			return artifact{}, err
		}
		info, err := buildinfo.ReadFile(path)
		if err != nil {
			return artifact{}, err
		}
		if info.Path != modulePath+"/cmd/"+command {
			return artifact{}, fmt.Errorf("binary has wrong entry point: %s", info.Path)
		}
		cgoDisabled := false
		for _, setting := range info.Settings {
			if setting.Key == "CGO_ENABLED" && setting.Value == "0" {
				cgoDisabled = true
			}
		}
		if !cgoDisabled {
			return artifact{}, fmt.Errorf("%s lacks CGO_ENABLED=0 build evidence", filename)
		}
		for _, dep := range info.Deps {
			if dep.Path != "github.com/lib/pq" || dep.Replace != nil {
				return artifact{}, fmt.Errorf("unreviewed binary dependency %s", dep.Path)
			}
		}
		report.GoVersion = info.GoVersion
		if target == runtime.GOOS+"/"+runtime.GOARCH {
			argument := "--version"
			if command == "kitdb" {
				argument = "version"
			}
			data, err := run(ctx, root, nil, path, argument)
			if err != nil {
				return artifact{}, err
			}
			if err := verifyVersion(data, command, version, commit); err != nil {
				return artifact{}, err
			}
		}
	}
	for _, document := range []struct{ source, name string }{
		{"LICENSE", "LICENSE"}, {"LICENSE-EXCEPTION.md", "LICENSE-EXCEPTION.md"},
		{"kitdb/DISTRIBUTION.md", "README.md"}, {"kitdb/RELEASE_1_0.md", "RELEASE_1_0.md"},
		{"kitdb/DISTRIBUTION.md", "DISTRIBUTION.md"},
		{"kitdb/PRODUCTION.md", "PRODUCTION.md"}, {"kitdb/relational/POSTGRES_COMPATIBILITY.md", "POSTGRES_COMPATIBILITY.md"},
	} {
		if err := copyFile(filepath.Join(root, document.source), filepath.Join(directory, document.name)); err != nil {
			return artifact{}, err
		}
	}
	if err := copyDependencyLicenses(ctx, root, directory); err != nil {
		return artifact{}, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return artifact{}, err
	}
	for _, entry := range entries {
		file, err := digestFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return artifact{}, err
		}
		report.Files = append(report.Files, file)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return artifact{}, err
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return artifact{}, err
	}
	archivePath := filepath.Join(output, name+".zip")
	if err := archiveDirectory(directory, archivePath); err != nil {
		return artifact{}, err
	}
	return digestFile(archivePath)
}

func verifyVersion(data []byte, command, version, commit string) error {
	var value struct {
		Version, Commit string
		Dirty           bool
	}
	if command == "kitdb" {
		var envelope struct {
			Result struct{ Build json.RawMessage }
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		data = envelope.Result.Build
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Version != version || value.Commit != commit || value.Dirty {
		return fmt.Errorf("%s binary build identity does not match source", command)
	}
	return nil
}

func allowedPackage(path string) bool {
	for _, prefix := range []string{modulePath + "/kitdb", modulePath + "/search", "github.com/lib/pq"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	if path == modulePath+"/id" || path == modulePath+"/internal/snapshotfile" {
		return true
	}
	for _, command := range commands {
		if path == modulePath+"/cmd/"+command {
			return true
		}
	}
	return false
}

func cleanCommit(ctx context.Context, root string) (string, error) {
	data, err := run(ctx, root, nil, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(string(data))) != 0 {
		return "", fmt.Errorf("packaging requires a clean committed source tree")
	}
	data, err = run(ctx, root, nil, "git", "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(data))
	if len(commit) != 40 {
		return "", fmt.Errorf("expected a full source commit")
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return "", err
	}
	return commit, nil
}

func run(ctx context.Context, root string, overlay map[string]string, program string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, program, args...)
	command.Dir = root
	command.Env = buildEnvironment(os.Environ(), overlay)
	command.Stderr = os.Stderr
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", program, err)
	}
	return data, nil
}

func buildEnvironment(base []string, overlay map[string]string) []string {
	result := make([]string, 0, len(base)+len(overlay))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		replaced := false
		for name := range overlay {
			if strings.EqualFold(key, name) {
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range overlay {
		result = append(result, name+"="+value)
	}
	return result
}

func copyDependencyLicenses(ctx context.Context, root, directory string) error {
	data, err := run(ctx, root, map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly"}, "go", "list", "-m", "-json", "github.com/lib/pq")
	if err != nil {
		return err
	}
	var dependency struct {
		Dir     string
		Replace any
	}
	if err := json.Unmarshal(data, &dependency); err != nil {
		return err
	}
	if dependency.Dir == "" || dependency.Replace != nil {
		return fmt.Errorf("lib/pq license source is not canonical")
	}
	if err := copyFile(filepath.Join(dependency.Dir, "LICENSE.md"), filepath.Join(directory, "LICENSE-libpq.md")); err != nil {
		return err
	}
	data, err = run(ctx, root, nil, "go", "env", "GOROOT")
	if err != nil {
		return err
	}
	return copyFile(filepath.Join(strings.TrimSpace(string(data)), "LICENSE"), filepath.Join(directory, "LICENSE-Go"))
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	return errors.Join(err, output.Close())
}

func digestFile(path string) (artifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return artifact{}, err
	}
	defer file.Close()
	hash := sha256.New()
	length, err := io.Copy(hash, file)
	return artifact{Name: filepath.Base(path), Bytes: length, SHA256: hex.EncodeToString(hash.Sum(nil))}, err
}

func archiveDirectory(directory, path string) (err error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(file)
	defer func() { err = errors.Join(err, archive.Close(), file.Close()) }()
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular distribution entry %q", entry.Name())
		}
		header := &zip.FileHeader{Name: entry.Name(), Method: zip.Deflate}
		header.SetMode(0o644)
		// Cross-building on Windows does not preserve Unix executable mode.
		if slices.Contains(commands, strings.TrimSuffix(entry.Name(), ".exe")) {
			header.SetMode(0o755)
		}
		// Fixed archive times make repeat builds comparable on the same toolchain.
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		if err := errors.Join(copyErr, input.Close()); err != nil {
			return err
		}
	}
	return nil
}
