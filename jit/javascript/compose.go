package javascript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Plan is the exact, deterministic graph selected for one document. Fixed and
// conditional core fragments are represented on Bundle.Modules.
type Plan struct {
	NeedsRuntime bool
	Drive        bool
	Services     []ModuleID
	Components   []ModuleID
}

// Bundle is a content-addressable KitJS artifact. An empty document produces
// an empty Bundle, allowing the caller to omit JavaScript entirely.
type Bundle struct {
	JavaScript  []byte
	ContentHash string
	Modules     []ModuleID
}

func (bundle Bundle) Empty() bool {
	return len(bundle.JavaScript) == 0
}

// Composer selects from an immutable registry. It has no request-local or
// generation-local mutable state and is safe for concurrent use.
type Composer struct {
	registry *Registry
}

func NewComposer(registry *Registry) (*Composer, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: nil registry", ErrInvalidModule)
	}
	return &Composer{registry: registry}, nil
}

func NewDefaultComposer() (*Composer, error) {
	registry, err := DefaultRegistry()
	if err != nil {
		return nil, err
	}
	return NewComposer(registry)
}

// ComposeHTML scans authored runtime use, resolves its exact graph, and
// composes one classic JavaScript artifact.
func (composer *Composer) ComposeHTML(source []byte) (Bundle, error) {
	if composer == nil || composer.registry == nil {
		return Bundle{}, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	use, err := ScanHTML(source)
	if err != nil {
		return Bundle{}, err
	}
	plan, err := composer.registry.resolveUse(use)
	if err != nil {
		return Bundle{}, err
	}
	return composer.composePlan(plan)
}

// ComposeAppScans closes one navigation graph across every prepared document
// that declares the same application identity. Drive can morph only when the
// current and incoming documents carry the same plan fingerprint, so an app
// bundle is intentionally the union of its generation-known routes rather than
// a different only-used graph for each response.
func (composer *Composer) ComposeAppScans(scans []ScanResult) (Bundle, error) {
	if composer == nil || composer.registry == nil {
		return Bundle{}, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	use, err := mergeAppScans(scans)
	if err != nil {
		return Bundle{}, err
	}
	plan, err := composer.registry.resolveUse(use)
	if err != nil {
		return Bundle{}, err
	}
	return composer.composePlan(plan)
}

func mergeAppScans(scans []ScanResult) (ScanResult, error) {
	if len(scans) == 0 {
		return ScanResult{}, fmt.Errorf("%w: application graph requires at least one document", ErrInvalidAppUse)
	}
	merged := ScanResult{
		Components:   make([]ComponentRef, 0, len(scans)*4),
		NeedsRuntime: true,
		Drive:        true,
		HasApp:       true,
	}
	for index, scan := range scans {
		if !scan.HasApp {
			return ScanResult{}, fmt.Errorf("%w: document %d is missing a positive application marker", ErrInvalidAppUse, index)
		}
		if index == 0 {
			merged.App = scan.App
			merged.AppOffset = scan.AppOffset
		} else if scan.App != merged.App {
			return ScanResult{}, fmt.Errorf("%w: application graph mixes identities %q and %q", ErrInvalidAppUse, merged.App, scan.App)
		}
		merged.Components = append(merged.Components, scan.Components...)
	}
	return merged, nil
}

// Resolve selects one exact version per service/component name and includes
// transitive dependencies. Result ordering is stable regardless of HTML order.
func (registry *Registry) Resolve(components []ComponentRef) (Plan, error) {
	return registry.resolveUse(ScanResult{
		Components:   components,
		NeedsRuntime: len(components) > 0,
	})
}

func (registry *Registry) resolveUse(use ScanResult) (Plan, error) {
	selected := make(map[ModuleID]struct{})
	selectedNames := make(map[moduleName]ModuleID)
	state := make(map[ModuleID]uint8)

	var selectModule func(ModuleID) error
	selectModule = func(id ModuleID) error {
		module, exists := registry.modules[id]
		if !exists {
			return fmt.Errorf("%w: %s", ErrModuleNotFound, id)
		}

		name := moduleName{kind: id.Kind, name: id.Name}
		if prior, exists := selectedNames[name]; exists && prior.Version != id.Version {
			return fmt.Errorf("%w: %s:%s requests both %s and %s", ErrVersionConflict, id.Kind, id.Name, prior.Version, id.Version)
		}
		selectedNames[name] = id

		switch state[id] {
		case 1:
			return fmt.Errorf("%w while selecting %s", ErrDependencyCycle, id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, dependency := range module.Requires {
			if err := selectModule(dependency); err != nil {
				return fmt.Errorf("resolve dependency of %s: %w", id, err)
			}
		}
		state[id] = 2
		selected[id] = struct{}{}
		return nil
	}

	for _, component := range use.Components {
		id, err := registry.resolveComponent(component)
		if err != nil {
			return Plan{}, err
		}
		if err := selectModule(id); err != nil {
			return Plan{}, err
		}
	}

	services, err := registry.orderSelected(selected, ServiceModule)
	if err != nil {
		return Plan{}, err
	}
	componentModules, err := registry.orderSelected(selected, ComponentModule)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		NeedsRuntime: use.NeedsRuntime || use.HasApp || len(componentModules) > 0,
		Drive:        use.Drive || use.HasApp,
		Services:     services,
		Components:   componentModules,
	}, nil
}

func (registry *Registry) resolveComponent(component ComponentRef) (ModuleID, error) {
	if !validModuleName(component.Name) {
		return ModuleID{}, fmt.Errorf("%w: invalid component name %q", ErrInvalidComponentUse, component.Name)
	}
	if component.Version == "" {
		id, exists := registry.defaults[moduleName{kind: ComponentModule, name: component.Name}]
		if !exists {
			return ModuleID{}, fmt.Errorf("%w: no default for component:%s", ErrModuleNotFound, component.Name)
		}
		return id, nil
	}
	if !validExactSemVer(component.Version) {
		return ModuleID{}, fmt.Errorf("%w: component %q has invalid exact version %q", ErrInvalidComponentUse, component.Name, component.Version)
	}
	id := ModuleID{Kind: ComponentModule, Name: component.Name, Version: component.Version}
	if _, exists := registry.modules[id]; !exists {
		return ModuleID{}, fmt.Errorf("%w: %s", ErrModuleNotFound, id)
	}
	return id, nil
}

func (registry *Registry) orderSelected(selected map[ModuleID]struct{}, kind ModuleKind) ([]ModuleID, error) {
	roots := make([]ModuleID, 0, len(selected))
	for id := range selected {
		if id.Kind == kind {
			roots = append(roots, id)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return lessModuleID(roots[i], roots[j]) })

	ordered := make([]ModuleID, 0, len(roots))
	state := make(map[ModuleID]uint8, len(roots))
	var visit func(ModuleID) error
	visit = func(id ModuleID) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("%w while ordering %s", ErrDependencyCycle, id)
		case 2:
			return nil
		}
		state[id] = 1
		module := registry.modules[id]
		for _, dependency := range module.Requires {
			if dependency.Kind != kind {
				continue
			}
			if _, included := selected[dependency]; !included {
				return fmt.Errorf("%w: selected graph omitted %s required by %s", ErrModuleNotFound, dependency, id)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[id] = 2
		ordered = append(ordered, id)
		return nil
	}
	for _, id := range roots {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// Compose emits ordered base core fragments, resolved services, resolved
// components, then core/boot. Source files are joined verbatim with an explicit
// ;\n boundary.
func (composer *Composer) Compose(components []ComponentRef) (Bundle, error) {
	if composer == nil || composer.registry == nil {
		return Bundle{}, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	plan, err := composer.registry.Resolve(components)
	if err != nil {
		return Bundle{}, err
	}
	return composer.composePlan(plan)
}

// ComposeStandalone emits a predefined standalone/CDN graph from the same
// registry and composer used by Kitwork generations. Unlike Compose, the core
// is emitted even when components is empty because including the standalone
// script is itself the activation opt-in.
func (composer *Composer) ComposeStandalone(components []ComponentRef, drive bool) (Bundle, error) {
	if composer == nil || composer.registry == nil {
		return Bundle{}, fmt.Errorf("%w: nil composer", ErrInvalidModule)
	}
	plan, err := composer.registry.Resolve(components)
	if err != nil {
		return Bundle{}, err
	}
	plan.NeedsRuntime = true
	plan.Drive = drive
	return composer.composePlan(plan)
}

func (composer *Composer) composePlan(plan Plan) (Bundle, error) {
	if !plan.NeedsRuntime && !plan.Drive {
		return Bundle{}, nil
	}
	ids := make([]ModuleID, 0, len(composer.registry.baseCore)+3+len(plan.Services)+len(plan.Components))
	ids = append(ids, composer.registry.baseCore...)
	if plan.Drive {
		morph, err := composer.registry.requireSingleCore("morph")
		if err != nil {
			return Bundle{}, fmt.Errorf("compose Drive: %w", err)
		}
		drive, err := composer.registry.requireSingleCore("drive")
		if err != nil {
			return Bundle{}, fmt.Errorf("compose Drive: %w", err)
		}
		ids = append(ids, morph, drive)
	}
	ids = append(ids, plan.Services...)
	ids = append(ids, plan.Components...)
	ids = append(ids, composer.registry.boot)

	parts := make([][]byte, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, composer.registry.modules[id].Source)
	}
	javascript := bytes.Join(parts, []byte(";\n"))
	digest := sha256.Sum256(javascript)

	return Bundle{
		JavaScript:  javascript,
		ContentHash: hex.EncodeToString(digest[:]),
		Modules:     append([]ModuleID(nil), ids...),
	}, nil
}
