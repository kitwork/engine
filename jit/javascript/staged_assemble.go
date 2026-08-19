package javascript

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
)

// JITRole is one engine-owned role in the staged browser delivery contract.
type JITRole string

const (
	JITRoleRuntime            JITRole = "runtime"
	JITRoleHydrate            JITRole = "hydrate"
	JITRoleGraph              JITRole = "graph"
	JITRoleService            JITRole = "service"
	JITRoleComponent          JITRole = "component"
	JITRoleComponents         JITRole = "components"
	stagedComponentCacheLimit         = 256
	// MaxStagedPackageSuffixBytes is shared by generation validation and the
	// public /jit/<hash>.<suffix>.js path contract.
	MaxStagedPackageSuffixBytes = 128
)

// ComponentPackage binds one exact component identity to its source. Staged
// assembly never guesses this relationship from a filename or discovery order.
type ComponentPackage struct {
	Name    string
	Version string
	Source  []byte
}

// StagedBuildOptions describes one closed staged delivery. Components named in
// SharedComponentNames are emitted in one stable shared chunk.
type StagedBuildOptions struct {
	Profile              Profile
	Services             []Service
	Components           []ComponentPackage
	ComponentRequires    []ComponentServiceRequirement
	SharedComponentNames []string
}

// JITArtifact is one immutable, content-addressed staged script.
type JITArtifact struct {
	role        JITRole
	packageName string
	version     string
	suffix      string
	sha256      string
	integrity   string
	name        string
	source      []byte
}

func (artifact JITArtifact) Role() JITRole     { return artifact.role }
func (artifact JITArtifact) Package() string   { return artifact.packageName }
func (artifact JITArtifact) Version() string   { return artifact.version }
func (artifact JITArtifact) Suffix() string    { return artifact.suffix }
func (artifact JITArtifact) SHA256() string    { return artifact.sha256 }
func (artifact JITArtifact) Integrity() string { return artifact.integrity }
func (artifact JITArtifact) Name() string      { return artifact.name }
func (artifact JITArtifact) Size() int         { return len(artifact.source) }
func (artifact JITArtifact) Bytes() []byte     { return append([]byte(nil), artifact.source...) }
func (artifact JITArtifact) Empty() bool       { return len(artifact.source) == 0 }

// StagedAssembly is one closed immutable staged delivery.
type StagedAssembly struct {
	Runtime          JITArtifact
	Hydrate          *JITArtifact
	Graph            JITArtifact
	Services         []JITArtifact
	ComponentsBundle *JITArtifact
	Components       []JITArtifact
	graphKey         string
}

// GraphKey identifies the exact runtime/profile, dependency metadata, package
// bytes, and selected chunk layout.
func (assembly StagedAssembly) GraphKey() string { return assembly.graphKey }

// GraphID is an explicit alias for GraphKey for graph-manifest consumers.
func (assembly StagedAssembly) GraphID() string { return assembly.graphKey }

// Artifacts returns fresh values in their only valid classic-defer order.
func (assembly StagedAssembly) Artifacts() []JITArtifact {
	capacity := 2 + len(assembly.Services) + len(assembly.Components)
	if assembly.Hydrate != nil {
		capacity++
	}
	if assembly.ComponentsBundle != nil {
		capacity++
	}
	artifacts := make([]JITArtifact, 0, capacity)
	artifacts = append(artifacts, assembly.Runtime)
	if assembly.Hydrate != nil {
		artifacts = append(artifacts, *assembly.Hydrate)
	}
	artifacts = append(artifacts, assembly.Graph)
	artifacts = append(artifacts, assembly.Services...)
	if assembly.ComponentsBundle != nil {
		artifacts = append(artifacts, *assembly.ComponentsBundle)
	}
	artifacts = append(artifacts, assembly.Components...)
	return artifacts
}

var stagedPackageSuffixPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

var reservedStagedPackageSuffixes = map[string]bool{
	"runtime":    true,
	"hydrate":    true,
	"graph":      true,
	"service":    true,
	"component":  true,
	"components": true,
}

type normalizedComponentPackage struct {
	identity   ComponentVersion
	source     []byte
	sourceHash string
}

type stagedComponentArtifact struct {
	component normalizedComponentPackage
	artifact  JITArtifact
}

// BuildStaged creates independently cacheable scripts without changing the
// standalone one-file SourceForProfile and Build contracts.
func BuildStaged(options StagedBuildOptions) (StagedAssembly, error) {
	if _, err := profileFragments(options.Profile); err != nil {
		return StagedAssembly{}, err
	}
	if err := validateEmbeddedRelease(); err != nil {
		return StagedAssembly{}, err
	}

	components, err := normalizeComponentPackages(options.Components)
	if err != nil {
		return StagedAssembly{}, err
	}
	if len(components) > stagedComponentCacheLimit {
		return StagedAssembly{}, fmt.Errorf("kitjs: staged component graph exceeds cache limit %d", stagedComponentCacheLimit)
	}
	componentVersions := make([]ComponentVersion, len(components))
	for index, component := range components {
		componentVersions[index] = component.identity
	}
	services, err := normalizeServices(options.Services)
	if err != nil {
		return StagedAssembly{}, err
	}
	for _, service := range services {
		if !validStagedPackageSuffix(service.Name) {
			return StagedAssembly{}, fmt.Errorf("kitjs: service %q cannot be represented by a staged asset suffix", service.Name)
		}
	}
	if err := validateDocumentOwners(componentVersions, services); err != nil {
		return StagedAssembly{}, err
	}
	requirements, err := normalizeComponentServiceRequirements(options.ComponentRequires, componentVersions, services)
	if err != nil {
		return StagedAssembly{}, err
	}
	sharedNames, err := normalizeSharedComponentNames(options.SharedComponentNames, components)
	if err != nil {
		return StagedAssembly{}, err
	}

	runtimeSource, err := stagedRuntimeSource()
	if err != nil {
		return StagedAssembly{}, err
	}
	runtime, err := newJITArtifact(JITRoleRuntime, "", "", "runtime", runtimeSource)
	if err != nil {
		return StagedAssembly{}, err
	}

	var hydrate *JITArtifact
	if options.Profile == ProfileHydrate {
		hydrateSource, sourceErr := stagedHydrateSource()
		if sourceErr != nil {
			return StagedAssembly{}, sourceErr
		}
		artifact, artifactErr := newJITArtifact(JITRoleHydrate, "", "", "hydrate", hydrateSource)
		if artifactErr != nil {
			return StagedAssembly{}, artifactErr
		}
		hydrate = &artifact
	}

	serviceArtifacts := make([]JITArtifact, 0, len(services))
	for _, service := range services {
		source, sourceErr := stagedServiceSource(service)
		if sourceErr != nil {
			return StagedAssembly{}, sourceErr
		}
		artifact, artifactErr := newJITArtifact(JITRoleService, service.Name, service.Version, service.Name, source)
		if artifactErr != nil {
			return StagedAssembly{}, artifactErr
		}
		serviceArtifacts = append(serviceArtifacts, artifact)
	}

	sharedSet := make(map[string]bool, len(sharedNames))
	for _, name := range sharedNames {
		sharedSet[name] = true
	}
	shared := make([]stagedComponentArtifact, 0, len(sharedNames))
	individual := make([]stagedComponentArtifact, 0, len(components)-len(sharedNames))
	for _, component := range components {
		source, sourceErr := stagedComponentSource(component)
		if sourceErr != nil {
			return StagedAssembly{}, sourceErr
		}
		artifact, artifactErr := newJITArtifact(JITRoleComponent, component.identity.Name, component.identity.Version, component.identity.Name, source)
		if artifactErr != nil {
			return StagedAssembly{}, artifactErr
		}
		entry := stagedComponentArtifact{component: component, artifact: artifact}
		if sharedSet[component.identity.Name] {
			shared = append(shared, entry)
		} else {
			individual = append(individual, entry)
		}
	}

	var componentsBundle *JITArtifact
	if len(shared) > 0 {
		source, sourceErr := stagedComponentsBundleSource(shared)
		if sourceErr != nil {
			return StagedAssembly{}, sourceErr
		}
		artifact, artifactErr := newJITArtifact(JITRoleComponents, "", "", "components", source)
		if artifactErr != nil {
			return StagedAssembly{}, artifactErr
		}
		componentsBundle = &artifact
	}

	graphKey, err := stagedGraphKey(options.Profile, runtime, hydrate, services, serviceArtifacts, shared, componentsBundle, individual, requirements)
	if err != nil {
		return StagedAssembly{}, err
	}
	graphSource, err := stagedGraphSource(options.Profile, graphKey, runtime, hydrate, services, serviceArtifacts, components, requirements, shared, componentsBundle, individual)
	if err != nil {
		return StagedAssembly{}, err
	}
	graph, err := newJITArtifact(JITRoleGraph, "", "", "graph", graphSource)
	if err != nil {
		return StagedAssembly{}, err
	}

	componentArtifacts := make([]JITArtifact, len(individual))
	for index, entry := range individual {
		componentArtifacts[index] = entry.artifact
	}
	return StagedAssembly{
		Runtime:          runtime,
		Hydrate:          cloneOptionalJITArtifact(hydrate),
		Graph:            graph,
		Services:         append([]JITArtifact(nil), serviceArtifacts...),
		ComponentsBundle: cloneOptionalJITArtifact(componentsBundle),
		Components:       componentArtifacts,
		graphKey:         graphKey,
	}, nil
}

func cloneOptionalJITArtifact(artifact *JITArtifact) *JITArtifact {
	if artifact == nil {
		return nil
	}
	clone := *artifact
	clone.source = append([]byte(nil), artifact.source...)
	return &clone
}

func validStagedPackageSuffix(name string) bool {
	return validStagedSuffixSyntax(name) && !reservedStagedPackageSuffixes[name]
}

func validStagedSuffixSyntax(name string) bool {
	return len(name) > 0 && len(name) <= MaxStagedPackageSuffixBytes && stagedPackageSuffixPattern.MatchString(name)
}

// ValidStagedComponentName reports whether a component identity can be used
// unchanged as one bounded, non-reserved staged asset suffix.
func ValidStagedComponentName(name string) bool {
	return componentNamePattern.MatchString(name) && !blockedComponentNames[name] && validStagedPackageSuffix(name)
}

func normalizeComponentPackages(input []ComponentPackage) ([]normalizedComponentPackage, error) {
	components := make([]normalizedComponentPackage, len(input))
	seen := make(map[string]bool, len(input))
	for index, component := range input {
		if !componentNamePattern.MatchString(component.Name) || blockedComponentNames[component.Name] {
			return nil, fmt.Errorf("kitjs: invalid component name %q", component.Name)
		}
		if !validStagedPackageSuffix(component.Name) {
			return nil, fmt.Errorf("kitjs: component %q cannot be represented by a staged asset suffix", component.Name)
		}
		if !exactSemVer(component.Version) {
			return nil, fmt.Errorf("kitjs: component %q has non-exact SemVer %q", component.Name, component.Version)
		}
		if seen[component.Name] {
			return nil, fmt.Errorf("kitjs: duplicate component %q in graph", component.Name)
		}
		seen[component.Name] = true
		if err := validateClassicScript("component:"+component.Name+"@"+component.Version, component.Source); err != nil {
			return nil, err
		}
		if err := validateStagedComponentSource(component.Name, component.Source); err != nil {
			return nil, err
		}
		components[index] = normalizedComponentPackage{
			identity:   ComponentVersion{Name: component.Name, Version: component.Version},
			source:     append([]byte(nil), component.Source...),
			sourceHash: ContentHash(component.Source),
		}
	}
	sort.Slice(components, func(left, right int) bool {
		return components[left].identity.Name < components[right].identity.Name
	})
	return components, nil
}

func normalizeSharedComponentNames(input []string, components []normalizedComponentPackage) ([]string, error) {
	if len(input) == 1 {
		return nil, fmt.Errorf("kitjs: shared components bundle requires at least two components")
	}
	available := make(map[string]bool, len(components))
	for _, component := range components {
		available[component.identity.Name] = true
	}
	names := append([]string(nil), input...)
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			return nil, fmt.Errorf("kitjs: shared components bundle repeats %q", name)
		}
		seen[name] = true
		if !available[name] {
			return nil, fmt.Errorf("kitjs: shared components bundle references missing component %q", name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func newJITArtifact(role JITRole, packageName, version, suffix string, source []byte) (JITArtifact, error) {
	if err := validateRuntimeFragment("staged "+string(role)+" artifact", source); err != nil {
		return JITArtifact{}, err
	}
	if !validStagedSuffixSyntax(suffix) {
		return JITArtifact{}, fmt.Errorf("kitjs: invalid staged artifact suffix %q", suffix)
	}
	sum := sha256.Sum256(source)
	digest := hex.EncodeToString(sum[:])
	return JITArtifact{
		role:        role,
		packageName: packageName,
		version:     version,
		suffix:      suffix,
		sha256:      digest,
		integrity:   "sha256-" + base64.StdEncoding.EncodeToString(sum[:]),
		name:        digest + "." + suffix + ".js",
		source:      append([]byte(nil), source...),
	}, nil
}

func stagedRuntimeSource() ([]byte, error) {
	if len(kitFragments) < 3 || kitFragments[len(kitFragments)-2] != "src/profile-kit.js" || kitFragments[len(kitFragments)-1] != "src/boot.js" {
		return nil, fmt.Errorf("kitjs: kit profile has no staged runtime boundary")
	}
	source, err := exactFragmentSource(kitFragments[:len(kitFragments)-2])
	if err != nil {
		return nil, err
	}
	return append(source, stagedRuntimeWatchdog...), nil
}

var stagedRuntimeWatchdog = []byte(`; (function (document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var core = document[ASSEMBLY];
  if (!core) throw new Error("KitJS: staged runtime watchdog loaded out of order");
  document.addEventListener("DOMContentLoaded", function () {
    if (document[ASSEMBLY] !== core || core.booted) return;
    delete document[ASSEMBLY];
    if (typeof core.report === "function") {
      core.report(new Error("KitJS: incomplete staged delivery was not published"));
    }
  }, { once: true });
})(document);
`)

func stagedHydrateSource() ([]byte, error) {
	return exactFragmentSource([]string{"src/morph.js", "src/drive.js"})
}

func exactFragmentSource(names []string) ([]byte, error) {
	var output bytes.Buffer
	for _, name := range names {
		source, err := sources.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("kitjs: read %s: %w", name, err)
		}
		if err := validateRuntimeFragment(name, source); err != nil {
			return nil, err
		}
		_, _ = output.Write(source)
	}
	return output.Bytes(), nil
}

func stagedGraphKey(profile Profile, runtime JITArtifact, hydrate *JITArtifact, services []Service, serviceArtifacts []JITArtifact, shared []stagedComponentArtifact, componentsBundle *JITArtifact, individual []stagedComponentArtifact, requirements []ComponentServiceRequirement) (string, error) {
	hash := sha256.New()
	writeHashFrame(hash, []byte("kitjs-staged-graph-v1"))
	writeHashFrame(hash, []byte(ReleaseVersion))
	writeHashFrame(hash, []byte(profile))
	markerName := "src/profile-kit.js"
	if profile == ProfileHydrate {
		markerName = "src/profile-hydrate.js"
	}
	for _, name := range []string{markerName, "src/boot.js"} {
		source, err := sources.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("kitjs: read %s: %w", name, err)
		}
		writeHashFrame(hash, []byte(name))
		writeHashFrame(hash, source)
	}
	if len(services) > 0 {
		serviceRuntime, err := sources.ReadFile("src/service.js")
		if err != nil {
			return "", fmt.Errorf("kitjs: read src/service.js: %w", err)
		}
		writeHashFrame(hash, []byte("src/service.js"))
		writeHashFrame(hash, serviceRuntime)
	}
	writeStagedArtifactHashFrame(hash, runtime)
	if hydrate != nil {
		writeStagedArtifactHashFrame(hash, *hydrate)
	} else {
		writeHashFrame(hash, []byte("no-hydrate"))
	}
	for index, service := range services {
		writeStagedArtifactHashFrame(hash, serviceArtifacts[index])
		for _, dependency := range service.Requires {
			writeHashFrame(hash, []byte("requires-service"))
			writeHashFrame(hash, []byte(dependency.Name))
			writeHashFrame(hash, []byte(dependency.Version))
		}
		for _, action := range service.Actions {
			writeHashFrame(hash, []byte("authored-action"))
			writeHashFrame(hash, []byte(action))
		}
	}
	if componentsBundle != nil {
		writeStagedArtifactHashFrame(hash, *componentsBundle)
		for _, entry := range shared {
			writeHashFrame(hash, []byte("bundled-component"))
			writeHashFrame(hash, []byte(entry.component.identity.Name))
			writeHashFrame(hash, []byte(entry.component.identity.Version))
		}
	} else {
		writeHashFrame(hash, []byte("no-components-bundle"))
	}
	for _, entry := range individual {
		writeStagedArtifactHashFrame(hash, entry.artifact)
	}
	for _, requirement := range requirements {
		writeHashFrame(hash, []byte("component-requires-service"))
		writeHashFrame(hash, []byte(requirement.Component))
		writeHashFrame(hash, []byte(requirement.Service.Name))
		writeHashFrame(hash, []byte(requirement.Service.Version))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeStagedArtifactHashFrame(output interface{ Write([]byte) (int, error) }, artifact JITArtifact) {
	writeHashFrame(output, []byte("artifact"))
	writeHashFrame(output, []byte(artifact.role))
	writeHashFrame(output, []byte(artifact.packageName))
	writeHashFrame(output, []byte(artifact.version))
	writeHashFrame(output, []byte(artifact.suffix))
	writeHashFrame(output, []byte(artifact.sha256))
	writeHashFrame(output, []byte(artifact.integrity))
}
