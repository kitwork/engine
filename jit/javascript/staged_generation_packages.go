package javascript

import (
	"fmt"
	"sort"
	"strings"
)

// stagedGenerationPackageCache owns candidate-only normalized package state.
// The immutable delivery catalog is read without detaching source, then every
// exact component/service is validated, copied, wrapped, and hashed at most
// once for the whole candidate generation. Nothing in this cache is published;
// AssetStore retains the complete candidate atomically only after all graphs
// and capacity checks succeed.
type stagedGenerationPackageCache struct {
	catalog *deliveryCatalog

	sharedNames      []string
	sharedSet        map[string]bool
	components       map[string]stagedPreparedComponentPackage
	services         map[string]stagedPreparedServicePackage
	sharedEntries    []stagedComponentArtifact
	componentsBundle *JITArtifact
	bundleBuilds     int
	artifacts        *jitArtifactAccumulator
	maxSourceBytes   int
	sourceBounded    bool
	sourceBytes      int
}

type stagedPreparedComponentPackage struct {
	component   normalizedComponentPackage
	requires    []ServiceVersion
	artifact    JITArtifact
	hasArtifact bool
}

type stagedPreparedServicePackage struct {
	service  Service
	artifact JITArtifact
}

type stagedPackageMaterializations struct {
	components int
	services   int
	bundles    int
}

func (cache *stagedGenerationPackageCache) materializations() stagedPackageMaterializations {
	if cache == nil {
		return stagedPackageMaterializations{}
	}
	return stagedPackageMaterializations{
		components: len(cache.components),
		services:   len(cache.services),
		bundles:    cache.bundleBuilds,
	}
}

func newStagedGenerationPackageCache(
	catalog *deliveryCatalog,
	sharedNames []string,
	artifacts *jitArtifactAccumulator,
	// A negative limit is reserved for internal canonical-parity assembly;
	// production preparation passes its exact remaining candidate byte budget,
	// where zero means no package source may be materialized.
	maxSourceBytes int,
) (*stagedGenerationPackageCache, error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: nil delivery catalog", ErrInvalidModule)
	}
	if maxSourceBytes < -1 {
		return nil, fmt.Errorf("%w: invalid package source limit", ErrInvalidModule)
	}
	if len(sharedNames) == 1 {
		return nil, fmt.Errorf("kitjs: shared components bundle requires at least two components")
	}
	names := append([]string(nil), sharedNames...)
	sort.Strings(names)
	sharedSet := make(map[string]bool, len(names))
	for _, name := range names {
		if sharedSet[name] {
			return nil, fmt.Errorf("kitjs: shared components bundle repeats %q", name)
		}
		sharedSet[name] = true
	}
	return &stagedGenerationPackageCache{
		catalog:        catalog,
		sharedNames:    names,
		sharedSet:      sharedSet,
		components:     make(map[string]stagedPreparedComponentPackage),
		services:       make(map[string]stagedPreparedServicePackage),
		artifacts:      artifacts,
		maxSourceBytes: maxSourceBytes,
		sourceBounded:  maxSourceBytes >= 0,
	}, nil
}

func stagedExactPackageKey(name, version string) string {
	return name + "\x00" + version
}

func (cache *stagedGenerationPackageCache) reserveSource(size int) error {
	if size < 0 || (cache.sourceBounded && size > cache.maxSourceBytes-cache.sourceBytes) {
		return fmt.Errorf("%w: candidate package source current=%d add=%d limit=%d",
			ErrAssetCapacity, cache.sourceBytes, size, cache.maxSourceBytes)
	}
	cache.sourceBytes += size
	return nil
}

func (cache *stagedGenerationPackageCache) addArtifact(artifact JITArtifact) error {
	if cache.artifacts == nil {
		return nil
	}
	return cache.artifacts.Add([]JITArtifact{artifact})
}

func (cache *stagedGenerationPackageCache) prepareComponent(identity ComponentVersion) (stagedPreparedComponentPackage, error) {
	key := stagedExactPackageKey(identity.Name, identity.Version)
	if prepared, exists := cache.components[key]; exists {
		return prepared, nil
	}
	component, err := cache.catalog.componentView(identity)
	if err != nil {
		return stagedPreparedComponentPackage{}, err
	}
	if err := cache.reserveSource(len(component.source)); err != nil {
		return stagedPreparedComponentPackage{}, err
	}
	normalized, err := normalizeComponentPackages([]ComponentPackage{{
		Name:    component.identity.Name,
		Version: component.identity.Version,
		Source:  component.source,
	}})
	if err != nil {
		return stagedPreparedComponentPackage{}, err
	}
	prepared := stagedPreparedComponentPackage{
		component: normalized[0],
		requires:  append([]ServiceVersion(nil), component.requires...),
	}
	if !cache.sharedSet[identity.Name] {
		source, sourceErr := stagedComponentSource(prepared.component)
		if sourceErr != nil {
			return stagedPreparedComponentPackage{}, sourceErr
		}
		artifact, artifactErr := newJITArtifact(JITRoleComponent, identity.Name, identity.Version, identity.Name, source)
		if artifactErr != nil {
			return stagedPreparedComponentPackage{}, artifactErr
		}
		if artifactErr := cache.addArtifact(artifact); artifactErr != nil {
			return stagedPreparedComponentPackage{}, artifactErr
		}
		prepared.artifact = artifact
		prepared.hasArtifact = true
	}
	cache.components[key] = prepared
	return prepared, nil
}

func (cache *stagedGenerationPackageCache) prepareService(identity ServiceVersion) (stagedPreparedServicePackage, error) {
	key := stagedExactPackageKey(identity.Name, identity.Version)
	if prepared, exists := cache.services[key]; exists {
		return prepared, nil
	}
	service, err := cache.catalog.serviceView(identity)
	if err != nil {
		return stagedPreparedServicePackage{}, err
	}
	if err := cache.reserveSource(len(service.source)); err != nil {
		return stagedPreparedServicePackage{}, err
	}
	normalized, err := normalizeServiceDefinition(Service{
		Name:     service.identity.Name,
		Version:  service.identity.Version,
		Requires: service.requires,
		Actions:  service.actions,
		Source:   service.source,
	})
	if err != nil {
		return stagedPreparedServicePackage{}, err
	}
	source, err := stagedServiceSource(normalized)
	if err != nil {
		return stagedPreparedServicePackage{}, err
	}
	artifact, err := newJITArtifact(JITRoleService, identity.Name, identity.Version, identity.Name, source)
	if err != nil {
		return stagedPreparedServicePackage{}, err
	}
	if err := cache.addArtifact(artifact); err != nil {
		return stagedPreparedServicePackage{}, err
	}
	prepared := stagedPreparedServicePackage{service: normalized, artifact: artifact}
	cache.services[key] = prepared
	return prepared, nil
}

func (cache *stagedGenerationPackageCache) selectedServices(roots []ServiceVersion) ([]Service, []JITArtifact, error) {
	selected := make(map[string]stagedPreparedServicePackage)
	var collect func(ServiceVersion) error
	collect = func(identity ServiceVersion) error {
		if prior, exists := selected[identity.Name]; exists {
			if prior.service.Version != identity.Version {
				return fmt.Errorf("%w: service %s requests %s and %s",
					ErrVersionConflict, identity.Name, prior.service.Version, identity.Version)
			}
			return nil
		}
		prepared, err := cache.prepareService(identity)
		if err != nil {
			return err
		}
		selected[identity.Name] = prepared
		for _, dependency := range prepared.service.Requires {
			if err := collect(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := collect(root); err != nil {
			return nil, nil, err
		}
	}

	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	ordered := make([]stagedPreparedServicePackage, 0, len(names))
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
		for _, dependency := range selected[name].service.Requires {
			dependencyService, exists := selected[dependency.Name]
			if !exists {
				return fmt.Errorf("kitjs: service %s@%s requires missing service %s@%s",
					name, selected[name].service.Version, dependency.Name, dependency.Version)
			}
			if dependencyService.service.Version != dependency.Version {
				return fmt.Errorf("kitjs: service %s@%s requires service %s@%s but graph provides %s",
					name, selected[name].service.Version, dependency.Name, dependency.Version, dependencyService.service.Version)
			}
			if err := visit(dependency.Name); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		ordered = append(ordered, selected[name])
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, nil, err
		}
	}

	services := make([]Service, len(ordered))
	artifacts := make([]JITArtifact, len(ordered))
	for index, prepared := range ordered {
		services[index] = prepared.service
		artifacts[index] = prepared.artifact
	}
	return services, artifacts, nil
}

func (cache *stagedGenerationPackageCache) ensureComponentsBundle(selected map[string]stagedPreparedComponentPackage) (*JITArtifact, error) {
	if len(cache.sharedNames) == 0 {
		return nil, nil
	}
	shared := make([]stagedComponentArtifact, len(cache.sharedNames))
	for index, name := range cache.sharedNames {
		prepared, exists := selected[name]
		if !exists {
			return nil, fmt.Errorf("kitjs: shared components bundle references missing component %q", name)
		}
		shared[index] = stagedComponentArtifact{component: prepared.component}
	}
	if cache.componentsBundle != nil {
		for index := range shared {
			if shared[index].component.identity != cache.sharedEntries[index].component.identity ||
				shared[index].component.sourceHash != cache.sharedEntries[index].component.sourceHash {
				return nil, fmt.Errorf("%w: shared component identity changed during generation", ErrInvalidBundle)
			}
		}
		return cache.componentsBundle, nil
	}
	source, err := stagedComponentsBundleSource(shared)
	if err != nil {
		return nil, err
	}
	artifact, err := newJITArtifact(JITRoleComponents, "", "", "components", source)
	if err != nil {
		return nil, err
	}
	if err := cache.addArtifact(artifact); err != nil {
		return nil, err
	}
	cache.sharedEntries = shared
	cache.componentsBundle = &artifact
	cache.bundleBuilds++
	return cache.componentsBundle, nil
}

func (cache *stagedGenerationPackageCache) build(
	profile Profile,
	core *stagedCoreArtifacts,
	componentIdentities []ComponentVersion,
) (StagedAssembly, error) {
	if core == nil || core.profile != profile {
		return StagedAssembly{}, fmt.Errorf("kitjs: prepared staged core does not match build profile")
	}
	components, err := normalizeComponents(componentIdentities)
	if err != nil {
		return StagedAssembly{}, err
	}
	if len(components) > stagedComponentCacheLimit {
		return StagedAssembly{}, fmt.Errorf("kitjs: staged component graph exceeds cache limit %d", stagedComponentCacheLimit)
	}

	normalized := make([]normalizedComponentPackage, 0, len(components))
	selected := make(map[string]stagedPreparedComponentPackage, len(components))
	individualCapacity := len(components) - len(cache.sharedNames)
	if individualCapacity < 0 {
		return StagedAssembly{}, fmt.Errorf("%w: shared component set exceeds document graph", ErrInvalidBundle)
	}
	individual := make([]stagedComponentArtifact, 0, individualCapacity)
	requirements := make([]ComponentServiceRequirement, 0, len(components))
	serviceRoots := make([]ServiceVersion, 0, len(components))
	for _, identity := range components {
		prepared, prepareErr := cache.prepareComponent(identity)
		if prepareErr != nil {
			return StagedAssembly{}, prepareErr
		}
		selected[identity.Name] = prepared
		normalized = append(normalized, prepared.component)
		for _, dependency := range prepared.requires {
			requirements = append(requirements, ComponentServiceRequirement{
				Component: identity.Name,
				Service:   dependency,
			})
			serviceRoots = append(serviceRoots, dependency)
		}
		if cache.sharedSet[identity.Name] {
			continue
		}
		if !prepared.hasArtifact {
			return StagedAssembly{}, fmt.Errorf("%w: component %s@%s has no staged artifact",
				ErrInvalidBundle, identity.Name, identity.Version)
		}
		individual = append(individual, stagedComponentArtifact{
			component: prepared.component,
			artifact:  prepared.artifact,
		})
	}

	componentsBundle, err := cache.ensureComponentsBundle(selected)
	if err != nil {
		return StagedAssembly{}, err
	}
	services, serviceArtifacts, err := cache.selectedServices(serviceRoots)
	if err != nil {
		return StagedAssembly{}, err
	}
	if err := validateDocumentOwners(components, services); err != nil {
		return StagedAssembly{}, err
	}
	requirements, err = normalizeComponentServiceRequirements(requirements, components, services)
	if err != nil {
		return StagedAssembly{}, err
	}

	runtime := core.runtime
	var hydrate *JITArtifact
	if core.hydrate != nil {
		copy := *core.hydrate
		hydrate = &copy
	}
	return assemblePreparedStaged(profile, runtime, hydrate, services, serviceArtifacts,
		normalized, requirements, cache.sharedEntries, componentsBundle, individual)
}
