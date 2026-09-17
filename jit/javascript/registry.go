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
	services         map[string]map[string]catalogService
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

// sealedExpressionServiceNames keeps every non-authored KitJS namespace visible
// to the expression scanner. Without this closed list, `$app.<service>` could be
// misclassified as an ordinary component field before the authored-action
// policy gets a chance to reject it.
var sealedExpressionServiceNames = map[string]struct{}{
	"biometric": {}, "geolocation": {}, "nfc": {},
	"camera": {}, "capabilities": {}, "device": {}, "files": {},
	"deepLinks": {}, "lifecycle": {}, "media": {}, "network": {}, "notifications": {}, "qr": {},
	"request": {}, "secureStorage": {}, "shell": {}, "studioDatabase": {}, "studioSqlite": {}, "studioState": {}, "wakeLock": {},
	"window": {},
}

// sealedApp110Services are trusted KitJS namespaces selected by canonical app
// packages beginning with app@1.1.0. They intentionally have no authored
// actions: component JavaScript may use window.kit, while HTML expressions and
// the $app facade remain default-deny.
var sealedApp110Services = []string{
	"capabilities", "device", "files", "network", "secureStorage", "shell",
}

// sealedApp140Services are the additional native namespaces first selected by
// app@1.4.0. They remain absent from authored action expressions.
var sealedApp140Services = []string{"camera"}

// sealedApp150Services are the convenience namespaces first selected by
// app@1.5.0. QR owns one private native operation while media delegates to the
// exact files service; neither is callable from authored action expressions.
var sealedApp150Services = []string{"media", "qr"}

// sealedApp160Services add a lifecycle-bound screen wake lock. The namespace
// remains unavailable to authored HTML actions; trusted component code owns
// the explicit request and release lifecycle.
var sealedApp160Services = []string{"wakeLock"}

// sealedApp170Services add bounded immediate local notifications. Permission
// prompts and delivery stay behind trusted component code and a v6 native
// manifest grant; authored HTML actions receive no notification authority.
var sealedApp170Services = []string{"notifications"}

// sealedApp180Services add informational browser lifecycle state and bounded
// same-app deep-link receipt. Deep-link snapshots remain behind a v7 native
// manifest grant; neither namespace is callable from authored HTML actions.
var sealedApp180Services = []string{"deepLinks", "lifecycle"}

// sealedApp1110Services add the desktop-only, picker-mediated SQLite inspector.
// Its opaque native handles remain inside the trusted service package and the
// namespace is never callable from authored HTML expressions.
var sealedApp1110Services = []string{"studioSqlite"}

// sealedApp1120Services add bounded read-only PostgreSQL/MySQL inspection.
// Credentials and opaque connection handles remain inside trusted JavaScript
// and the desktop host; authored HTML receives no database authority.
var sealedApp1120Services = []string{"studioDatabase"}

// sealedApp1130Services add host-owned persistent Studio metadata. The
// service strips credential references and stays unavailable to authored HTML.
var sealedApp1130Services = []string{"studioState"}

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

func appComponentServices(version string) []ServiceVersion {
	if version == "1.17.0" {
		services := appComponentServices("1.16.0")
		for index := range services {
			if services[index].Name == "studioSqlite" {
				services[index].Version = "1.1.0"
			}
		}
		return services
	}
	if version == "1.16.0" {
		services := appComponentServices("1.15.0")
		for index := range services {
			if services[index].Name == "notifications" {
				services[index].Version = "1.2.0"
			}
		}
		for _, name := range []string{"biometric", "geolocation", "nfc"} {
			services = append(services, ServiceVersion{Name: name, Version: "1.0.0"})
		}
		sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
		return services
	}
	if version == "1.15.0" {
		services := appComponentServices("1.14.0")
		for index := range services {
			switch services[index].Name {
			case "network", "camera", "media":
				services[index].Version = "1.1.0"
			case "files":
				services[index].Version = "1.4.0"
			}
		}
		return services
	}
	services := authoredAppServices(version)
	if version != "1.1.0" && version != "1.2.0" && version != "1.3.0" && version != "1.4.0" && version != "1.5.0" && version != "1.6.0" && version != "1.7.0" && version != "1.8.0" && version != "1.9.0" && version != "1.10.0" && version != "1.11.0" && version != "1.12.0" && version != "1.13.0" && version != "1.14.0" {
		return services
	}
	names := make(map[string]bool, len(services)+len(sealedApp110Services)+len(sealedApp140Services)+len(sealedApp150Services)+len(sealedApp160Services)+len(sealedApp170Services)+len(sealedApp180Services)+len(sealedApp1110Services)+len(sealedApp1120Services)+len(sealedApp1130Services))
	for _, service := range services {
		names[service.Name] = true
	}
	for _, name := range sealedApp110Services {
		names[name] = true
	}
	if version == "1.4.0" || version == "1.5.0" || version == "1.6.0" || version == "1.7.0" || version == "1.8.0" || version == "1.9.0" || version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp140Services {
			names[name] = true
		}
	}
	if version == "1.5.0" || version == "1.6.0" || version == "1.7.0" || version == "1.8.0" || version == "1.9.0" || version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp150Services {
			names[name] = true
		}
	}
	if version == "1.6.0" || version == "1.7.0" || version == "1.8.0" || version == "1.9.0" || version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp160Services {
			names[name] = true
		}
	}
	if version == "1.7.0" || version == "1.8.0" || version == "1.9.0" || version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp170Services {
			names[name] = true
		}
	}
	if version == "1.8.0" || version == "1.9.0" || version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp180Services {
			names[name] = true
		}
	}
	if version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp1110Services {
			names[name] = true
		}
	}
	if version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp1120Services {
			names[name] = true
		}
	}
	if version == "1.13.0" || version == "1.14.0" {
		for _, name := range sealedApp1130Services {
			names[name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	output := make([]ServiceVersion, len(ordered))
	for index, name := range ordered {
		serviceVersion := "1.0.0"
		if name == "files" {
			if version == "1.2.0" {
				serviceVersion = "1.1.0"
			} else if version == "1.3.0" {
				serviceVersion = "1.2.0"
			} else if version == "1.4.0" || version == "1.5.0" || version == "1.6.0" || version == "1.7.0" || version == "1.8.0" || version == "1.9.0" || version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" {
				serviceVersion = "1.3.0"
			}
		}
		if name == "notifications" && (version == "1.9.0" || version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0") {
			serviceVersion = "1.1.0"
		}
		if name == "deepLinks" && (version == "1.10.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0") {
			serviceVersion = "1.1.0"
		}
		if name == "studioDatabase" && version == "1.14.0" {
			serviceVersion = "1.1.0"
		}
		output[index] = ServiceVersion{Name: name, Version: serviceVersion}
	}
	return output
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
		"biometric":   {{Name: "capabilities", Version: "1.0.0"}},
		"geolocation": {{Name: "capabilities", Version: "1.0.0"}},
		"nfc":         {{Name: "capabilities", Version: "1.0.0"}},
		"camera":      {{Name: "capabilities", Version: "1.0.0"}, {Name: "files", Version: "1.3.0"}},
		"media":       {{Name: "files", Version: "1.3.0"}},
		"qr":          {{Name: "capabilities", Version: "1.0.0"}},
		"request":     {{Name: "progress", Version: "1.0.0"}},
		"share":       {{Name: "clipboard", Version: "1.0.0"}},
	}
	serviceNames := []string{
		"biometric", "geolocation", "nfc",
		"announce", "appearance", "camera", "capabilities", "clipboard", "cookie", "device", "files", "fullscreen", "navigation",
		"deepLinks", "lifecycle", "media", "network", "notifications", "progress", "qr", "request", "secureStorage", "share", "shell", "storage", "studioDatabase", "studioSqlite", "studioState", "wakeLock", "window",
	}
	catalog := &deliveryCatalog{
		services:   make(map[string]map[string]catalogService, len(serviceNames)),
		components: make(map[string]map[string]catalogComponent),
		componentDefault: map[string]string{
			"accordion":        "1.0.0",
			"app":              "1.1.0",
			"alert":            "1.0.0",
			"capability-lab":   "1.0.0",
			"carousel":         "1.0.0",
			"collapse":         "1.0.0",
			"combobox":         "1.0.0",
			"copy":             "1.0.0",
			"desktop-titlebar": "1.0.0",
			"dialog":           "1.0.0",
			"drawer":           "1.0.0",
			"dropzone":         "1.0.0",
			"dropdown":         "1.0.0",
			"otp":              "1.0.0",
			"pagination":       "1.0.0",
			"popover":          "1.0.0",
			"progress-bar":     "2.0.0",
			"rating":           "1.0.0",
			"rotator":          "1.0.0",
			"scrolled":         "1.0.0",
			"shortcut":         "1.0.0",
			"slider":           "1.0.0",
			"stepper":          "1.0.0",
			"switch":           "1.0.0",
			"tabs":             "1.0.0",
			"tags":             "1.0.0",
			"terminal":         "1.0.0",
			"theme":            "3.0.0",
			"toast":            "1.0.0",
			"tooltip":          "1.0.0",
		},
	}
	for _, name := range serviceNames {
		path := "service/" + name + "/1.0.0.js"
		source, err := embeddedDeliveryPackages.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidModule, path, err)
		}
		catalog.services[name] = map[string]catalogService{"1.0.0": {
			identity: ServiceVersion{Name: name, Version: "1.0.0"},
			requires: append([]ServiceVersion(nil), serviceRequires[name]...),
			actions:  append([]string(nil), authoredServiceActions[name]...),
			source:   append([]byte(nil), source...),
		}}
	}
	notificationsSource110, err := embeddedDeliveryPackages.ReadFile("service/notifications/1.1.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read service/notifications/1.1.0.js: %v", ErrInvalidModule, err)
	}
	catalog.services["notifications"]["1.1.0"] = catalogService{
		identity: ServiceVersion{Name: "notifications", Version: "1.1.0"},
		source:   append([]byte(nil), notificationsSource110...),
	}
	deepLinksSource110, err := embeddedDeliveryPackages.ReadFile("service/deepLinks/1.1.0.js")
	notificationsSource120, notificationsError := embeddedDeliveryPackages.ReadFile("service/notifications/1.2.0.js")
	if notificationsError != nil {
		return nil, notificationsError
	}
	catalog.services["notifications"]["1.2.0"] = catalogService{
		identity: ServiceVersion{Name: "notifications", Version: "1.2.0"},
		source:   append([]byte(nil), notificationsSource120...),
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read service/deepLinks/1.1.0.js: %v", ErrInvalidModule, err)
	}
	catalog.services["deepLinks"]["1.1.0"] = catalogService{
		identity: ServiceVersion{Name: "deepLinks", Version: "1.1.0"},
		source:   append([]byte(nil), deepLinksSource110...),
	}
	filesSource110, err := embeddedDeliveryPackages.ReadFile("service/files/1.1.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read service/files/1.1.0.js: %v", ErrInvalidModule, err)
	}
	catalog.services["files"]["1.1.0"] = catalogService{
		identity: ServiceVersion{Name: "files", Version: "1.1.0"},
		source:   append([]byte(nil), filesSource110...),
	}
	filesSource120, err := embeddedDeliveryPackages.ReadFile("service/files/1.2.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read service/files/1.2.0.js: %v", ErrInvalidModule, err)
	}
	catalog.services["files"]["1.2.0"] = catalogService{
		identity: ServiceVersion{Name: "files", Version: "1.2.0"},
		source:   append([]byte(nil), filesSource120...),
	}
	filesSource130, err := embeddedDeliveryPackages.ReadFile("service/files/1.3.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read service/files/1.3.0.js: %v", ErrInvalidModule, err)
	}
	catalog.services["files"]["1.3.0"] = catalogService{
		identity: ServiceVersion{Name: "files", Version: "1.3.0"},
		source:   append([]byte(nil), filesSource130...),
	}
	for _, entry := range []struct {
		name, version string
		requires      []ServiceVersion
	}{
		{"network", "1.1.0", nil},
		{"studioSqlite", "1.1.0", nil},
		{"files", "1.4.0", []ServiceVersion{{Name: "network", Version: "1.1.0"}}},
		{"camera", "1.1.0", []ServiceVersion{{Name: "capabilities", Version: "1.0.0"}, {Name: "files", Version: "1.4.0"}}},
		{"media", "1.1.0", []ServiceVersion{{Name: "files", Version: "1.4.0"}}},
	} {
		path := "service/" + entry.name + "/" + entry.version + ".js"
		source, err := embeddedDeliveryPackages.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidModule, path, err)
		}
		catalog.services[entry.name][entry.version] = catalogService{
			identity: ServiceVersion{Name: entry.name, Version: entry.version},
			requires: append([]ServiceVersion(nil), entry.requires...),
			source:   append([]byte(nil), source...),
		}
	}
	studioDatabaseSource110, err := embeddedDeliveryPackages.ReadFile("service/studioDatabase/1.1.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read service/studioDatabase/1.1.0.js: %v", ErrInvalidModule, err)
	}
	catalog.services["studioDatabase"]["1.1.0"] = catalogService{
		identity: ServiceVersion{Name: "studioDatabase", Version: "1.1.0"},
		source:   append([]byte(nil), studioDatabaseSource110...),
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
		"stepper", "slider", "rating", "tags", "collapse", "combobox", "otp",
		"rotator", "scrolled",
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
	capabilityLabSource, err := embeddedDeliveryPackages.ReadFile("component/capability-lab/1.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/capability-lab/1.0.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["capability-lab"] = map[string]catalogComponent{
		"1.0.0": {
			identity: ComponentVersion{Name: "capability-lab", Version: "1.0.0"},
			requires: []ServiceVersion{
				{Name: "announce", Version: "1.0.0"},
				{Name: "appearance", Version: "1.0.0"},
				{Name: "camera", Version: "1.0.0"},
				{Name: "capabilities", Version: "1.0.0"},
				{Name: "clipboard", Version: "1.0.0"},
				{Name: "cookie", Version: "1.0.0"},
				{Name: "deepLinks", Version: "1.0.0"},
				{Name: "device", Version: "1.0.0"},
				{Name: "files", Version: "1.3.0"},
				{Name: "fullscreen", Version: "1.0.0"},
				{Name: "lifecycle", Version: "1.0.0"},
				{Name: "media", Version: "1.0.0"},
				{Name: "navigation", Version: "1.0.0"},
				{Name: "network", Version: "1.0.0"},
				{Name: "notifications", Version: "1.0.0"},
				{Name: "progress", Version: "1.0.0"},
				{Name: "qr", Version: "1.0.0"},
				{Name: "request", Version: "1.0.0"},
				{Name: "secureStorage", Version: "1.0.0"},
				{Name: "share", Version: "1.0.0"},
				{Name: "shell", Version: "1.0.0"},
				{Name: "storage", Version: "1.0.0"},
				{Name: "wakeLock", Version: "1.0.0"},
			},
			source: append([]byte(nil), capabilityLabSource...),
		},
	}
	capabilityLabSource110, err := embeddedDeliveryPackages.ReadFile("component/capability-lab/1.1.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/capability-lab/1.1.0.js: %v", ErrInvalidModule, err)
	}
	capabilityLabRequirements110 := append(
		[]ServiceVersion(nil),
		catalog.components["capability-lab"]["1.0.0"].requires...,
	)
	for index := range capabilityLabRequirements110 {
		if capabilityLabRequirements110[index].Name == "notifications" {
			capabilityLabRequirements110[index].Version = "1.1.0"
		}
	}
	catalog.components["capability-lab"]["1.1.0"] = catalogComponent{
		identity: ComponentVersion{Name: "capability-lab", Version: "1.1.0"},
		requires: capabilityLabRequirements110,
		source:   append([]byte(nil), capabilityLabSource110...),
	}
	capabilityLabSource120, err := embeddedDeliveryPackages.ReadFile("component/capability-lab/1.2.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/capability-lab/1.2.0.js: %v", ErrInvalidModule, err)
	}
	capabilityLabRequirements120 := append(
		[]ServiceVersion(nil),
		catalog.components["capability-lab"]["1.1.0"].requires...,
	)
	for index := range capabilityLabRequirements120 {
		if capabilityLabRequirements120[index].Name == "deepLinks" {
			capabilityLabRequirements120[index].Version = "1.1.0"
		}
	}
	catalog.components["capability-lab"]["1.2.0"] = catalogComponent{
		identity: ComponentVersion{Name: "capability-lab", Version: "1.2.0"},
		requires: capabilityLabRequirements120,
		source:   append([]byte(nil), capabilityLabSource120...),
	}
	capabilityLabSource130, err := embeddedDeliveryPackages.ReadFile("component/capability-lab/1.3.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/capability-lab/1.3.0.js: %v", ErrInvalidModule, err)
	}
	capabilityLabRequirements130 := append([]ServiceVersion(nil), capabilityLabRequirements120...)
	for index := range capabilityLabRequirements130 {
		switch capabilityLabRequirements130[index].Name {
		case "network", "camera", "media":
			capabilityLabRequirements130[index].Version = "1.1.0"
		case "files":
			capabilityLabRequirements130[index].Version = "1.4.0"
		}
	}
	catalog.components["capability-lab"]["1.3.0"] = catalogComponent{
		identity: ComponentVersion{Name: "capability-lab", Version: "1.3.0"},
		requires: capabilityLabRequirements130,
		source:   append([]byte(nil), capabilityLabSource130...),
	}
	appSource100, err := embeddedDeliveryPackages.ReadFile("component/app/1.0.0.js")
	capabilityLabSource140, sourceError := embeddedDeliveryPackages.ReadFile("component/capability-lab/1.4.0.js")
	if sourceError != nil {
		return nil, sourceError
	}
	capabilityLabRequirements140 := append([]ServiceVersion(nil), capabilityLabRequirements130...)
	for index := range capabilityLabRequirements140 {
		if capabilityLabRequirements140[index].Name == "notifications" {
			capabilityLabRequirements140[index].Version = "1.2.0"
		}
	}
	catalog.components["capability-lab"]["1.4.0"] = catalogComponent{
		identity: ComponentVersion{Name: "capability-lab", Version: "1.4.0"},
		requires: capabilityLabRequirements140,
		source:   append([]byte(nil), capabilityLabSource140...),
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.0.0.js: %v", ErrInvalidModule, err)
	}
	appSource110, err := embeddedDeliveryPackages.ReadFile("component/app/1.1.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.1.0.js: %v", ErrInvalidModule, err)
	}
	appSource120, err := embeddedDeliveryPackages.ReadFile("component/app/1.2.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.2.0.js: %v", ErrInvalidModule, err)
	}
	appSource130, err := embeddedDeliveryPackages.ReadFile("component/app/1.3.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.3.0.js: %v", ErrInvalidModule, err)
	}
	appSource140, err := embeddedDeliveryPackages.ReadFile("component/app/1.4.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.4.0.js: %v", ErrInvalidModule, err)
	}
	appSource150, err := embeddedDeliveryPackages.ReadFile("component/app/1.5.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.5.0.js: %v", ErrInvalidModule, err)
	}
	appSource160, err := embeddedDeliveryPackages.ReadFile("component/app/1.6.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.6.0.js: %v", ErrInvalidModule, err)
	}
	appSource170, err := embeddedDeliveryPackages.ReadFile("component/app/1.7.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.7.0.js: %v", ErrInvalidModule, err)
	}
	appSource180, err := embeddedDeliveryPackages.ReadFile("component/app/1.8.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.8.0.js: %v", ErrInvalidModule, err)
	}
	appSource190, err := embeddedDeliveryPackages.ReadFile("component/app/1.9.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.9.0.js: %v", ErrInvalidModule, err)
	}
	appSource1100, err := embeddedDeliveryPackages.ReadFile("component/app/1.10.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.10.0.js: %v", ErrInvalidModule, err)
	}
	appSource1110, err := embeddedDeliveryPackages.ReadFile("component/app/1.11.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.11.0.js: %v", ErrInvalidModule, err)
	}
	appSource1120, err := embeddedDeliveryPackages.ReadFile("component/app/1.12.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.12.0.js: %v", ErrInvalidModule, err)
	}
	appSource1130, err := embeddedDeliveryPackages.ReadFile("component/app/1.13.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.13.0.js: %v", ErrInvalidModule, err)
	}
	appSource1140, err := embeddedDeliveryPackages.ReadFile("component/app/1.14.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.14.0.js: %v", ErrInvalidModule, err)
	}
	appSource1150, err := embeddedDeliveryPackages.ReadFile("component/app/1.15.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/app/1.15.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["app"] = map[string]catalogComponent{
		"1.17.0": {
			identity: ComponentVersion{Name: "app", Version: "1.17.0"},
			requires: appComponentServices("1.17.0"),
			source:   append([]byte(nil), appSource1150...),
		},
		"1.16.0": {
			identity: ComponentVersion{Name: "app", Version: "1.16.0"},
			requires: appComponentServices("1.16.0"),
			source:   append([]byte(nil), appSource1150...),
		},
		"1.0.0": {
			identity: ComponentVersion{Name: "app", Version: "1.0.0"},
			requires: appComponentServices("1.0.0"),
			source:   append([]byte(nil), appSource100...),
		},
		"1.1.0": {
			identity: ComponentVersion{Name: "app", Version: "1.1.0"},
			requires: appComponentServices("1.1.0"),
			source:   append([]byte(nil), appSource110...),
		},
		"1.2.0": {
			identity: ComponentVersion{Name: "app", Version: "1.2.0"},
			requires: appComponentServices("1.2.0"),
			source:   append([]byte(nil), appSource120...),
		},
		"1.3.0": {
			identity: ComponentVersion{Name: "app", Version: "1.3.0"},
			requires: appComponentServices("1.3.0"),
			source:   append([]byte(nil), appSource130...),
		},
		"1.4.0": {
			identity: ComponentVersion{Name: "app", Version: "1.4.0"},
			requires: appComponentServices("1.4.0"),
			source:   append([]byte(nil), appSource140...),
		},
		"1.5.0": {
			identity: ComponentVersion{Name: "app", Version: "1.5.0"},
			requires: appComponentServices("1.5.0"),
			source:   append([]byte(nil), appSource150...),
		},
		"1.6.0": {
			identity: ComponentVersion{Name: "app", Version: "1.6.0"},
			requires: appComponentServices("1.6.0"),
			source:   append([]byte(nil), appSource160...),
		},
		"1.7.0": {
			identity: ComponentVersion{Name: "app", Version: "1.7.0"},
			requires: appComponentServices("1.7.0"),
			source:   append([]byte(nil), appSource170...),
		},
		"1.8.0": {
			identity: ComponentVersion{Name: "app", Version: "1.8.0"},
			requires: appComponentServices("1.8.0"),
			source:   append([]byte(nil), appSource180...),
		},
		"1.9.0": {
			identity: ComponentVersion{Name: "app", Version: "1.9.0"},
			requires: appComponentServices("1.9.0"),
			source:   append([]byte(nil), appSource190...),
		},
		"1.10.0": {
			identity: ComponentVersion{Name: "app", Version: "1.10.0"},
			requires: appComponentServices("1.10.0"),
			source:   append([]byte(nil), appSource1100...),
		},
		"1.11.0": {
			identity: ComponentVersion{Name: "app", Version: "1.11.0"},
			requires: appComponentServices("1.11.0"),
			source:   append([]byte(nil), appSource1110...),
		},
		"1.12.0": {
			identity: ComponentVersion{Name: "app", Version: "1.12.0"},
			requires: appComponentServices("1.12.0"),
			source:   append([]byte(nil), appSource1120...),
		},
		"1.13.0": {
			identity: ComponentVersion{Name: "app", Version: "1.13.0"},
			requires: appComponentServices("1.13.0"),
			source:   append([]byte(nil), appSource1130...),
		},
		"1.14.0": {
			identity: ComponentVersion{Name: "app", Version: "1.14.0"},
			requires: appComponentServices("1.14.0"),
			source:   append([]byte(nil), appSource1140...),
		},
		"1.15.0": {
			identity: ComponentVersion{Name: "app", Version: "1.15.0"},
			requires: appComponentServices("1.15.0"),
			source:   append([]byte(nil), appSource1150...),
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
	copySource, err := embeddedDeliveryPackages.ReadFile("component/copy/1.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/copy/1.0.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["copy"] = map[string]catalogComponent{
		"1.0.0": {
			identity: ComponentVersion{Name: "copy", Version: "1.0.0"},
			requires: []ServiceVersion{{Name: "clipboard", Version: "1.0.0"}},
			source:   append([]byte(nil), copySource...),
		},
	}
	dropzoneSource, err := embeddedDeliveryPackages.ReadFile("component/dropzone/1.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/dropzone/1.0.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["dropzone"] = map[string]catalogComponent{
		"1.0.0": {
			identity: ComponentVersion{Name: "dropzone", Version: "1.0.0"},
			source:   append([]byte(nil), dropzoneSource...),
		},
	}
	terminalSource, err := embeddedDeliveryPackages.ReadFile("component/terminal/1.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/terminal/1.0.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["terminal"] = map[string]catalogComponent{
		"1.0.0": {
			identity: ComponentVersion{Name: "terminal", Version: "1.0.0"},
			requires: []ServiceVersion{{Name: "clipboard", Version: "1.0.0"}},
			source:   append([]byte(nil), terminalSource...),
		},
	}
	desktopTitlebarSource, err := embeddedDeliveryPackages.ReadFile("component/desktop-titlebar/1.0.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/desktop-titlebar/1.0.0.js: %v", ErrInvalidModule, err)
	}
	desktopTitlebarSource110, err := embeddedDeliveryPackages.ReadFile("component/desktop-titlebar/1.1.0.js")
	if err != nil {
		return nil, fmt.Errorf("%w: read component/desktop-titlebar/1.1.0.js: %v", ErrInvalidModule, err)
	}
	catalog.components["desktop-titlebar"] = map[string]catalogComponent{
		"1.0.0": {
			identity: ComponentVersion{Name: "desktop-titlebar", Version: "1.0.0"},
			requires: []ServiceVersion{{Name: "window", Version: "1.0.0"}},
			source:   append([]byte(nil), desktopTitlebarSource...),
		},
		"1.1.0": {
			identity: ComponentVersion{Name: "desktop-titlebar", Version: "1.1.0"},
			requires: []ServiceVersion{
				{Name: "navigation", Version: "1.0.0"},
				{Name: "window", Version: "1.0.0"},
			},
			source: append([]byte(nil), desktopTitlebarSource110...),
		},
	}
	return catalog, nil
}

// addComponentPackages overlays one detached tenant catalog on this composer.
// Every name has exactly one version so an authored name@exact-semver identity
// resolves deterministically against the detached tenant catalog.
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
	identity, err := catalog.componentIdentity(name, version)
	if err != nil {
		return catalogComponent{}, err
	}
	component := catalog.components[identity.Name][identity.Version]
	component.requires = append([]ServiceVersion(nil), component.requires...)
	component.source = append([]byte(nil), component.source...)
	return component, nil
}

// componentIdentity resolves catalog defaults without detaching package
// source. Generation preparation uses it to deduplicate repeated document
// graphs before any potentially large tenant component bytes are copied.
func (catalog *deliveryCatalog) componentIdentity(name, version string) (ComponentVersion, error) {
	versions, exists := catalog.components[name]
	if !exists {
		return ComponentVersion{}, fmt.Errorf("%w: %s", ErrModuleNotFound, name)
	}
	if version == "" {
		version = catalog.componentDefault[name]
	}
	component, exists := versions[version]
	if !exists {
		return ComponentVersion{}, fmt.Errorf("%w: %s@%s", ErrModuleNotFound, name, version)
	}
	return component.identity, nil
}

// componentView returns one immutable catalog entry without detaching its
// package bytes. It is private to generation preparation, where exact package
// identities are normalized and copied once into generation-local artifacts.
func (catalog *deliveryCatalog) componentView(identity ComponentVersion) (catalogComponent, error) {
	if catalog == nil {
		return catalogComponent{}, fmt.Errorf("%w: nil delivery catalog", ErrInvalidModule)
	}
	versions, exists := catalog.components[identity.Name]
	if !exists {
		return catalogComponent{}, fmt.Errorf("%w: %s", ErrModuleNotFound, identity.Name)
	}
	component, exists := versions[identity.Version]
	if !exists {
		return catalogComponent{}, fmt.Errorf("%w: %s@%s", ErrModuleNotFound, identity.Name, identity.Version)
	}
	return component, nil
}

func (catalog *deliveryCatalog) service(identity ServiceVersion) (catalogService, error) {
	versions, exists := catalog.services[identity.Name]
	if !exists {
		return catalogService{}, fmt.Errorf("%w: required service %s@%s", ErrModuleNotFound, identity.Name, identity.Version)
	}
	service, exists := versions[identity.Version]
	if !exists {
		return catalogService{}, fmt.Errorf("%w: required service %s@%s", ErrModuleNotFound, identity.Name, identity.Version)
	}
	service.requires = append([]ServiceVersion(nil), service.requires...)
	service.actions = append([]string(nil), service.actions...)
	service.source = append([]byte(nil), service.source...)
	return service, nil
}

// serviceView is the service equivalent of componentView. The returned slices
// alias the immutable catalog and must never escape generation preparation.
func (catalog *deliveryCatalog) serviceView(identity ServiceVersion) (catalogService, error) {
	if catalog == nil {
		return catalogService{}, fmt.Errorf("%w: nil delivery catalog", ErrInvalidModule)
	}
	versions, exists := catalog.services[identity.Name]
	if !exists {
		return catalogService{}, fmt.Errorf("%w: required service %s@%s", ErrModuleNotFound, identity.Name, identity.Version)
	}
	service, exists := versions[identity.Version]
	if !exists {
		return catalogService{}, fmt.Errorf("%w: required service %s@%s", ErrModuleNotFound, identity.Name, identity.Version)
	}
	return service, nil
}
