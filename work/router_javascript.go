package work

import (
	"fmt"
	"sort"
	"strings"

	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/value"
)

// Javascript is where a site says what its JavaScript does — the counterpart of router.css().
//
//	router.javascript({
//	  drive: true,                      // same-origin links swap the page instead of reloading it
//	  prerender: true,                  // the server writes page-scope text/show answers into the HTML
//	  components: { dialog: "2.0.0" }   // pin a catalogue component for the whole site
//	})
//
// Calling it is not what turns JavaScript on. A page that uses the grammar gets the kernel and one
// file for each component it names whether or not the manifest says anything; a page that uses
// none gets no JavaScript. router.javascript() changes HOW, never WHETHER — so `router.javascript()`
// and `router.javascript(true)` are accepted and change nothing, and `false` is refused rather than
// silently ignored: there is nothing to switch off.
//
// A pin lets the markup say data-kit-component="dialog" and mean dialog@2.0.0 on every page; a
// version written in the markup still wins, and a name with no pin is the latest. A site's own
// components are not declared here at all — dropping _components/<name>.js is the declaration.
//
// (Until 01/10 this name switched the site onto staged KitJS, the second runtime the engine once
// carried. KitJS lives on only as the standalone package.)
func (f *FolderRouter) Javascript(args ...value.Value) (*FolderRouter, error) {
	if len(args) > 1 {
		return f, fmt.Errorf("router.javascript expects nothing or one options object")
	}
	if len(args) == 0 {
		return f, nil
	}
	switch args[0].K {
	case value.Bool:
		if args[0].N == 0 {
			return f, fmt.Errorf("router.javascript(false): there is nothing to switch off — a page that uses no data-kit-* directive already gets no JavaScript")
		}
		return f, nil
	case value.Map:
		return f, f.configureJavascript(args[0].Map())
	default:
		return f, fmt.Errorf("router.javascript expects an options object; got %s", args[0].K)
	}
}

// Jitjs is the old spelling, kept so a manifest that still says router.jitjs(true) boots. It used
// to move the site onto staged KitJS; that runtime is gone, so it now means exactly what
// router.javascript() means — which for true and for no argument is nothing at all.
func (f *FolderRouter) Jitjs(args ...value.Value) (*FolderRouter, error) { return f.Javascript(args...) }

func (f *FolderRouter) configureJavascript(options map[string]value.Value) error {
	if f == nil || f.tenant == nil || f.node == nil {
		return nil
	}
	if f.node.parent != nil {
		return fmt.Errorf("router.javascript options belong to the site-root router.kitwork.js: they apply to every page")
	}
	presentation := f.tenant.presentation()
	for _, key := range sortedValueKeys(options) {
		option := options[key]
		switch key {
		case "drive":
			if option.K != value.Bool {
				return fmt.Errorf("router.javascript drive must be true or false; got %s", option.K)
			}
			if !presentation.SetDrive(option.N != 0) {
				return fmt.Errorf("router.javascript cannot change a frozen site generation")
			}
		case "prerender":
			if option.K != value.Bool {
				return fmt.Errorf("router.javascript prerender must be true or false; got %s", option.K)
			}
			if !presentation.SetPrerender(option.N != 0) {
				return fmt.Errorf("router.javascript cannot change a frozen site generation")
			}
		case "components":
			if !option.IsMap() {
				return fmt.Errorf(`router.javascript components must be an object of versions, like { dialog: "2.0.0" }`)
			}
			pins := option.Map()
			for _, name := range sortedValueKeys(pins) {
				pin := pins[name]
				if pin.K != value.String {
					return fmt.Errorf(`router.javascript components.%s must be a version string, like "1.0.0" — a site's own component is a file in _components/, not a declaration`, name)
				}
				version := strings.TrimPrefix(strings.TrimSpace(pin.Text()), "v")
				if !jitjs.HasComponentVersion(name, version) {
					known := jitjs.ComponentVersionsOf(name)
					if len(known) == 0 {
						return fmt.Errorf("router.javascript components.%s: there is no catalogue component by that name", name)
					}
					return fmt.Errorf("router.javascript components.%s: there is no version %s — the catalogue has %s", name, version, strings.Join(known, ", "))
				}
				if !presentation.SetComponentVersion(name, version) {
					return fmt.Errorf("router.javascript cannot change a frozen site generation")
				}
			}
		default:
			return fmt.Errorf("router.javascript has no option %q — it takes drive, prerender and components", key)
		}
	}
	return nil
}

func sortedValueKeys(values map[string]value.Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
