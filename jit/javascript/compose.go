package javascript

import (
	"fmt"
	"sort"
)

// Bundle is the compatibility delivery shape consumed by generation-owned
// asset stores. Its bytes and identity come directly from one Build Artifact.
type Bundle struct {
	JavaScript  []byte
	ContentHash string
	Profile     Profile
	Release     string
}

func (bundle Bundle) Empty() bool { return len(bundle.JavaScript) == 0 }

// Composer adapts HTML scan results to the flattened KitJS Build API. It owns
// no mutable selection state and is safe for concurrent generation preparation.
type Composer struct {
	catalog *deliveryCatalog
}

func NewDefaultComposer(tenantComponents ...ComponentPackage) (*Composer, error) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		return nil, err
	}
	if err := catalog.addComponentPackages(tenantComponents); err != nil {
		return nil, err
	}
	return &Composer{catalog: catalog}, nil
}

func (composer *Composer) valid() bool {
	return composer != nil && composer.catalog != nil
}

// HasManagedComponent reports whether name belongs to either the immutable
// embedded catalog or this generation's tenant overlay.
func (composer *Composer) HasManagedComponent(name string) bool {
	if !composer.valid() {
		return false
	}
	_, exists := composer.catalog.components[name]
	return exists
}

// ComposeHTML scans authored use and emits the exact immutable KitJS artifact.
func (composer *Composer) ComposeHTML(source []byte) (Bundle, error) {
	if !composer.valid() {
		return Bundle{}, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	use, err := ScanHTML(source)
	if err != nil {
		return Bundle{}, err
	}
	return composer.composeUse(use, ProfileKit)
}

// ComposeStandalone emits a core profile even when no component is selected.
func (composer *Composer) ComposeStandalone(components []ComponentRef, drive bool) (Bundle, error) {
	if !composer.valid() {
		return Bundle{}, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	profile := ProfileKit
	if drive {
		profile = ProfileHydrate
	}
	return composer.composeUse(ScanResult{
		Components:   append([]ComponentRef(nil), components...),
		NeedsRuntime: true,
	}, profile)
}

func (composer *Composer) composeUse(use ScanResult, profile Profile) (Bundle, error) {
	if err := composer.validateLocalComponents(use.LocalComponents); err != nil {
		return Bundle{}, err
	}
	if !use.NeedsRuntime {
		return Bundle{}, nil
	}
	components, requirements, scripts, services, err := composer.closePackages(use.Components)
	if err != nil {
		return Bundle{}, err
	}
	artifact, err := Build(BuildOptions{
		Profile:           profile,
		Services:          services,
		Components:        components,
		ComponentRequires: requirements,
		Scripts:           scripts,
	})
	if err != nil {
		return Bundle{}, fmt.Errorf("compose KitJS %s artifact: %w", profile, err)
	}
	return bundleFromArtifact(artifact), nil
}

// stagedBuildOptions closes the same catalog graph as the standalone builder,
// but preserves each component source as its own package for content-addressed
// Kitwork delivery. The caller owns profile selection and any shared bundle
// policy; authored HTML never chooses either.
func (composer *Composer) stagedBuildOptions(
	use ScanResult,
	profile Profile,
	sharedComponentNames []string,
) (StagedBuildOptions, error) {
	if !composer.valid() {
		return StagedBuildOptions{}, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	if err := composer.validateLocalComponents(use.LocalComponents); err != nil {
		return StagedBuildOptions{}, err
	}
	components, requirements, scripts, services, err := composer.closePackages(use.Components)
	if err != nil {
		return StagedBuildOptions{}, err
	}
	if len(components) != len(scripts) {
		return StagedBuildOptions{}, fmt.Errorf("%w: component package source mismatch", ErrInvalidModule)
	}
	packages := make([]ComponentPackage, len(components))
	for index, component := range components {
		packages[index] = ComponentPackage{
			Name:    component.Name,
			Version: component.Version,
			Source:  append([]byte(nil), scripts[index].Source...),
		}
	}
	return StagedBuildOptions{
		Profile:              profile,
		Services:             services,
		Components:           packages,
		ComponentRequires:    requirements,
		SharedComponentNames: append([]string(nil), sharedComponentNames...),
	}, nil
}

func (composer *Composer) validateLocalComponents(references []ComponentRef) error {
	if !composer.valid() {
		return fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	for _, reference := range references {
		if composer.HasManagedComponent(reference.Name) {
			return fmt.Errorf("%w at byte %d: client component %q shadows the managed catalog",
				ErrInvalidComponentUse, reference.Offset, reference.Name)
		}
	}
	return nil
}

func bundleFromArtifact(artifact Artifact) Bundle {
	return Bundle{
		JavaScript:  artifact.Bytes(),
		ContentHash: artifact.SHA256(),
		Profile:     artifact.Profile(),
		Release:     artifact.Release(),
	}
}

func (composer *Composer) closePackages(references []ComponentRef) ([]ComponentVersion, []ComponentServiceRequirement, []Script, []Service, error) {
	selected := make(map[string]catalogComponent, len(references))
	for _, reference := range references {
		component, err := composer.catalog.component(reference.Name, reference.Version)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if prior, exists := selected[component.identity.Name]; exists {
			if prior.identity.Version != component.identity.Version {
				return nil, nil, nil, nil, fmt.Errorf("%w: %s requests %s and %s",
					ErrVersionConflict, component.identity.Name, prior.identity.Version, component.identity.Version)
			}
			continue
		}
		selected[component.identity.Name] = component
	}

	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	components := make([]ComponentVersion, 0, len(names))
	requirements := make([]ComponentServiceRequirement, 0, len(names))
	scripts := make([]Script, 0, len(names))
	serviceRoots := make([]ServiceVersion, 0, len(names))
	for _, name := range names {
		component := selected[name]
		components = append(components, component.identity)
		scripts = append(scripts, Script{
			Name:   "component." + component.identity.Name + "." + component.identity.Version,
			Source: append([]byte(nil), component.source...),
		})
		for _, dependency := range component.requires {
			requirements = append(requirements, ComponentServiceRequirement{
				Component: component.identity.Name,
				Service:   dependency,
			})
			serviceRoots = append(serviceRoots, dependency)
		}
	}
	services, err := composer.closeServices(serviceRoots)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return components, requirements, scripts, services, nil
}

func (composer *Composer) closeServices(roots []ServiceVersion) ([]Service, error) {
	selected := make(map[string]catalogService)
	state := make(map[string]uint8)
	var visit func(ServiceVersion) error
	visit = func(identity ServiceVersion) error {
		if prior, exists := selected[identity.Name]; exists && prior.identity.Version != identity.Version {
			return fmt.Errorf("%w: service %s requests %s and %s",
				ErrVersionConflict, identity.Name, prior.identity.Version, identity.Version)
		}
		switch state[identity.Name] {
		case 1:
			return fmt.Errorf("%w involving %s@%s", ErrDependencyCycle, identity.Name, identity.Version)
		case 2:
			return nil
		}
		service, err := composer.catalog.service(identity)
		if err != nil {
			return err
		}
		state[identity.Name] = 1
		for _, dependency := range service.requires {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[identity.Name] = 2
		selected[identity.Name] = service
		return nil
	}
	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	services := make([]Service, 0, len(names))
	for _, name := range names {
		service := selected[name]
		services = append(services, Service{
			Name:     service.identity.Name,
			Version:  service.identity.Version,
			Requires: append([]ServiceVersion(nil), service.requires...),
			Actions:  append([]string(nil), service.actions...),
			Source:   append([]byte(nil), service.source...),
		})
	}
	return services, nil
}
