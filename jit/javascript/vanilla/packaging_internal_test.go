package vanilla

import (
	"bytes"
	"testing"
)

func TestBuildPlacesClosedGraphBeforeBootAndCopiesPackageSource(t *testing.T) {
	storageSource := []byte("; kit.service(\"storage\", { get: function () { return \"stored\"; } });\n")
	shareSource := []byte("; kit.service(\"share\", { open: function () { return kit.storage.get(); } });\n")
	packageSource := []byte("; kit.component(\"counter\", { release: kit.version, count: 0 });\n")
	artifact, err := Build(BuildOptions{
		Profile: ProfileHydrate,
		Services: []Service{
			{Name: "share", Version: "1.0.0", Requires: []ServiceVersion{{Name: "storage", Version: "1.0.0"}}, Source: shareSource},
			{Name: "storage", Version: "1.0.0", Source: storageSource},
		},
		Components: []ComponentVersion{
			{Name: "counter", Version: "1.0.0"},
		},
		Scripts: []Script{
			{Name: "counter", Source: packageSource},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	storageSource[2] = 'X'
	shareSource[2] = 'X'
	packageSource[2] = 'X'
	source := artifact.Bytes()
	installAt := bytes.Index(source, []byte("core.installComponentGraph(graph);"))
	storageAt := bytes.Index(source, []byte(`kit.service("storage"`))
	shareAt := bytes.Index(source, []byte(`kit.service("share"`))
	sealAt := bytes.Index(source, []byte("core.sealServices();"))
	componentAt := bytes.Index(source, []byte(`kit.component("counter"`))
	bootAt := bytes.Index(source, []byte("global.kit = kit;"))
	if installAt < 0 || storageAt < 0 || shareAt < 0 || sealAt < 0 || componentAt < 0 || bootAt < 0 {
		t.Fatalf("closed graph capsule is incomplete: install=%d storage=%d share=%d seal=%d component=%d boot=%d",
			installAt, storageAt, shareAt, sealAt, componentAt, bootAt)
	}
	if !(installAt < storageAt && storageAt < shareAt && shareAt < sealAt && sealAt < componentAt && componentAt < bootAt) {
		t.Fatalf("closed graph order = install:%d storage:%d share:%d seal:%d component:%d boot:%d",
			installAt, storageAt, shareAt, sealAt, componentAt, bootAt)
	}
	if !bytes.Contains(source, []byte(`Symbol.for("kitjs:graph")`)) {
		t.Fatal("closed graph has no private graph identity marker")
	}
	if !bytes.Contains(source, []byte("var kit = core.kit;")) {
		t.Fatal("packaged sources do not share the future public kit object")
	}
	if !bytes.Contains(source, []byte(`services["storage"] = "1.0.0";`)) ||
		!bytes.Contains(source, []byte(`services["share"] = "1.0.0";`)) {
		t.Fatal("private graph does not contain the exact service manifest")
	}

	source[0] ^= 0xff
	if bytes.Equal(source, artifact.Bytes()) {
		t.Fatal("Artifact.Bytes exposed mutable artifact storage")
	}
}

func TestBuildServiceGraphIsDeterministicAndDependencyMetadataAffectsIdentity(t *testing.T) {
	a := Service{Name: "a", Version: "1.0.0", Source: []byte("; kit.service(\"a\", { value: 1 });\n")}
	b := Service{Name: "b", Version: "1.0.0", Source: []byte("; kit.service(\"b\", { value: 2 });\n")}
	consumerAB := Service{
		Name: "consumer", Version: "1.0.0",
		Requires: []ServiceVersion{{Name: "b", Version: "1.0.0"}, {Name: "a", Version: "1.0.0"}},
		Source:   []byte("; kit.service(\"consumer\", { value: 3 });\n"),
	}
	consumerBA := consumerAB
	consumerBA.Requires = []ServiceVersion{{Name: "a", Version: "1.0.0"}, {Name: "b", Version: "1.0.0"}}

	left, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{consumerAB, b, a}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{a, consumerBA, b}})
	if err != nil {
		t.Fatal(err)
	}
	if left.SHA256() != right.SHA256() || left.Name() != right.Name() || !bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("service input or dependency order changed deterministic artifact identity")
	}

	consumerOnlyA := consumerAB
	consumerOnlyA.Requires = []ServiceVersion{{Name: "a", Version: "1.0.0"}}
	changed, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{a, b, consumerOnlyA}})
	if err != nil {
		t.Fatal(err)
	}
	if left.SHA256() == changed.SHA256() || bytes.Equal(left.Bytes(), changed.Bytes()) {
		t.Fatal("service dependency metadata did not affect sealed graph identity")
	}
}

func TestBuildRejectsInvalidServiceGraphs(t *testing.T) {
	valid := func(name string) Service {
		return Service{Name: name, Version: "1.0.0", Source: []byte("; kit.service(\"" + name + "\", {});\n")}
	}
	tests := []struct {
		name     string
		services []Service
		contains string
	}{
		{name: "invalid name", services: []Service{{Name: "$storage", Version: "1.0.0", Source: []byte("; ok();\n")}}, contains: "invalid service name"},
		{name: "reserved name", services: []Service{{Name: "component", Version: "1.0.0", Source: []byte("; ok();\n")}}, contains: "invalid service name"},
		{name: "non exact version", services: []Service{{Name: "storage", Version: "latest", Source: []byte("; ok();\n")}}, contains: "non-exact SemVer"},
		{name: "invalid source", services: []Service{{Name: "storage", Version: "1.0.0", Source: []byte("kit.service")}}, contains: "must begin"},
		{name: "duplicate", services: []Service{valid("storage"), valid("storage")}, contains: "duplicate service"},
		{name: "missing", services: []Service{{Name: "share", Version: "1.0.0", Requires: []ServiceVersion{{Name: "clipboard", Version: "1.0.0"}}, Source: []byte("; ok();\n")}}, contains: "requires missing service"},
		{name: "version mismatch", services: []Service{
			valid("clipboard"),
			{Name: "share", Version: "1.0.0", Requires: []ServiceVersion{{Name: "clipboard", Version: "2.0.0"}}, Source: []byte("; ok();\n")},
		}, contains: "but graph provides 1.0.0"},
		{name: "duplicate dependency", services: []Service{
			valid("clipboard"),
			{Name: "share", Version: "1.0.0", Requires: []ServiceVersion{{Name: "clipboard", Version: "1.0.0"}, {Name: "clipboard", Version: "1.0.0"}}, Source: []byte("; ok();\n")},
		}, contains: "repeats dependency"},
		{name: "cycle", services: []Service{
			{Name: "a", Version: "1.0.0", Requires: []ServiceVersion{{Name: "b", Version: "1.0.0"}}, Source: []byte("; ok();\n")},
			{Name: "b", Version: "1.0.0", Requires: []ServiceVersion{{Name: "a", Version: "1.0.0"}}, Source: []byte("; ok();\n")},
		}, contains: "dependency cycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Build(BuildOptions{Profile: ProfileKit, Services: test.services})
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(test.contains)) {
				t.Fatalf("Build error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestBuildCopiesServiceSourcesAndDependencies(t *testing.T) {
	dependency := ServiceVersion{Name: "storage", Version: "1.0.0"}
	storageSource := []byte("; kit.service(\"storage\", {});\n")
	shareSource := []byte("; kit.service(\"share\", {});\n")
	services := []Service{
		{Name: "storage", Version: "1.0.0", Source: storageSource},
		{Name: "share", Version: "1.0.0", Requires: []ServiceVersion{dependency}, Source: shareSource},
	}
	artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	want := artifact.Bytes()
	storageSource[2] = 'X'
	shareSource[2] = 'X'
	services[1].Requires[0] = ServiceVersion{Name: "other", Version: "9.9.9"}
	if !bytes.Equal(want, artifact.Bytes()) {
		t.Fatal("service input aliases mutable artifact storage")
	}
}
