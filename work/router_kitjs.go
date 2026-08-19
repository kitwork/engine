package work

import (
	"fmt"
	"sort"

	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/value"
)

// Jitjs opts the whole pending site generation into generation-prepared staged
// JavaScript delivery. It is intentionally site-wide and defaults off so
// tenants must opt in explicitly.
//
//	router.jitjs()
//	router.jitjs(true)
//	router.jitjs(false)
//	router.jitjs({ components: { counter: { version: "1.0.0", source: "./components/counter.js" } } })
func (f *FolderRouter) Jitjs(args ...value.Value) (*FolderRouter, error) {
	if len(args) > 1 {
		return f, fmt.Errorf("router.jitjs expects zero arguments or one boolean or options object")
	}
	enabled := true
	if len(args) == 1 {
		switch args[0].K {
		case value.Bool:
			enabled = args[0].N != 0
		case value.Map:
			if err := f.configureJITJS(args[0]); err != nil {
				return f, err
			}
			return f, nil
		default:
			return f, fmt.Errorf("router.jitjs expects zero arguments or one boolean; got %s (or use an options object)", args[0].K)
		}
	}
	if f == nil || f.tenant == nil {
		return f, nil
	}
	f.tenant.presentation().SetKitJS(enabled)
	return f, nil
}

type jitJSComponentDeclaration struct {
	name    string
	version string
	source  string
}

// configureJITJS installs the canonical generation manifest. It is root-only:
// an exact component identity must resolve to one site-wide source regardless
// of which route uses it.
func (f *FolderRouter) configureJITJS(options value.Value) error {
	if f == nil || f.tenant == nil || f.node == nil || f.tenant.generation == nil {
		return fmt.Errorf("router.jitjs options require a pending site generation")
	}
	if f.node.parent != nil {
		return fmt.Errorf("router.jitjs component manifest is allowed only in the site-root router.kitwork.js")
	}

	manifest := options.Map()
	for _, key := range sortedValueKeys(manifest) {
		if key != "components" {
			return fmt.Errorf("router.jitjs options contains unsupported field %q", key)
		}
	}
	declarations, err := parseJITJSComponentDeclarations(manifest)
	if err != nil {
		return err
	}
	for _, declaration := range declarations {
		label := fmt.Sprintf("router.jitjs component %q", declaration.name)
		if err := f.addJITComponent(label, declaration.name, declaration.version, declaration.source, f.node.diskPath()); err != nil {
			return err
		}
	}
	if !f.tenant.presentation().SetKitJS(true) {
		return fmt.Errorf("router.jitjs options cannot change a frozen site generation")
	}
	return nil
}

func parseJITJSComponentDeclarations(manifest map[string]value.Value) ([]jitJSComponentDeclaration, error) {
	components, exists := manifest["components"]
	if !exists {
		return nil, nil
	}
	if !components.IsMap() {
		return nil, fmt.Errorf("router.jitjs components must be an object")
	}
	componentMap := components.Map()
	if len(componentMap) > site.MaxJITComponentSources {
		return nil, fmt.Errorf("router.jitjs components has %d entries; limit is %d", len(componentMap), site.MaxJITComponentSources)
	}

	names := sortedValueKeys(componentMap)
	declarations := make([]jitJSComponentDeclaration, 0, len(names))
	for _, name := range names {
		descriptor := componentMap[name]
		if !descriptor.IsMap() {
			return nil, fmt.Errorf("router.jitjs component %q must be an object", name)
		}
		fields := descriptor.Map()
		for _, field := range sortedValueKeys(fields) {
			if field != "source" && field != "version" {
				return nil, fmt.Errorf("router.jitjs component %q contains unsupported field %q", name, field)
			}
		}
		version, versionExists := fields["version"]
		if !versionExists {
			return nil, fmt.Errorf("router.jitjs component %q requires field %q", name, "version")
		}
		if version.K != value.String {
			return nil, fmt.Errorf("router.jitjs component %q field %q must be a string; got %s", name, "version", version.K)
		}
		source, sourceExists := fields["source"]
		if !sourceExists {
			return nil, fmt.Errorf("router.jitjs component %q requires field %q", name, "source")
		}
		if source.K != value.String {
			return nil, fmt.Errorf("router.jitjs component %q field %q must be a string; got %s", name, "source", source.K)
		}
		declarations = append(declarations, jitJSComponentDeclaration{
			name: name, version: version.Text(), source: source.Text(),
		})
	}
	return declarations, nil
}

func sortedValueKeys(values map[string]value.Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Kitjs is the deprecated 0.9 compatibility alias for Jitjs.
func (f *FolderRouter) Kitjs(args ...value.Value) (*FolderRouter, error) { return f.Jitjs(args...) }

// KitJS is the deprecated acronym-preserving Go alias for Jitjs.
func (f *FolderRouter) KitJS(args ...value.Value) (*FolderRouter, error) { return f.Jitjs(args...) }
