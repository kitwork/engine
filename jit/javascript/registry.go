package javascript

import (
	"errors"
	"fmt"
	"sort"
)

// Delivery errors remain stable for the HTML scanner and generation-preparation
// callers. The old independently executable Module/Registry graph is gone;
// package selection now adapts authored component references to BuildOptions.
var (
	ErrInvalidModule        = errors.New("kitjs: invalid delivery package")
	ErrModuleNotFound       = errors.New("kitjs: component package not found")
	ErrVersionConflict      = errors.New("kitjs: one version per component name")
	ErrDependencyCycle      = errors.New("kitjs: service dependency cycle")
	ErrInvalidComponentUse  = errors.New("kitjs: invalid component use")
	ErrUnsupportedAttribute = errors.New("kitjs: unsupported reserved attribute")
	ErrComponentShadow      = errors.New("kitjs: tenant component shadows managed component")
)

type catalogService struct {
	identity ServiceVersion
	requires []ServiceVersion
	actions  []string
	source   []byte
}

type catalogComponent struct {
	identity ComponentVersion
	requires []ServiceVersion
	source   []byte
}

// deliveryCatalog is the small engine adapter catalog for the flattened KitJS
// release. It does not duplicate the runtime graph: Build remains the only
// assembler and validates the closed set selected here.
type deliveryCatalog struct {
	services         map[string]catalogService
	components       map[string]map[string]catalogComponent
	componentDefault map[string]string
}

// authoredServiceActions is the single engine-side policy for the service
// methods the canonical app component may expose to authored actions.
var authoredServiceActions = map[string][]string{
	"announce":   {"say", "polite", "assertive", "clear"},
	"appearance": {"set", "toggle", "system"},
	"clipboard":  {"writeText"},
	"cookie":     {"set", "remove"},
	"fullscreen": {"request", "exit"},
	"navigation": {"back", "forward", "reload"},
	"progress":   {"start", "update", "finish"},
	"share":      {"open"},
	"storage":    {"set", "remove"},
}

func validAuthoredServiceAction(service, method string) bool {
	for _, allowed := range authoredServiceActions[service] {
		if method == allowed {
			return true
		}
	}
	return false
}

func authoredAppServices(version string) []ServiceVersion {
	names := make([]string, 0, len(authoredServiceActions))
	for name := range authoredServiceActions {
		if version == "1.0.0" && name == "progress" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	services := make([]ServiceVersion, len(names))
	for index, name := range names {
		services[index] = ServiceVersion{Name: name, Version: "1.0.0"}
	}
	return services
}

func appGrantsAuthoredService(version, service string) bool {
	if version == "" {
		version = "1.1.0"
	}
	if version == "1.0.0" && service == "progress" {
		return false
	}
	_, exists := authoredServiceActions[service]
	return exists
}

func loadDeliveryCatalog() (*deliveryCatalog, error) {
	serviceRequires := map[string][]ServiceVersion{
		"request": {{Name: "progress", Version: "1.0.0"}},
		"share":   {{Name: "clipboard", Version: "1.0.0"}},
	}
	serviceNames := []string{
		"announce", "appearance", "clipboard", "cookie", "fullscreen", "navigation",
		"network", "progress", "request", "share", "storage",
	}
	catalog := &deliveryCatalog{
		services:   make(map[string]catalogService, len(serviceNames)),
		components: make(map[string]map[string]catalogComponent),
		componentDefault: map[string]string{
			"accordion":    "1.0.0",
			"app":          "1.1.0",
			"alert":        "1.0.0",
			"carousel":     "1.0.0",
			"dialog":       "1.0.0",
			"drawer":       "1.0.0",
			"dropdown":     "1.0.0",
			"pagination":   "1.0.0",
			"popover":      "1.0.0",
			"progress-bar": "2.0.0",
			"shortcut":     "1.0.0",
			"switch":       "1.0.0",
			"tabs":         "1.0.0",
			"theme":        "3.0.0",
			"toast":        "1.0.0",
			"tooltip":      "1.0.0",
		},
	}
	for _, name := range serviceNames {
		path := "service/" + name + "/1.0.0.js"
		source, err := embeddedDeliveryPackages.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidModule, path, err)
		}
		catalog.services[name] = catalogService{
			identity: ServiceVersion{Name: name, Version: "1.0.0"},
			requires: append([]ServiceVersion(nil), serviceRequires[name]...),
			actions:  append([]string(nil), authoredServiceActions[name]...),
			source:   append([]byte(nil), source...),
		}
	}

	progressVersions := []string{"1.1.0", "1.2.0", "2.0.0"}
	catalog.components["progress-bar"] = make(map[string]catalogComponent, len(progressVersions))
	for _, version := range progressVersions {
		path := "component/progress-bar/" + version + ".js"
		source, err := embeddedDeliveryPackages.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidModule, path, err)
		}
		catalog.components["progress-bar"][version] = catalogComponent{
			identity: ComponentVersion{Name: "progress-bar", Version: version},
			requires: []ServiceVersion{{Name: "progress", Version: "1.0.0"}},
			source:   append([]byte(nil), source...),
		}
	}
	for _, name := range []string{
		"accordion", "alert", "carousel", "dialog", "drawer", "dropdown",
		"pagination", "popover", "shortcut", "switch", "tabs", "toast", "tooltip",
	} {
		path := "component/" + name + "/1.0.0.js"
		source, err := embeddedDeliveryPackages.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidModule, path, err)
		}
		catalog.components[name] = map[string]catalogComponent{
			"1.0.0": {
				identity: ComponentVersion{Name: name, Version: "1.0.0"},
				source:   append([]byte(nil), source...),
			},
		}
	}
	appSource100, err := embeddedDeliveryPackages.ReadFile("component/app/1.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.0.0.js: %v", ErrInvalidModule, err)
	}
	appSource110, err := embeddedDeliveryPackages.ReadFile("component/app/1.1.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.1.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["app"] = map[string]catalogComponent{
		"1.0.0": {
			identity: ComponentVersion{Name: "app", Version: "1.0.0"},
			requires: authoredAppServices("1.0.0"),
			source:   append([]byte(nil), appSource100...),
		},
		"1.1.0": {
			identity: ComponentVersion{Name: "app", Version: "1.1.0"},
			requires: authoredAppServices("1.1.0"),
			source:   append([]byte(nil), appSource110...),
		},
	}
	themeSource, err := embeddedDeliveryPackages.ReadFile("component/theme/2.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/theme/2.0.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["theme"] = map[string]catalogComponent{
		"2.0.0": {
			identity: ComponentVersion{Name: "theme", Version: "2.0.0"},
			source:   append([]byte(nil), themeSource...),
		},
	}
	themeSource, err = embeddedDeliveryPackages.ReadFile("component/theme/3.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/theme/3.0.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["theme"]["3.0.0"] = catalogComponent{
		identity: ComponentVersion{Name: "theme", Version: "3.0.0"},
		requires: []ServiceVersion{{Name: "appearance", Version: "1.0.0"}},
		source:   append([]byte(nil), themeSource...),
	}
	for _, name := range []string{"dialog", "dropdown", "tabs"} {
		path := "component/" + name + "/2.0.0.js"
		source, err := embeddedDeliveryPackages.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidModule, path, err)
		}
		catalog.components[name]["2.0.0"] = catalogComponent{
			identity: ComponentVersion{Name: name, Version: "2.0.0"},
			source:   append([]byte(nil), source...),
		}
	}
	return catalog, nil
}

// addComponentPackages overlays one detached tenant catalog on this composer.
// Every name has exactly one version so an omitted data-kit-version resolves
// deterministically, while an explicit matching exact version remains valid.
func (catalog *deliveryCatalog) addComponentPackages(packages []ComponentPackage) error {
	if catalog == nil {
		return fmt.Errorf("%w: nil delivery catalog", ErrInvalidModule)
	}
	if len(packages) == 0 {
		return nil
	}
	if len(packages) > stagedComponentCacheLimit {
		return fmt.Errorf("%w: tenant catalog has %d components (limit %d)",
			ErrInvalidModule, len(packages), stagedComponentCacheLimit)
	}
	components, err := normalizeComponentPackages(packages)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidModule, err)
	}
	for _, component := range components {
		name := component.identity.Name
		if _, exists := catalog.components[name]; exists {
			return fmt.Errorf("%w: %s", ErrComponentShadow, name)
		}
	}
	for _, component := range components {
		identity := component.identity
		catalog.components[identity.Name] = map[string]catalogComponent{
			identity.Version: {
				identity: identity,
				source:   append([]byte(nil), component.source...),
			},
		}
		catalog.componentDefault[identity.Name] = identity.Version
	}
	return nil
}

func (catalog *deliveryCatalog) component(name, version string) (catalogComponent, error) {
	versions, exists := catalog.components[name]
	if !exists {
		return catalogComponent{}, fmt.Errorf("%w: %s", ErrModuleNotFound, name)
	}
	if version == "" {
		version = catalog.componentDefault[name]
	}
	component, exists := versions[version]
	if !exists {
		return catalogComponent{}, fmt.Errorf("%w: %s@%s", ErrModuleNotFound, name, version)
	}
	component.requires = append([]ServiceVersion(nil), component.requires...)
	component.source = append([]byte(nil), component.source...)
	return component, nil
}

func (catalog *deliveryCatalog) service(identity ServiceVersion) (catalogService, error) {
	service, exists := catalog.services[identity.Name]
	if !exists || service.identity.Version != identity.Version {
		return catalogService{}, fmt.Errorf("%w: required service %s@%s", ErrModuleNotFound, identity.Name, identity.Version)
	}
	service.requires = append([]ServiceVersion(nil), service.requires...)
	service.actions = append([]string(nil), service.actions...)
	service.source = append([]byte(nil), service.source...)
	return service, nil
}
