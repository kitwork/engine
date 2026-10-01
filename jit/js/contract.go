package js

// A component's CONTRACT: what it needs from the markup and what it gives back, declared beside the
// code as components/<name>/<version>.json. The browser never loads it; the server reads it so the
// verify pass can name a mistake at render time instead of the component failing in silence, and
// so the catalogue can print the surface from one source rather than three hand-kept copies. A test
// holds the contract to the code: a state field or a method one has and the other lacks is red.
//
//	{
//	  "name": "tree", "version": "1.0.0", "summary": "…",
//	  "host":     { "attributes": { "data-x": { "type": "number", "default": 0, "doc": "…" } } },
//	  "elements": { "item": { "required": true, "attributes": { "data-id": { "required": true } } } },
//	  "state":    { "selected": { "type": "string", "doc": "…" } },
//	  "methods":  { "select": { "params": ["name"], "doc": "…" } },
//	  "events":   { "seen": { "doc": "…" } }
//	}
//
// Keys keep the order they are written in, because that is the order a reader meets them in the
// catalogue. A key that is not part of the format is an error, not a silent no-op: a contract that
// says "requird" must not pass as one that requires nothing.
//
// The version is the contract (01/10). A fix that leaves the contract alone is made in place — the
// file's content hash takes care of every cache. Adding, removing or changing the meaning of an
// attribute, element, state field, method or event is a new version.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Contract is one component version's declared surface.
type Contract struct {
	Name     string              `json:"name"`
	Version  string              `json:"version"`
	Summary  string              `json:"summary"`
	Host     ContractHost        `json:"host"`
	Elements Entries[Element]    `json:"elements"`
	State    Entries[StateField] `json:"state"`
	Methods  Entries[Method]     `json:"methods"`
	Events   Entries[Event]      `json:"events"`
}

// ContractHost is what the element carrying data-kit-component must be and may say. A host takes
// no data-kit-scope, so its configuration is plain attributes.
type ContractHost struct {
	// Tag, when set, is the only element the component works on ("dialog" drives a <dialog>).
	Tag        string             `json:"tag,omitempty"`
	Attributes Entries[Attribute] `json:"attributes"`
}

// Attribute is one attribute the component reads, on its host or on one of its elements. It may be
// written plainly or bound (data-kit-bind:<name>); either way it is on the element when read.
type Attribute struct {
	Type     string          `json:"type,omitempty"`
	Required bool            `json:"required,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
	Doc      string          `json:"doc"`
}

// Element is a part of the markup the component finds by data-kit-element="<name>".
type Element struct {
	Required   bool               `json:"required,omitempty"`
	Many       bool               `json:"many,omitempty"`
	Attributes Entries[Attribute] `json:"attributes,omitempty"`
	Doc        string             `json:"doc"`
}

// StateField is a name an expression inside the component can read.
type StateField struct {
	Type string `json:"type"`
	Doc  string `json:"doc"`
	// Computed marks a getter: always answers for things as they stand, and cannot be assigned.
	Computed bool `json:"computed,omitempty"`
	// Page marks a name the component reads from the page's scope rather than owning it — kanban's
	// `cards` are the page's data. It is not on the blueprint.
	Page bool `json:"page,omitempty"`
}

// Method is a function an expression can call: select('docs'), $menu.hide().
type Method struct {
	Params  []string `json:"params,omitempty"`
	Returns string   `json:"returns,omitempty"`
	Doc     string   `json:"doc"`
	// Internal marks a method that is on the blueprint for the component's own use; it can be
	// called, but the catalogue does not offer it and a page should not lean on it.
	Internal bool `json:"internal,omitempty"`
}

// Event is something the component announces, heard with data-kit-event:<name>.
type Event struct {
	Detail string `json:"detail,omitempty"`
	Doc    string `json:"doc"`
}

// Entry is one named item of a contract section.
type Entry[T any] struct {
	Name  string
	Value T
}

// Entries is a JSON object read in the order it was written. Go's maps forget that order, and a
// duplicate key — which encoding/json would let the last one win — is an error here.
type Entries[T any] []Entry[T]

// Get returns the named entry.
func (entries Entries[T]) Get(name string) (T, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry.Value, true
		}
	}
	var zero T
	return zero, false
}

// Names lists the entries' names in order.
func (entries Entries[T]) Names() []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

// UnmarshalJSON reads an object key by key, each value strictly.
func (entries *Entries[T]) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected an object, got %v", token)
	}
	seen := map[string]bool{}
	var out Entries[T]
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name := token.(string)
		if seen[name] {
			return fmt.Errorf("%q is declared twice", name)
		}
		seen[name] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		var value T
		if err := strictDecode(raw, &value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, Entry[T]{Name: name, Value: value})
	}
	*entries = out
	return nil
}

// strictDecode decodes one JSON value, refusing a key the format does not have.
func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after the value")
	}
	return nil
}

var (
	contractIdentifierRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	contractAttributeRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	contractVersionRe    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// nativeEvents are DOM event names a component may not announce under: data-kit-event:click must
// mean one thing, the DOM's. The list is the events a page is likely to listen to with the same
// directive, not every event the platform defines.
var nativeEvents = map[string]bool{
	"click": true, "dblclick": true, "submit": true, "input": true, "change": true, "keydown": true,
	"keyup": true, "keypress": true, "pointerdown": true, "pointerup": true, "pointermove": true,
	"pointerenter": true, "pointerleave": true, "pointercancel": true, "mousedown": true,
	"mouseup": true, "mousemove": true, "mouseenter": true, "mouseleave": true, "mouseover": true,
	"mouseout": true, "focus": true, "blur": true, "focusin": true, "focusout": true, "scroll": true,
	"wheel": true, "paste": true, "copy": true, "cut": true, "drag": true, "dragstart": true,
	"dragend": true, "dragenter": true, "dragleave": true, "dragover": true, "drop": true,
	"touchstart": true, "touchmove": true, "touchend": true, "touchcancel": true, "contextmenu": true,
	"select": true, "selectionchange": true, "resize": true, "load": true, "error": true,
	"toggle": true, "close": true, "cancel": true, "reset": true, "invalid": true, "beforeinput": true,
	"animationend": true, "transitionend": true,
}

// reservedMembers are blueprint keys the kernel gives a meaning to; a contract cannot declare them
// as state or method.
var reservedMembers = map[string]bool{"init": true, "top": true, "parent": true, "self": true}

// ParseContract reads one contract and checks it is well formed. It does not compare it with the
// code; the browser test does that.
func ParseContract(data []byte) (Contract, error) {
	var contract Contract
	if err := strictDecode(data, &contract); err != nil {
		return Contract{}, err
	}
	return contract, contract.check()
}

func (contract Contract) check() error {
	var problems []string
	fail := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if !componentNameRe.MatchString(contract.Name) || contract.Name == coreName {
		fail("name %q is not a component name", contract.Name)
	}
	if !contractVersionRe.MatchString(contract.Version) {
		fail("version %q is not major.minor.patch", contract.Version)
	}
	if strings.TrimSpace(contract.Summary) == "" {
		fail("summary is empty")
	}
	checkAttributes := func(where string, attributes Entries[Attribute]) {
		for _, attribute := range attributes {
			if !contractAttributeRe.MatchString(attribute.Name) {
				fail("%s attribute %q is not an attribute name", where, attribute.Name)
			}
			if strings.HasPrefix(attribute.Name, "data-kit-") {
				fail("%s attribute %q is grammar, not configuration", where, attribute.Name)
			}
			if strings.TrimSpace(attribute.Value.Doc) == "" {
				fail("%s attribute %q has no doc", where, attribute.Name)
			}
		}
	}
	checkAttributes("host", contract.Host.Attributes)
	for _, element := range contract.Elements {
		if !componentNameRe.MatchString(element.Name) {
			fail("element %q is not an element name", element.Name)
		}
		if strings.TrimSpace(element.Value.Doc) == "" {
			fail("element %q has no doc", element.Name)
		}
		checkAttributes("element "+element.Name, element.Value.Attributes)
	}
	members := map[string]string{}
	member := func(kind, name, doc string) {
		if !contractIdentifierRe.MatchString(name) {
			fail("%s %q is not a name an expression can read", kind, name)
		}
		if reservedMembers[name] {
			fail("%s %q is reserved by the kernel", kind, name)
		}
		if other, ok := members[name]; ok {
			fail("%s %q is also declared as %s", kind, name, other)
		}
		members[name] = kind
		if strings.TrimSpace(doc) == "" {
			fail("%s %q has no doc", kind, name)
		}
	}
	for _, field := range contract.State {
		member("state", field.Name, field.Value.Doc)
		if strings.TrimSpace(field.Value.Type) == "" {
			fail("state %q has no type", field.Name)
		}
		if field.Value.Computed && field.Value.Page {
			fail("state %q cannot be both computed and the page's", field.Name)
		}
	}
	for _, method := range contract.Methods {
		member("method", method.Name, method.Value.Doc)
		for _, param := range method.Value.Params {
			if !contractIdentifierRe.MatchString(strings.TrimSuffix(param, "?")) {
				fail("method %q parameter %q is not a name", method.Name, param)
			}
		}
	}
	for _, event := range contract.Events {
		if !componentNameRe.MatchString(event.Name) {
			fail("event %q is not an event name", event.Name)
		}
		if nativeEvents[event.Name] {
			fail("event %q is a DOM event; data-kit-event:%s must keep meaning the DOM's", event.Name, event.Name)
		}
		if strings.TrimSpace(event.Value.Doc) == "" {
			fail("event %q has no doc", event.Name)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// contractCache memoizes parsed contracts by "<name>@v<version>"; a miss is stored as nil.
var contractCache sync.Map

type contractResult struct {
	contract Contract
	err      error
}

// ComponentContract returns the contract of one catalogue component. The version is written as the
// markup writes it ("v1.0.0" or "1.0.0"); empty means the latest. ok is false when the component or
// that version does not exist, or has no contract; err reports a contract that does not parse.
func ComponentContract(name, version string) (contract Contract, ok bool, err error) {
	if !componentNameRe.MatchString(name) || name == coreName {
		return Contract{}, false, nil
	}
	if version == "" {
		latest := findLatestComponentVersion(name)
		if latest == "" {
			return Contract{}, false, nil
		}
		version = strings.TrimSuffix(latest, ".js")
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !componentVersionRe.MatchString(version) {
		return Contract{}, false, nil
	}
	key := name + "@" + version
	if cached, hit := contractCache.Load(key); hit {
		if cached == nil {
			return Contract{}, false, nil
		}
		result := cached.(contractResult)
		return result.contract, true, result.err
	}
	data, readErr := jsFS.ReadFile("components/" + name + "/" + version + ".json")
	if readErr != nil {
		contractCache.Store(key, nil)
		return Contract{}, false, nil
	}
	parsed, parseErr := ParseContract(data)
	if parseErr == nil && (parsed.Name != name || "v"+parsed.Version != version) {
		parseErr = fmt.Errorf("the file is %s/%s.json but says %s@v%s", name, version, parsed.Name, parsed.Version)
	}
	if parseErr != nil {
		parseErr = fmt.Errorf("%s: %w", key, parseErr)
	}
	contractCache.Store(key, contractResult{contract: parsed, err: parseErr})
	return parsed, true, parseErr
}

// ComponentVersionFiles lists every catalogue component version as "<name>@v<version>", sorted.
func ComponentVersionFiles() []string {
	directories, err := jsFS.ReadDir("components")
	if err != nil {
		return nil
	}
	var out []string
	for _, directory := range directories {
		if !directory.IsDir() {
			continue
		}
		files, err := jsFS.ReadDir("components/" + directory.Name())
		if err != nil {
			continue
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".js") {
				out = append(out, directory.Name()+"@"+strings.TrimSuffix(file.Name(), ".js"))
			}
		}
	}
	sort.Strings(out)
	return out
}
