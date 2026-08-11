package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/jit/javascript/vanilla"
)

type options struct {
	profile      vanilla.Profile
	output       string
	canonicalDir string
	services     []serviceSpec
	components   []vanilla.ComponentVersion
	componentReq []vanilla.ComponentServiceRequirement
	scripts      []scriptSpec
}

type serviceSpec struct {
	name     string
	version  string
	path     string
	requires []vanilla.ServiceVersion
}

type scriptSpec struct {
	name string
	path string
}

type repeatedFlag []string

func (values *repeatedFlag) String() string { return strings.Join(*values, ",") }
func (values *repeatedFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	options, err := parseOptions(os.Args[1:], os.Stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(2)
	}
	if options.canonicalDir == "" && len(options.services) == 0 && len(options.components) == 0 && len(options.scripts) == 0 {
		source, err := vanilla.SourceForProfile(options.profile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(options.output, source, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("KitJS %s release=%s profile=%s sha256:%s %d bytes\n",
			options.output, vanilla.ReleaseVersion, options.profile, vanilla.ContentHash(source), len(source))
		return
	}

	services := make([]vanilla.Service, len(options.services))
	for index, spec := range options.services {
		source, err := os.ReadFile(spec.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kitjs: read service %q: %v\n", spec.name, err)
			os.Exit(1)
		}
		services[index] = vanilla.Service{
			Name: spec.name, Version: spec.version,
			Requires: append([]vanilla.ServiceVersion(nil), spec.requires...),
			Source:   source,
		}
	}
	scripts := make([]vanilla.Script, len(options.scripts))
	for index, spec := range options.scripts {
		source, err := os.ReadFile(spec.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kitjs: read script %q: %v\n", spec.name, err)
			os.Exit(1)
		}
		scripts[index] = vanilla.Script{Name: spec.name, Source: source}
	}
	artifact, err := vanilla.Build(vanilla.BuildOptions{
		Profile: options.profile, Services: services, Components: options.components,
		ComponentRequires: options.componentReq, Scripts: scripts,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	output := options.output
	immutable := options.canonicalDir != ""
	if immutable {
		if err := os.MkdirAll(options.canonicalDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		output = filepath.Join(options.canonicalDir, artifact.Name())
	}
	if err := writeArtifact(output, artifact.Bytes(), immutable); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("KitJS %s release=%s profile=%s sha256:%s %d bytes\n",
		output, artifact.Release(), artifact.Profile(), artifact.SHA256(), artifact.Size())
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	flags := flag.NewFlagSet("assemble", flag.ContinueOnError)
	flags.SetOutput(stderr)

	profileName := flags.String("profile", string(vanilla.ProfileKit), "artifact profile: kit or hydrate")
	outputName := flags.String("output", "", "output artifact path")
	canonicalDir := flags.String("canonical-dir", "", "directory for an immutable canonical artifact")
	var componentFlags repeatedFlag
	var componentRequireFlags repeatedFlag
	var scriptFlags repeatedFlag
	var serviceFlags repeatedFlag
	var serviceRequireFlags repeatedFlag
	flags.Var(&componentFlags, "component", "exact component pin name=version (repeatable)")
	flags.Var(&componentRequireFlags, "component-require", "exact component service dependency owner=service=version (repeatable)")
	flags.Var(&scriptFlags, "script", "named classic-script package name=path (repeatable)")
	flags.Var(&serviceFlags, "service", "service package name=version=path (repeatable)")
	flags.Var(&serviceRequireFlags, "service-require", "exact service dependency owner=dependency=version (repeatable)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: assemble [-profile kit|hydrate] [-service name=version=path] [-service-require owner=dependency=version] [-component name=version] [-component-require owner=service=version] [-script name=path] (-canonical-dir dir | -output artifact.js | artifact.js)")
	}
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}

	output := *outputName
	switch {
	case *canonicalDir != "" && output == "" && flags.NArg() == 0:
	case *canonicalDir == "" && output != "" && flags.NArg() == 0:
	case *canonicalDir == "" && output == "" && flags.NArg() == 1:
		output = flags.Arg(0)
	default:
		flags.Usage()
		return options{}, errors.New("kitjs: choose exactly one output path or canonical directory")
	}

	profile := vanilla.Profile(*profileName)
	if _, err := vanilla.ArtifactName(profile); err != nil {
		return options{}, err
	}
	components := make([]vanilla.ComponentVersion, len(componentFlags))
	componentIndexes := make(map[string]int, len(componentFlags))
	for index, value := range componentFlags {
		name, version, found := strings.Cut(value, "=")
		if !found || name == "" || version == "" {
			return options{}, fmt.Errorf("kitjs: invalid component pin %q; want name=version", value)
		}
		if _, duplicate := componentIndexes[name]; duplicate {
			return options{}, fmt.Errorf("kitjs: duplicate component %q", name)
		}
		componentIndexes[name] = index
		components[index] = vanilla.ComponentVersion{Name: name, Version: version}
	}
	componentRequires := make([]vanilla.ComponentServiceRequirement, 0, len(componentRequireFlags))
	for _, value := range componentRequireFlags {
		owner, remainder, found := strings.Cut(value, "=")
		if !found || owner == "" {
			return options{}, fmt.Errorf("kitjs: invalid component dependency %q; want owner=service=version", value)
		}
		dependency, version, found := strings.Cut(remainder, "=")
		if !found || dependency == "" || version == "" {
			return options{}, fmt.Errorf("kitjs: invalid component dependency %q; want owner=service=version", value)
		}
		_, exists := componentIndexes[owner]
		if !exists {
			return options{}, fmt.Errorf("kitjs: component dependency owner %q has no -component pin", owner)
		}
		componentRequires = append(componentRequires, vanilla.ComponentServiceRequirement{
			Component: owner,
			Service:   vanilla.ServiceVersion{Name: dependency, Version: version},
		})
	}
	services := make([]serviceSpec, len(serviceFlags))
	serviceIndexes := make(map[string]int, len(serviceFlags))
	for index, value := range serviceFlags {
		name, remainder, found := strings.Cut(value, "=")
		if !found || name == "" {
			return options{}, fmt.Errorf("kitjs: invalid service %q; want name=version=path", value)
		}
		version, path, found := strings.Cut(remainder, "=")
		if !found || version == "" || path == "" {
			return options{}, fmt.Errorf("kitjs: invalid service %q; want name=version=path", value)
		}
		if _, duplicate := serviceIndexes[name]; duplicate {
			return options{}, fmt.Errorf("kitjs: duplicate service %q", name)
		}
		serviceIndexes[name] = index
		services[index] = serviceSpec{name: name, version: version, path: path}
	}
	for _, value := range serviceRequireFlags {
		owner, remainder, found := strings.Cut(value, "=")
		if !found || owner == "" {
			return options{}, fmt.Errorf("kitjs: invalid service dependency %q; want owner=dependency=version", value)
		}
		dependency, version, found := strings.Cut(remainder, "=")
		if !found || dependency == "" || version == "" {
			return options{}, fmt.Errorf("kitjs: invalid service dependency %q; want owner=dependency=version", value)
		}
		index, exists := serviceIndexes[owner]
		if !exists {
			return options{}, fmt.Errorf("kitjs: service dependency owner %q has no -service package", owner)
		}
		services[index].requires = append(services[index].requires, vanilla.ServiceVersion{
			Name: dependency, Version: version,
		})
	}
	scripts := make([]scriptSpec, len(scriptFlags))
	for index, value := range scriptFlags {
		name, path, found := strings.Cut(value, "=")
		if !found || name == "" || path == "" {
			return options{}, fmt.Errorf("kitjs: invalid script %q; want name=path", value)
		}
		scripts[index] = scriptSpec{name: name, path: path}
	}
	return options{
		profile: profile, output: output, canonicalDir: *canonicalDir,
		services: services, components: components, componentReq: componentRequires, scripts: scripts,
	}, nil
}

func writeArtifact(path string, source []byte, immutable bool) error {
	if !immutable {
		return os.WriteFile(path, source, 0o644)
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	written, err := io.Copy(file, bytes.NewReader(source))
	if err != nil || written != int64(len(source)) {
		_ = file.Close()
		if err != nil {
			return err
		}
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	// Link publishes the already-complete bytes under the canonical name in one
	// atomic, no-replace operation on both Unix and Windows. A crash before this
	// point can leave only an unaddressed temporary file, never a partial hash URL.
	if err := os.Link(temporary, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, source) {
			return fmt.Errorf("kitjs: immutable artifact %s already exists with different bytes", path)
		}
	}
	return nil
}
