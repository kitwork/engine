package javascript

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ModuleKind identifies a composition layer. The order is architectural:
// core, services, components, then the core boot module.
type ModuleKind string

const (
	CoreModule      ModuleKind = "core"
	ServiceModule   ModuleKind = "service"
	ComponentModule ModuleKind = "component"
)

const (
	embeddedCoreVersion = "0.1.0-preview.1"
)

var baseCoreNames = [...]string{"global", "expression", "component", "dom", "lifecycle"}

var (
	ErrInvalidModule        = errors.New("kitjs: invalid module")
	ErrModuleNotFound       = errors.New("kitjs: module not found")
	ErrVersionConflict      = errors.New("kitjs: one version per module name")
	ErrDependencyCycle      = errors.New("kitjs: dependency cycle")
	ErrInvalidComponentUse  = errors.New("kitjs: invalid component use")
	ErrInvalidAppUse        = errors.New("kitjs: invalid app use")
	ErrUnsupportedAttribute = errors.New("kitjs: unsupported reserved attribute")
)

// ModuleID is the stable identity used by the Go registry. Versions are exact
// SemVer values; the browser never resolves versions.
type ModuleID struct {
	Kind    ModuleKind
	Name    string
	Version string
}

func (id ModuleID) String() string {
	return string(id.Kind) + ":" + id.Name + "@" + id.Version
}

// Module is explicit metadata for one independently executable classic
// browser script. Source is copied when a Registry is created, keeping the
// registry immutable after publication.
type Module struct {
	ID       ModuleID
	Path     string
	Requires []ModuleID
	Source   []byte
	Default  bool
}

type moduleName struct {
	kind ModuleKind
	name string
}

// Registry is an immutable module catalog safe to share across generations.
// Selection state belongs to a Compose call, never to this registry.
type Registry struct {
	modules  map[ModuleID]Module
	defaults map[moduleName]ModuleID
	baseCore []ModuleID
	boot     ModuleID
}

// NewRegistry validates and copies an explicit module catalog.
func NewRegistry(modules []Module) (*Registry, error) {
	registry := &Registry{
		modules:  make(map[ModuleID]Module, len(modules)),
		defaults: make(map[moduleName]ModuleID),
	}
	paths := make(map[string]ModuleID, len(modules))

	for _, input := range modules {
		module, err := copyAndValidateModule(input)
		if err != nil {
			return nil, err
		}
		if _, exists := registry.modules[module.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate %s", ErrInvalidModule, module.ID)
		}
		if prior, exists := paths[module.Path]; exists {
			return nil, fmt.Errorf("%w: path %q is shared by %s and %s", ErrInvalidModule, module.Path, prior, module.ID)
		}

		registry.modules[module.ID] = module
		paths[module.Path] = module.ID
		if module.Default {
			name := moduleName{kind: module.ID.Kind, name: module.ID.Name}
			if prior, exists := registry.defaults[name]; exists {
				return nil, fmt.Errorf("%w: multiple defaults for %s:%s (%s and %s)", ErrInvalidModule, name.kind, name.name, prior.Version, module.ID.Version)
			}
			registry.defaults[name] = module.ID
		}
	}
	for id := range registry.modules {
		if id.Kind == CoreModule && !supportedCoreModule(id.Name) {
			return nil, fmt.Errorf("%w: unsupported core module %s", ErrInvalidModule, id)
		}
		if id.Kind != CoreModule {
			name := moduleName{kind: id.Kind, name: id.Name}
			if _, exists := registry.defaults[name]; !exists {
				return nil, fmt.Errorf("%w: %s:%s has no explicit default version", ErrInvalidModule, id.Kind, id.Name)
			}
		}
	}

	registry.baseCore = make([]ModuleID, 0, len(baseCoreNames))
	for _, name := range baseCoreNames {
		id, err := registry.requireSingleCore(name)
		if err != nil {
			return nil, err
		}
		registry.baseCore = append(registry.baseCore, id)
	}
	boot, err := registry.requireSingleCore("boot")
	if err != nil {
		return nil, err
	}
	registry.boot = boot

	for _, module := range registry.modules {
		for _, dependency := range module.Requires {
			if _, exists := registry.modules[dependency]; !exists {
				return nil, fmt.Errorf("%w: %s requires %s", ErrModuleNotFound, module.ID, dependency)
			}
			if err := validateDependencyLayer(module.ID, dependency); err != nil {
				return nil, err
			}
		}
	}
	if err := registry.validateAllDependencyCycles(); err != nil {
		return nil, err
	}

	return registry, nil
}

func copyAndValidateModule(input Module) (Module, error) {
	module := input
	if !validKind(module.ID.Kind) {
		return Module{}, fmt.Errorf("%w: unknown kind %q", ErrInvalidModule, module.ID.Kind)
	}
	if !validModuleName(module.ID.Name) {
		return Module{}, fmt.Errorf("%w: invalid %s name %q", ErrInvalidModule, module.ID.Kind, module.ID.Name)
	}
	if !validExactSemVer(module.ID.Version) {
		return Module{}, fmt.Errorf("%w: invalid version %q for %s:%s", ErrInvalidModule, module.ID.Version, module.ID.Kind, module.ID.Name)
	}
	if module.Path == "" || strings.Contains(module.Path, "\\") || strings.HasPrefix(module.Path, "/") || strings.Contains(module.Path, "../") {
		return Module{}, fmt.Errorf("%w: invalid source path %q", ErrInvalidModule, module.Path)
	}
	if len(module.Source) == 0 {
		return Module{}, fmt.Errorf("%w: empty source for %s", ErrInvalidModule, module.ID)
	}

	module.Source = append([]byte(nil), module.Source...)
	module.Requires = append([]ModuleID(nil), module.Requires...)
	seen := make(map[ModuleID]struct{}, len(module.Requires))
	for _, dependency := range module.Requires {
		if !validKind(dependency.Kind) || !validModuleName(dependency.Name) || !validExactSemVer(dependency.Version) {
			return Module{}, fmt.Errorf("%w: invalid dependency %s on %s", ErrInvalidModule, dependency, module.ID)
		}
		if dependency == module.ID {
			return Module{}, fmt.Errorf("%w: %s requires itself", ErrDependencyCycle, module.ID)
		}
		if _, duplicate := seen[dependency]; duplicate {
			return Module{}, fmt.Errorf("%w: duplicate dependency %s on %s", ErrInvalidModule, dependency, module.ID)
		}
		seen[dependency] = struct{}{}
	}
	sort.Slice(module.Requires, func(i, j int) bool {
		return lessModuleID(module.Requires[i], module.Requires[j])
	})
	return module, nil
}

func (registry *Registry) requireSingleCore(name string) (ModuleID, error) {
	var found []ModuleID
	for id := range registry.modules {
		if id.Kind == CoreModule && id.Name == name {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		return ModuleID{}, fmt.Errorf("%w: registry requires exactly one core:%s module", ErrInvalidModule, name)
	}
	return found[0], nil
}

func validateDependencyLayer(owner, dependency ModuleID) error {
	switch owner.Kind {
	case CoreModule:
		return fmt.Errorf("%w: core module %s cannot declare dependencies", ErrInvalidModule, owner)
	case ServiceModule:
		if dependency.Kind != ServiceModule {
			return fmt.Errorf("%w: service %s may require only services, got %s", ErrInvalidModule, owner, dependency)
		}
	case ComponentModule:
		if dependency.Kind != ServiceModule && dependency.Kind != ComponentModule {
			return fmt.Errorf("%w: component %s may require only services or components, got %s", ErrInvalidModule, owner, dependency)
		}
	}
	return nil
}

func (registry *Registry) validateAllDependencyCycles() error {
	state := make(map[ModuleID]uint8, len(registry.modules))
	stack := make([]ModuleID, 0, 8)
	var visit func(ModuleID) error
	visit = func(id ModuleID) error {
		switch state[id] {
		case 1:
			cycle := append(append([]ModuleID(nil), stack...), id)
			parts := make([]string, len(cycle))
			for index := range cycle {
				parts[index] = cycle[index].String()
			}
			return fmt.Errorf("%w: %s", ErrDependencyCycle, strings.Join(parts, " -> "))
		case 2:
			return nil
		}
		state[id] = 1
		stack = append(stack, id)
		for _, dependency := range registry.modules[id].Requires {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return nil
	}

	ids := make([]ModuleID, 0, len(registry.modules))
	for id := range registry.modules {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return lessModuleID(ids[i], ids[j]) })
	for _, id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func validKind(kind ModuleKind) bool {
	return kind == CoreModule || kind == ServiceModule || kind == ComponentModule
}

func supportedCoreModule(name string) bool {
	switch name {
	case "global", "expression", "component", "dom", "lifecycle", "morph", "drive", "boot":
		return true
	default:
		return false
	}
}

func validModuleName(name string) bool {
	if name == "" || !asciiLetter(name[0]) {
		return false
	}
	for index := 1; index < len(name); index++ {
		char := name[index]
		if !asciiLetter(char) && (char < '0' || char > '9') && char != '_' && char != '.' && char != '-' {
			return false
		}
	}
	return true
}

func asciiLetter(char byte) bool {
	return (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
}

func lessModuleID(left, right ModuleID) bool {
	if left.Kind != right.Kind {
		return moduleKindRank(left.Kind) < moduleKindRank(right.Kind)
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.Version < right.Version
}

func moduleKindRank(kind ModuleKind) int {
	switch kind {
	case CoreModule:
		return 0
	case ServiceModule:
		return 1
	case ComponentModule:
		return 2
	default:
		return 3
	}
}

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *Registry
	defaultRegistryErr  error
)

// DefaultRegistry returns the immutable registry backed by go:embed sources.
func DefaultRegistry() (*Registry, error) {
	defaultRegistryOnce.Do(func() {
		defaultRegistry, defaultRegistryErr = loadEmbeddedRegistry()
	})
	return defaultRegistry, defaultRegistryErr
}

type embeddedModule struct {
	id        ModuleID
	path      string
	requires  []ModuleID
	isDefault bool
}

func loadEmbeddedRegistry() (*Registry, error) {
	service := func(name, version string) ModuleID {
		return ModuleID{Kind: ServiceModule, Name: name, Version: version}
	}
	component := func(name, version string) ModuleID {
		return ModuleID{Kind: ComponentModule, Name: name, Version: version}
	}

	announceV1 := service("announce", "1.0.0")
	clipboardV1 := service("clipboard", "1.0.0")
	cookieV1 := service("cookie", "1.0.0")
	fullscreenV1 := service("fullscreen", "1.0.0")
	navigationV1 := service("navigation", "1.0.0")
	networkV1 := service("network", "1.0.0")
	requestV1 := service("request", "1.0.0")
	shareV1 := service("share", "1.0.0")
	storageV1 := service("storage", "1.0.0")
	accordionV1 := component("accordion", "1.0.0")
	announceComponentV1 := component("announce", "1.0.0")
	clipboardComponentV1 := component("clipboard", "1.0.0")
	comboboxV1 := component("combobox", "1.0.0")
	commandPaletteV1 := component("command-palette", "1.0.0")
	counterV1 := component("counter", "1.0.0")
	dialogV1 := component("dialog", "1.0.0")
	drawerV1 := component("drawer", "1.0.0")
	dropdownV1 := component("dropdown", "1.0.0")
	menuV1 := component("menu", "1.0.0")
	popoverV1 := component("popover", "1.0.0")
	progressBarV1 := component("progress-bar", "1.0.0")
	tabsV1 := component("tabs", "1.0.0")
	themeV1 := component("theme", "1.0.0")
	toastV1 := component("toast", "1.0.0")
	tooltipV1 := component("tooltip", "1.0.0")

	catalog := []embeddedModule{
		{id: ModuleID{Kind: CoreModule, Name: "global", Version: embeddedCoreVersion}, path: "core/global.js", isDefault: true},
		{id: ModuleID{Kind: CoreModule, Name: "expression", Version: embeddedCoreVersion}, path: "core/expression.js", isDefault: true},
		{id: ModuleID{Kind: CoreModule, Name: "component", Version: embeddedCoreVersion}, path: "core/component.js", isDefault: true},
		{id: ModuleID{Kind: CoreModule, Name: "dom", Version: embeddedCoreVersion}, path: "core/dom.js", isDefault: true},
		{id: ModuleID{Kind: CoreModule, Name: "lifecycle", Version: embeddedCoreVersion}, path: "core/lifecycle.js", isDefault: true},
		{id: ModuleID{Kind: CoreModule, Name: "morph", Version: embeddedCoreVersion}, path: "core/morph.js", isDefault: true},
		{id: ModuleID{Kind: CoreModule, Name: "drive", Version: embeddedCoreVersion}, path: "core/drive.js", isDefault: true},
		{id: ModuleID{Kind: CoreModule, Name: "boot", Version: embeddedCoreVersion}, path: "core/boot.js", isDefault: true},

		{id: announceV1, path: "service/announce/1.0.0.js", isDefault: true},
		{id: clipboardV1, path: "service/clipboard/1.0.0.js", isDefault: true},
		{id: cookieV1, path: "service/cookie/1.0.0.js", isDefault: true},
		{id: fullscreenV1, path: "service/fullscreen/1.0.0.js", isDefault: true},
		{id: navigationV1, path: "service/navigation/1.0.0.js", isDefault: true},
		{id: networkV1, path: "service/network/1.0.0.js", isDefault: true},
		{id: requestV1, path: "service/request/1.0.0.js", isDefault: true},
		{id: shareV1, path: "service/share/1.0.0.js", requires: []ModuleID{clipboardV1}, isDefault: true},
		{id: storageV1, path: "service/storage/1.0.0.js", isDefault: true},

		{id: accordionV1, path: "component/accordion/1.0.0.js", isDefault: true},
		{id: announceComponentV1, path: "component/announce/1.0.0.js", requires: []ModuleID{announceV1}, isDefault: true},
		{id: clipboardComponentV1, path: "component/clipboard/1.0.0.js", requires: []ModuleID{clipboardV1}, isDefault: true},
		{id: comboboxV1, path: "component/combobox/1.0.0.js", isDefault: true},
		{id: commandPaletteV1, path: "component/command-palette/1.0.0.js", isDefault: true},
		{id: counterV1, path: "component/counter/1.0.0.js", isDefault: true},
		{id: dialogV1, path: "component/dialog/1.0.0.js", isDefault: true},
		{id: drawerV1, path: "component/drawer/1.0.0.js", isDefault: true},
		{id: dropdownV1, path: "component/dropdown/1.0.0.js", isDefault: true},
		{id: menuV1, path: "component/menu/1.0.0.js", isDefault: true},
		{id: popoverV1, path: "component/popover/1.0.0.js", isDefault: true},
		{id: progressBarV1, path: "component/progress-bar/1.0.0.js", isDefault: true},
		{id: tabsV1, path: "component/tabs/1.0.0.js", isDefault: true},
		{id: themeV1, path: "component/theme/1.0.0.js", requires: []ModuleID{storageV1}, isDefault: true},
		{id: toastV1, path: "component/toast/1.0.0.js", requires: []ModuleID{announceV1}, isDefault: true},
		{id: tooltipV1, path: "component/tooltip/1.0.0.js", isDefault: true},
	}

	modules := make([]Module, 0, len(catalog))
	for _, entry := range catalog {
		source, err := embeddedSources.ReadFile(entry.path)
		if err != nil {
			return nil, fmt.Errorf("load embedded KitJS module %s: %w", entry.id, err)
		}
		modules = append(modules, Module{
			ID:       entry.id,
			Path:     entry.path,
			Requires: entry.requires,
			Source:   source,
			Default:  entry.isDefault,
		})
	}
	return NewRegistry(modules)
}
