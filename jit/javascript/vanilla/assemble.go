package vanilla

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ReleaseVersion is the exact SemVer of the KitJS browser runtime assembled
// by this package. A source change shipped to users must advance this value.
const ReleaseVersion = "0.8.0"

// Profile selects one public, deterministic browser artifact.
type Profile string

const (
	// ProfileKit is the independent component/directive runtime.
	ProfileKit Profile = "kit"
	// ProfileHydrate adds private Morph and Drive document continuity.
	ProfileHydrate Profile = "hydrate"
)

// ComponentVersion is one exact component release required by an artifact.
// Version ranges and floating tags are deliberately unsupported.
type ComponentVersion struct {
	Name    string
	Version string
}

// ServiceVersion is one exact service dependency. Services may require only
// other services; Go closes and orders that graph before any package source is
// emitted. The browser never resolves ranges or floating versions.
type ServiceVersion struct {
	Name    string
	Version string
}

// ComponentServiceRequirement is one exact component-to-service dependency.
// Keeping dependency edges separate preserves the small comparable
// ComponentVersion value used by existing callers.
type ComponentServiceRequirement struct {
	Component string
	Service   ServiceVersion
}

// Service is one trusted service package included in a sealed artifact.
// Source must be an ordinary classic script that registers exactly one service
// through the private package-time kit.service registrar.
type Service struct {
	Name     string
	Version  string
	Requires []ServiceVersion
	Source   []byte
}

// Script is one named classic-script package included in a component graph.
// Names are artifact-local identities and do not appear in the browser API.
type Script struct {
	Name   string
	Source []byte
}

// BuildOptions describes one closed, deterministic browser artifact.
type BuildOptions struct {
	Profile           Profile
	Services          []Service
	Components        []ComponentVersion
	ComponentRequires []ComponentServiceRequirement
	Scripts           []Script
}

// Artifact is an immutable assembled browser artifact. Bytes returns a copy so
// callers cannot change the bytes after their identity has been calculated.
type Artifact struct {
	profile Profile
	release string
	sha256  string
	name    string
	source  []byte
}

// Profile returns the runtime profile included in the artifact.
func (artifact Artifact) Profile() Profile { return artifact.profile }

// Release returns the exact KitJS release included in the artifact.
func (artifact Artifact) Release() string { return artifact.release }

// SHA256 returns the lowercase, full SHA-256 of the exact artifact bytes.
func (artifact Artifact) SHA256() string { return artifact.sha256 }

// Name returns the canonical immutable filename for the artifact.
func (artifact Artifact) Name() string { return artifact.name }

// Bytes returns a private copy of the exact artifact bytes.
func (artifact Artifact) Bytes() []byte { return append([]byte(nil), artifact.source...) }

// Size returns the artifact size in bytes.
func (artifact Artifact) Size() int { return len(artifact.source) }

// kitFragments and hydrateFragments are deliberately ordered by hand. Every
// entry is an ordinary classic browser script. Hydrate is the exact base
// runtime plus Morph and Drive immediately before boot; the assembler performs
// no source transformation.
var kitFragments = []string{
	"src/core.js",
	"src/lexer.js",
	"src/parser.js",
	"src/evaluator.js",
	"src/component.js",
	"src/directives.js",
	"src/dom.js",
	"src/structure.js",
	"src/class.js",
	"src/model.js",
	"src/events.js",
	"src/boot.js",
}

var hydrateFragments = []string{
	"src/core.js",
	"src/lexer.js",
	"src/parser.js",
	"src/evaluator.js",
	"src/component.js",
	"src/directives.js",
	"src/dom.js",
	"src/structure.js",
	"src/class.js",
	"src/model.js",
	"src/events.js",
	"src/morph.js",
	"src/drive.js",
	"src/boot.js",
}

//go:embed src/*.js
var sources embed.FS

// FragmentNames returns a copy of the canonical standalone assembly order.
func FragmentNames() []string {
	return append([]string(nil), kitFragments...)
}

// FragmentNamesForProfile returns a copy of a profile's canonical assembly order.
func FragmentNamesForProfile(profile Profile) ([]string, error) {
	fragments, err := profileFragments(profile)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), fragments...), nil
}

// Source assembles the independent browser runtime without transformation.
func Source() ([]byte, error) {
	return SourceForProfile(ProfileKit)
}

// SourceForProfile assembles one public profile without transformation.
func SourceForProfile(profile Profile) ([]byte, error) {
	fragments, err := profileFragments(profile)
	if err != nil {
		return nil, err
	}
	if err := validateEmbeddedRelease(); err != nil {
		return nil, err
	}

	var output bytes.Buffer
	for _, name := range fragments {
		source, err := sources.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("kitjs: read %s: %w", name, err)
		}
		if len(source) == 0 || source[0] != ';' {
			return nil, fmt.Errorf("kitjs: %s must begin with ';'", name)
		}
		if source[len(source)-1] != '\n' {
			return nil, fmt.Errorf("kitjs: %s must end with LF", name)
		}
		if bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(source, []byte{'\r'}) {
			return nil, fmt.Errorf("kitjs: %s must be UTF-8 without BOM and use LF", name)
		}
		_, _ = output.Write(source)
	}
	return output.Bytes(), nil
}

// ArtifactName returns the stable unhashed filename for a public profile.
func ArtifactName(profile Profile) (string, error) {
	switch profile {
	case ProfileKit:
		return "kit.js", nil
	case ProfileHydrate:
		return "hydrate.kit.js", nil
	default:
		return "", fmt.Errorf("kitjs: unknown profile %q", profile)
	}
}

func profileFragments(profile Profile) ([]string, error) {
	switch profile {
	case ProfileKit:
		return kitFragments, nil
	case ProfileHydrate:
		return hydrateFragments, nil
	default:
		return nil, fmt.Errorf("kitjs: unknown profile %q", profile)
	}
}

// ContentHash identifies the exact assembled artifact bytes.
func ContentHash(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

var (
	componentNamePattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.-]*$`)
	serviceNamePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	scriptNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hexSHA256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var blockedComponentNames = map[string]bool{
	"constructor":      true,
	"prototype":        true,
	"__proto__":        true,
	"__defineGetter__": true,
	"__defineSetter__": true,
	"__lookupGetter__": true,
	"__lookupSetter__": true,
	"ownerDocument":    true,
	"defaultView":      true,
	"contentWindow":    true,
	"window":           true,
	"globalThis":       true,
	"top":              true,
	"parent":           true,
	"self":             true,
	"caller":           true,
	"callee":           true,
	"arguments":        true,
}

var reservedServiceNames = map[string]bool{
	"version":   true,
	"component": true,
	"service":   true,
}

// Build creates one closed component graph and its immutable browser
// artifact. The manifest and named scripts are sorted by identity, so caller
// map or discovery order cannot change the result.
func Build(options BuildOptions) (Artifact, error) {
	fragments, err := profileFragments(options.Profile)
	if err != nil {
		return Artifact{}, err
	}
	runtimeSource, err := SourceForProfile(options.Profile)
	if err != nil {
		return Artifact{}, err
	}
	components, err := normalizeComponents(options.Components)
	if err != nil {
		return Artifact{}, err
	}
	services, err := normalizeServices(options.Services)
	if err != nil {
		return Artifact{}, err
	}
	componentRequires, err := normalizeComponentServiceRequirements(options.ComponentRequires, components, services)
	if err != nil {
		return Artifact{}, err
	}
	scripts, err := normalizeScripts(options.Scripts)
	if err != nil {
		return Artifact{}, err
	}

	var serviceRuntime []byte
	if len(services) > 0 {
		serviceRuntime, err = sources.ReadFile("src/service.js")
		if err != nil {
			return Artifact{}, fmt.Errorf("kitjs: read src/service.js: %w", err)
		}
		if err := validateRuntimeFragment("src/service.js", serviceRuntime); err != nil {
			return Artifact{}, err
		}
	}

	graphID := componentGraphID(options.Profile, runtimeSource, serviceRuntime, services, components, componentRequires, scripts)
	graphSource, err := componentGraphSource(options.Profile, graphID, services, components, scripts)
	if err != nil {
		return Artifact{}, err
	}

	var output bytes.Buffer
	inserted := false
	for _, name := range fragments {
		if name == "src/boot.js" {
			_, _ = output.Write(serviceRuntime)
			_, _ = output.Write(graphSource)
			inserted = true
		}
		source, err := sources.ReadFile(name)
		if err != nil {
			return Artifact{}, fmt.Errorf("kitjs: read %s: %w", name, err)
		}
		_, _ = output.Write(source)
	}
	if !inserted {
		return Artifact{}, fmt.Errorf("kitjs: profile %q has no boot fragment", options.Profile)
	}

	assembled := output.Bytes()
	digest := ContentHash(assembled)
	name, err := CanonicalArtifactName(options.Profile, digest)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{
		profile: options.Profile,
		release: ReleaseVersion,
		sha256:  digest,
		name:    name,
		source:  append([]byte(nil), assembled...),
	}, nil
}

// CanonicalArtifactName returns the immutable production filename for exact
// artifact bytes. Development aliases remain available through ArtifactName.
func CanonicalArtifactName(profile Profile, digest string) (string, error) {
	alias, err := ArtifactName(profile)
	if err != nil {
		return "", err
	}
	if !hexSHA256Pattern.MatchString(digest) {
		return "", fmt.Errorf("kitjs: invalid SHA-256 %q", digest)
	}
	return strings.TrimSuffix(alias, ".js") + "." + ReleaseVersion + "." + digest + ".js", nil
}

func validateEmbeddedRelease() error {
	if !exactSemVer(ReleaseVersion) {
		return fmt.Errorf("kitjs: ReleaseVersion %q is not exact SemVer", ReleaseVersion)
	}
	source, err := sources.ReadFile("src/core.js")
	if err != nil {
		return fmt.Errorf("kitjs: read src/core.js: %w", err)
	}
	needle := []byte(`var VERSION = "` + ReleaseVersion + `";`)
	if bytes.Count(source, needle) != 1 {
		return fmt.Errorf("kitjs: ReleaseVersion %q does not match src/core.js", ReleaseVersion)
	}
	return nil
}

func normalizeComponents(input []ComponentVersion) ([]ComponentVersion, error) {
	components := append([]ComponentVersion(nil), input...)
	seen := make(map[string]bool, len(components))
	for _, component := range components {
		if !componentNamePattern.MatchString(component.Name) || blockedComponentNames[component.Name] {
			return nil, fmt.Errorf("kitjs: invalid component name %q", component.Name)
		}
		if !exactSemVer(component.Version) {
			return nil, fmt.Errorf("kitjs: component %q has non-exact SemVer %q", component.Name, component.Version)
		}
		if seen[component.Name] {
			return nil, fmt.Errorf("kitjs: duplicate component %q in graph", component.Name)
		}
		seen[component.Name] = true
	}
	sort.Slice(components, func(left, right int) bool {
		return components[left].Name < components[right].Name
	})
	return components, nil
}

func normalizeComponentServiceRequirements(input []ComponentServiceRequirement, components []ComponentVersion, services []Service) ([]ComponentServiceRequirement, error) {
	componentVersions := make(map[string]string, len(components))
	for _, component := range components {
		componentVersions[component.Name] = component.Version
	}
	serviceVersions := make(map[string]string, len(services))
	for _, service := range services {
		serviceVersions[service.Name] = service.Version
	}
	requirements := append([]ComponentServiceRequirement(nil), input...)
	seen := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		componentVersion, componentExists := componentVersions[requirement.Component]
		if !componentExists {
			return nil, fmt.Errorf("kitjs: service dependency owner component %q is missing", requirement.Component)
		}
		if !validServiceVersion(requirement.Service) {
			return nil, fmt.Errorf("kitjs: component %s@%s has invalid service dependency %s@%s",
				requirement.Component, componentVersion, requirement.Service.Name, requirement.Service.Version)
		}
		key := requirement.Component + "\x00" + requirement.Service.Name
		if seen[key] {
			return nil, fmt.Errorf("kitjs: component %s@%s repeats service dependency %s",
				requirement.Component, componentVersion, requirement.Service.Name)
		}
		seen[key] = true
		serviceVersion, serviceExists := serviceVersions[requirement.Service.Name]
		if !serviceExists {
			return nil, fmt.Errorf("kitjs: component %s@%s requires missing service %s@%s",
				requirement.Component, componentVersion, requirement.Service.Name, requirement.Service.Version)
		}
		if serviceVersion != requirement.Service.Version {
			return nil, fmt.Errorf("kitjs: component %s@%s requires service %s@%s but graph provides %s",
				requirement.Component, componentVersion, requirement.Service.Name, requirement.Service.Version, serviceVersion)
		}
	}
	sort.Slice(requirements, func(left, right int) bool {
		if requirements[left].Component != requirements[right].Component {
			return requirements[left].Component < requirements[right].Component
		}
		if requirements[left].Service.Name != requirements[right].Service.Name {
			return requirements[left].Service.Name < requirements[right].Service.Name
		}
		return requirements[left].Service.Version < requirements[right].Service.Version
	})
	return requirements, nil
}

func validServiceVersion(service ServiceVersion) bool {
	return serviceNamePattern.MatchString(service.Name) &&
		!blockedComponentNames[service.Name] &&
		!reservedServiceNames[service.Name] &&
		exactSemVer(service.Version)
}

// normalizeServices copies, validates, and deterministically topologically
// orders a closed service graph. Dependencies always precede their owner; ties
// between independent services are resolved by service name.
func normalizeServices(input []Service) ([]Service, error) {
	byName := make(map[string]Service, len(input))
	names := make([]string, 0, len(input))
	for _, service := range input {
		identity := ServiceVersion{Name: service.Name, Version: service.Version}
		if !serviceNamePattern.MatchString(service.Name) || blockedComponentNames[service.Name] || reservedServiceNames[service.Name] {
			return nil, fmt.Errorf("kitjs: invalid service name %q", service.Name)
		}
		if !exactSemVer(service.Version) {
			return nil, fmt.Errorf("kitjs: service %q has non-exact SemVer %q", service.Name, service.Version)
		}
		if _, exists := byName[service.Name]; exists {
			return nil, fmt.Errorf("kitjs: duplicate service %q in graph", service.Name)
		}
		if err := validateClassicScript("service:"+service.Name+"@"+service.Version, service.Source); err != nil {
			return nil, err
		}

		requires := make([]ServiceVersion, len(service.Requires))
		seenRequires := make(map[string]ServiceVersion, len(service.Requires))
		for index, dependency := range service.Requires {
			if !validServiceVersion(dependency) {
				return nil, fmt.Errorf("kitjs: service %s@%s has invalid dependency %s@%s",
					identity.Name, identity.Version, dependency.Name, dependency.Version)
			}
			if prior, exists := seenRequires[dependency.Name]; exists {
				return nil, fmt.Errorf("kitjs: service %s@%s repeats dependency %s (versions %s and %s)",
					identity.Name, identity.Version, dependency.Name, prior.Version, dependency.Version)
			}
			seenRequires[dependency.Name] = dependency
			requires[index] = dependency
		}
		sort.Slice(requires, func(left, right int) bool {
			if requires[left].Name != requires[right].Name {
				return requires[left].Name < requires[right].Name
			}
			return requires[left].Version < requires[right].Version
		})
		byName[service.Name] = Service{
			Name:     service.Name,
			Version:  service.Version,
			Requires: requires,
			Source:   append([]byte(nil), service.Source...),
		}
		names = append(names, service.Name)
	}

	sort.Strings(names)
	for _, name := range names {
		service := byName[name]
		for _, dependency := range service.Requires {
			selected, exists := byName[dependency.Name]
			if !exists {
				return nil, fmt.Errorf("kitjs: service %s@%s requires missing service %s@%s",
					service.Name, service.Version, dependency.Name, dependency.Version)
			}
			if selected.Version != dependency.Version {
				return nil, fmt.Errorf("kitjs: service %s@%s requires service %s@%s but graph provides %s",
					service.Name, service.Version, dependency.Name, dependency.Version, selected.Version)
			}
		}
	}

	ordered := make([]Service, 0, len(names))
	state := make(map[string]uint8, len(names))
	stack := make([]string, 0, len(names))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			start := 0
			for index, entry := range stack {
				if entry == name {
					start = index
					break
				}
			}
			cycle := append(append([]string(nil), stack[start:]...), name)
			return fmt.Errorf("kitjs: service dependency cycle: %s", strings.Join(cycle, " -> "))
		case 2:
			return nil
		}
		state[name] = 1
		stack = append(stack, name)
		for _, dependency := range byName[name].Requires {
			if err := visit(dependency.Name); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		ordered = append(ordered, byName[name])
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func normalizeScripts(input []Script) ([]Script, error) {
	scripts := make([]Script, len(input))
	seen := make(map[string]bool, len(input))
	for index, script := range input {
		if !scriptNamePattern.MatchString(script.Name) {
			return nil, fmt.Errorf("kitjs: invalid script name %q", script.Name)
		}
		if seen[script.Name] {
			return nil, fmt.Errorf("kitjs: duplicate script %q in graph", script.Name)
		}
		seen[script.Name] = true
		if err := validateClassicScript(script.Name, script.Source); err != nil {
			return nil, err
		}
		scripts[index] = Script{Name: script.Name, Source: append([]byte(nil), script.Source...)}
	}
	sort.Slice(scripts, func(left, right int) bool {
		return scripts[left].Name < scripts[right].Name
	})
	return scripts, nil
}

func validateClassicScript(name string, source []byte) error {
	if len(source) == 0 || source[0] != ';' {
		return fmt.Errorf("kitjs: script %q must begin with ';'", name)
	}
	if source[len(source)-1] != '\n' {
		return fmt.Errorf("kitjs: script %q must end with LF", name)
	}
	if bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(source, []byte{'\r'}) {
		return fmt.Errorf("kitjs: script %q must be UTF-8 without BOM and use LF", name)
	}
	return nil
}

func validateRuntimeFragment(name string, source []byte) error {
	if len(source) == 0 || source[0] != ';' {
		return fmt.Errorf("kitjs: %s must begin with ';'", name)
	}
	if source[len(source)-1] != '\n' {
		return fmt.Errorf("kitjs: %s must end with LF", name)
	}
	if bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(source, []byte{'\r'}) {
		return fmt.Errorf("kitjs: %s must be UTF-8 without BOM and use LF", name)
	}
	return nil
}

func exactSemVer(version string) bool {
	if version == "" || version != strings.TrimSpace(version) || strings.Count(version, "+") > 1 {
		return false
	}
	main := version
	if index := strings.IndexByte(main, '+'); index >= 0 {
		if !validIdentifiers(main[index+1:], false) {
			return false
		}
		main = main[:index]
	}
	if strings.Count(main, "-") > 0 {
		index := strings.IndexByte(main, '-')
		if !validIdentifiers(main[index+1:], true) {
			return false
		}
		main = main[:index]
	}
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !numericIdentifier(part) {
			return false
		}
	}
	return true
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, character := range identifier {
			if character < '0' || character > '9' {
				numeric = false
			}
			if character >= '0' && character <= '9' ||
				character >= 'A' && character <= 'Z' ||
				character >= 'a' && character <= 'z' || character == '-' {
				continue
			}
			return false
		}
		if rejectNumericLeadingZero && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func numericIdentifier(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func componentGraphID(profile Profile, runtime, serviceRuntime []byte, services []Service, components []ComponentVersion, componentRequires []ComponentServiceRequirement, scripts []Script) string {
	hash := sha256.New()
	writeHashFrame(hash, []byte("kitjs-package-graph-v2"))
	writeHashFrame(hash, []byte(ReleaseVersion))
	writeHashFrame(hash, []byte(profile))
	writeHashFrame(hash, []byte(ContentHash(runtime)))
	if len(serviceRuntime) > 0 {
		writeHashFrame(hash, []byte("service-runtime"))
		writeHashFrame(hash, []byte(ContentHash(serviceRuntime)))
	}
	for _, service := range services {
		writeHashFrame(hash, []byte("service"))
		writeHashFrame(hash, []byte(service.Name))
		writeHashFrame(hash, []byte(service.Version))
		for _, dependency := range service.Requires {
			writeHashFrame(hash, []byte("requires-service"))
			writeHashFrame(hash, []byte(dependency.Name))
			writeHashFrame(hash, []byte(dependency.Version))
		}
		writeHashFrame(hash, service.Source)
	}
	for _, component := range components {
		writeHashFrame(hash, []byte("component"))
		writeHashFrame(hash, []byte(component.Name))
		writeHashFrame(hash, []byte(component.Version))
	}
	for _, requirement := range componentRequires {
		writeHashFrame(hash, []byte("component-requires-service"))
		writeHashFrame(hash, []byte(requirement.Component))
		writeHashFrame(hash, []byte(requirement.Service.Name))
		writeHashFrame(hash, []byte(requirement.Service.Version))
	}
	for _, script := range scripts {
		writeHashFrame(hash, []byte("script"))
		writeHashFrame(hash, []byte(script.Name))
		writeHashFrame(hash, script.Source)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeHashFrame(output interface{ Write([]byte) (int, error) }, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = output.Write(size[:])
	_, _ = output.Write(value)
}

func componentGraphSource(profile Profile, graphID string, services []Service, components []ComponentVersion, scripts []Script) ([]byte, error) {
	profileJSON, err := json.Marshal(string(profile))
	if err != nil {
		return nil, err
	}
	graphJSON, err := json.Marshal(graphID)
	if err != nil {
		return nil, err
	}

	var output bytes.Buffer
	_, _ = output.WriteString("; (function (global, document) {\n")
	_, _ = output.WriteString("  \"use strict\";\n\n")
	_, _ = output.WriteString("  var ASSEMBLY = Symbol.for(\"kitjs:assembly\");\n")
	_, _ = output.WriteString("  var GRAPH = Symbol.for(\"kitjs:graph\");\n")
	_, _ = output.WriteString("  var core = document[ASSEMBLY];\n")
	_, _ = output.WriteString("  if (!core || core.phase !== ")
	expectedPhase := "events"
	if profile == ProfileHydrate {
		expectedPhase = "drive"
	}
	phaseJSON, _ := json.Marshal(expectedPhase)
	_, _ = output.Write(phaseJSON)
	_, _ = output.WriteString(") throw new Error(\"KitJS: component graph loaded out of order\");\n")
	_, _ = output.WriteString("  var services = Object.create(null);\n")
	for _, service := range services {
		name, _ := json.Marshal(service.Name)
		version, _ := json.Marshal(service.Version)
		_, _ = output.WriteString("  services[")
		_, _ = output.Write(name)
		_, _ = output.WriteString("] = ")
		_, _ = output.Write(version)
		_, _ = output.WriteString(";\n")
	}
	_, _ = output.WriteString("  var components = Object.create(null);\n")
	for _, component := range components {
		name, _ := json.Marshal(component.Name)
		version, _ := json.Marshal(component.Version)
		_, _ = output.WriteString("  components[")
		_, _ = output.Write(name)
		_, _ = output.WriteString("] = ")
		_, _ = output.Write(version)
		_, _ = output.WriteString(";\n")
	}
	_, _ = output.WriteString("  var graph = { id: ")
	_, _ = output.Write(graphJSON)
	_, _ = output.WriteString(", profile: ")
	_, _ = output.Write(profileJSON)
	_, _ = output.WriteString(", services: services, components: components };\n")
	_, _ = output.WriteString("  if (core.reuse) {\n")
	_, _ = output.WriteString("    var installed = global.kit && global.kit[GRAPH];\n")
	_, _ = output.WriteString("    if (!installed || installed.id !== graph.id || installed.profile !== graph.profile) {\n")
	_, _ = output.WriteString("      delete document[ASSEMBLY];\n")
	_, _ = output.WriteString("      throw new Error(\"KitJS: installed component graph does not match this artifact\");\n")
	_, _ = output.WriteString("    }\n")
	_, _ = output.WriteString("    return;\n")
	_, _ = output.WriteString("  }\n")
	_, _ = output.WriteString("  try {\n")
	_, _ = output.WriteString("    if (typeof core.installComponentGraph !== \"function\") throw new Error(\"KitJS: component graph installer is unavailable\");\n")
	_, _ = output.WriteString("    core.installComponentGraph(graph);\n")
	_, _ = output.WriteString("    var kit = core.kit;\n")
	_, _ = output.WriteString("    if (!kit || kit.version !== core.version || kit.component !== core.component) throw new Error(\"KitJS: package facade is unavailable\");\n")
	for _, service := range services {
		_, _ = output.WriteString("    ; (function (kit) {\n")
		_, _ = output.Write(service.Source)
		_, _ = output.WriteString("    })(kit);\n")
	}
	if len(services) > 0 {
		_, _ = output.WriteString("    if (typeof core.sealServices !== \"function\") throw new Error(\"KitJS: service graph sealer is unavailable\");\n")
		_, _ = output.WriteString("    core.sealServices();\n")
	} else {
		_, _ = output.WriteString("    if (typeof core.sealKit !== \"function\") throw new Error(\"KitJS: package facade sealer is unavailable\");\n")
		_, _ = output.WriteString("    core.sealKit();\n")
	}
	for _, script := range scripts {
		_, _ = output.WriteString("    ; (function (kit) {\n")
		_, _ = output.Write(script.Source)
		_, _ = output.WriteString("    })(kit);\n")
	}
	_, _ = output.WriteString("  } catch (error) {\n")
	_, _ = output.WriteString("    delete document[ASSEMBLY];\n")
	_, _ = output.WriteString("    throw error;\n")
	_, _ = output.WriteString("  }\n")
	_, _ = output.WriteString("})(globalThis, document);\n")
	return output.Bytes(), nil
}
