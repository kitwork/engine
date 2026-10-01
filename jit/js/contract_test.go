package js

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// Every catalogue component version has a contract that parses and names its own file, and every
// contract sits beside the code it describes.
func TestEveryComponentVersionHasAContract(t *testing.T) {
	versions := ComponentVersionFiles()
	if len(versions) < 20 {
		t.Fatalf("expected the whole catalogue, found %d component versions: %v", len(versions), versions)
	}
	for _, key := range versions {
		name, version, _ := strings.Cut(key, "@")
		contract, ok, err := ComponentContract(name, version)
		if !ok {
			t.Errorf("%s has no contract: add components/%s/%s.json", key, name, version)
			continue
		}
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if contract.Name != name || "v"+contract.Version != version {
			t.Errorf("%s: contract says %s@v%s", key, contract.Name, contract.Version)
		}
	}

	directories, _ := jsFS.ReadDir("components")
	for _, directory := range directories {
		files, _ := jsFS.ReadDir("components/" + directory.Name())
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			code := strings.TrimSuffix(file.Name(), ".json") + ".js"
			if _, err := jsFS.ReadFile("components/" + directory.Name() + "/" + code); err != nil {
				t.Errorf("components/%s/%s describes code that does not exist", directory.Name(), file.Name())
			}
		}
	}
}

// The latest version is what an unversioned name means, for the contract as for the code.
func TestComponentContractResolvesTheLatestVersion(t *testing.T) {
	contract, ok, err := ComponentContract("clipboard", "")
	if !ok || err != nil || contract.Version != "2.0.0" {
		t.Fatalf("clipboard latest = %+v ok=%v err=%v, want 2.0.0", contract.Version, ok, err)
	}
	if contract, ok, _ := ComponentContract("clipboard", "1.0.0"); !ok || contract.Version != "1.0.0" {
		t.Fatalf("clipboard 1.0.0 = %q ok=%v", contract.Version, ok)
	}
	for _, name := range []string{"nope", "core", "../tree", "Tree"} {
		if _, ok, _ := ComponentContract(name, ""); ok {
			t.Errorf("%q resolved to a contract", name)
		}
	}
	if _, ok, _ := ComponentContract("tree", "v9.9.9"); ok {
		t.Error("a version that does not exist resolved to a contract")
	}
}

// Sections keep the order they are written in — the catalogue prints them that way.
func TestContractKeepsWrittenOrder(t *testing.T) {
	contract, _, err := ComponentContract("sidebar", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(contract.Methods.Names(), ",")
	want := "cycle,expand,collapse,hide,toggle,set,openDrawer,closeDrawer,toggleDrawer,isExpanded,isCollapsed,isHidden"
	if got != want {
		t.Fatalf("methods in order %s, want %s", got, want)
	}
}

// A contract that is wrong says why. Each case is the smallest change from a good contract, and the
// good one passing is the control: a case that failed for some other reason would not prove its own.
func TestParseContractRefusesMistakes(t *testing.T) {
	good := `{"name":"thing","version":"1.0.0","summary":"A thing.",
	  "host":{"attributes":{"data-size":{"type":"number","doc":"How big."}}},
	  "elements":{"part":{"required":true,"attributes":{"data-id":{"required":true,"doc":"Its name."}},"doc":"A part."}},
	  "state":{"open":{"type":"boolean","doc":"Open."}},
	  "methods":{"show":{"params":["name?"],"doc":"Shows."}},
	  "events":{"seen":{"doc":"Seen."}}}`
	if _, err := ParseContract([]byte(good)); err != nil {
		t.Fatalf("the control contract was refused: %v", err)
	}
	cases := []struct{ label, from, to, reason string }{
		{"a misspelt key", `"required":true,"attributes"`, `"requird":true,"attributes"`, `unknown field "requird"`},
		{"a key declared twice", `"state":{"open":{"type":"boolean","doc":"Open."}}`, `"state":{"open":{"type":"boolean","doc":"Open."},"open":{"type":"boolean","doc":"Again."}}`, `"open" is declared twice`},
		{"a DOM event name", `"events":{"seen"`, `"events":{"click"`, `"click" is a DOM event`},
		{"a reserved member", `"methods":{"show"`, `"methods":{"init"`, `"init" is reserved`},
		{"a name used twice", `"methods":{"show"`, `"methods":{"open"`, `"open" is also declared as state`},
		{"a missing doc", `"doc":"Shows."`, `"doc":""`, `method "show" has no doc`},
		{"a state with no type", `{"type":"boolean","doc":"Open."}`, `{"doc":"Open."}`, `state "open" has no type`},
		{"grammar as configuration", `"data-size"`, `"data-kit-size"`, `is grammar, not configuration`},
		{"a bad version", `"version":"1.0.0"`, `"version":"v1"`, `is not major.minor.patch`},
		{"a section that is not an object", `"events":{"seen":{"doc":"Seen."}}`, `"events":["seen"]`, `expected an object`},
	}
	for _, c := range cases {
		bad := strings.Replace(good, c.from, c.to, 1)
		if bad == good {
			t.Fatalf("%s: the case did not change the contract", c.label)
		}
		_, err := ParseContract([]byte(bad))
		if err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: err = %v, want it to say %q", c.label, err, c.reason)
		}
	}
}

// The contract and the code say the same thing. The kernel's own registry (kit.blueprints) is read
// in a real browser, so the comparison is with what the kernel will copy onto a host — getters as
// getters, a method assigned after the literal (clipboard's init) included — not with a regex over
// the source. Everything on a blueprint is in the contract and everything in the contract is on the
// blueprint, except the page's own names (kanban's cards), which must NOT be on it.
func TestBrowserContractsMatchTheCode(t *testing.T) {
	type surface struct {
		State    []string `json:"state"`
		Computed []string `json:"computed"`
		Methods  []string `json:"methods"`
		Page     []string `json:"page"`
	}
	expected := map[string]surface{}
	versions := ComponentVersionFiles()
	for _, key := range versions {
		name, version, _ := strings.Cut(key, "@")
		contract, ok, err := ComponentContract(name, version)
		if !ok || err != nil {
			t.Fatalf("%s: contract missing or broken (%v); TestEveryComponentVersionHasAContract says which", key, err)
		}
		s := surface{State: []string{}, Computed: []string{}, Methods: contract.Methods.Names(), Page: []string{}}
		for _, field := range contract.State {
			switch {
			case field.Value.Computed:
				s.Computed = append(s.Computed, field.Name)
			case field.Value.Page:
				s.Page = append(s.Page, field.Name)
			default:
				s.State = append(s.State, field.Name)
			}
		}
		for _, list := range [][]string{s.State, s.Computed, s.Methods} {
			sort.Strings(list)
		}
		expected[key] = s
	}
	data, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}

	runBrowserProof(t, "contracts", versions, `
<script>
var expected = `+string(data)+`;
var problems = [];
function sorted(list) { return list.slice().sort(); }
function same(a, b) { return JSON.stringify(sorted(a)) === JSON.stringify(sorted(b)); }
Object.keys(expected).forEach(function (key) {
  var def = window.kit.blueprints[key];
  if (!def) { problems.push(key + ": not registered"); return; }
  var actual = { state: [], computed: [], methods: [] };
  Object.keys(def).forEach(function (name) {
    if (name === "init") return;
    var descriptor = Object.getOwnPropertyDescriptor(def, name);
    if (descriptor.get || descriptor.set) actual.computed.push(name);
    else if (typeof descriptor.value === "function") actual.methods.push(name);
    else actual.state.push(name);
  });
  var want = expected[key];
  ["state", "computed", "methods"].forEach(function (section) {
    if (!same(actual[section], want[section])) {
      problems.push(key + " " + section + ": code has [" + sorted(actual[section]) + "], contract has [" + sorted(want[section]) + "]");
    }
  });
  want.page.forEach(function (name) {
    if (Object.prototype.hasOwnProperty.call(def, name)) problems.push(key + ": " + name + " is declared the page's but the blueprint owns it");
  });
});
if (Object.keys(expected).length < 20) problems.push("only " + Object.keys(expected).length + " components were compared");
if (problems.length) fail(problems.join(" | "));
root.setAttribute("data-kit-test", "passed");
</script>`)
}
