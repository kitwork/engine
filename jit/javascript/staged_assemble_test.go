package javascript

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestBuildStagedProducesStableRoleAddressedArtifacts(t *testing.T) {
	options := stagedTestOptions(ProfileHydrate, []string{"tabs", "dialog"})
	assembly, err := BuildStaged(options)
	if err != nil {
		t.Fatal(err)
	}
	kitAssembly, err := BuildStaged(stagedTestOptions(ProfileKit, []string{"dialog", "tabs"}))
	if err != nil {
		t.Fatal(err)
	}

	if assembly.Hydrate == nil || kitAssembly.Hydrate != nil {
		t.Fatalf("profile addons = hydrate:%v kit:%v", assembly.Hydrate != nil, kitAssembly.Hydrate != nil)
	}
	if assembly.Runtime.SHA256() != kitAssembly.Runtime.SHA256() ||
		!bytes.Equal(assembly.Runtime.Bytes(), kitAssembly.Runtime.Bytes()) {
		t.Fatal("common runtime depends on profile or package graph")
	}
	if bytes.Contains(assembly.Runtime.Bytes(), []byte(`global.kit = kit`)) ||
		bytes.Contains(assembly.Runtime.Bytes(), []byte(`profile marker`)) ||
		bytes.Contains(assembly.Runtime.Bytes(), []byte(`core.morph = morph`)) {
		t.Fatal("common runtime contains profile, Hydrate, or boot publication")
	}
	if !bytes.Contains(assembly.Runtime.Bytes(), []byte(`incomplete staged delivery was not published`)) {
		t.Fatal("common runtime has no fail-closed missing-graph watchdog")
	}
	if assembly.ComponentsBundle == nil || assembly.ComponentsBundle.Role() != JITRoleComponents {
		t.Fatal("shared component selection did not produce one components artifact")
	}
	if got := len(assembly.Components); got != 1 || assembly.Components[0].Package() != "app" {
		t.Fatalf("individual components = %#v", assembly.Components)
	}

	wantRoles := []JITRole{
		JITRoleRuntime, JITRoleHydrate, JITRoleGraph,
		JITRoleService, JITRoleService, JITRoleComponents, JITRoleComponent,
	}
	artifacts := assembly.Artifacts()
	if len(artifacts) != len(wantRoles) {
		t.Fatalf("artifact count = %d, want %d", len(artifacts), len(wantRoles))
	}
	canonicalName := regexp.MustCompile(`^[0-9a-f]{64}\.[A-Za-z0-9][A-Za-z0-9._-]*\.js$`)
	for index, artifact := range artifacts {
		if artifact.Role() != wantRoles[index] {
			t.Fatalf("artifact %d role = %q, want %q", index, artifact.Role(), wantRoles[index])
		}
		if artifact.SHA256() != ContentHash(artifact.Bytes()) {
			t.Fatalf("artifact %s hash does not identify exact bytes", artifact.Name())
		}
		sum := sha256.Sum256(artifact.Bytes())
		if artifact.Integrity() != "sha256-"+base64.StdEncoding.EncodeToString(sum[:]) {
			t.Fatalf("artifact %s integrity does not identify exact bytes", artifact.Name())
		}
		if !canonicalName.MatchString(artifact.Name()) ||
			artifact.Name() != artifact.SHA256()+"."+artifact.Suffix()+".js" {
			t.Fatalf("artifact name = %q", artifact.Name())
		}
	}

	graph := assembly.Graph.Bytes()
	for _, artifact := range artifacts {
		if artifact.Role() == JITRoleGraph {
			continue
		}
		if !bytes.Contains(graph, []byte(artifact.SHA256())) {
			t.Fatalf("graph source does not cover %s hash", artifact.Role())
		}
		if !bytes.Contains(graph, []byte(artifact.Integrity())) {
			t.Fatalf("graph source does not cover %s integrity", artifact.Role())
		}
	}
	for _, required := range [][]byte{
		[]byte(assembly.GraphKey()),
		[]byte(`core.installStagedDelivery(delivery)`),
		[]byte(`componentHashes`),
		[]byte(`handoff.graph(graphScript, graph, delivery)`),
		[]byte(`graph.artifact = graphHash`),
		[]byte(`installed.artifact !== graph.artifact`),
		[]byte(`rawSource !== expectedSource`),
		[]byte(`script.getAttribute("crossorigin") !== "anonymous"`),
		[]byte(`script.hasAttribute("nomodule")`),
		[]byte(`document.readyState === "loading" || document.readyState === "interactive"`),
	} {
		if !bytes.Contains(graph, required) {
			t.Fatalf("graph source is missing %q", required)
		}
	}
	if bytes.Contains(graph, []byte("data-kitwork-plan")) {
		t.Fatal("deprecated plan marker leaked into staged graph")
	}
}

func TestBuildStagedPreservesPackageSourcesAndNormalizesDiscoveryOrder(t *testing.T) {
	leftOptions := stagedTestOptions(ProfileHydrate, []string{"dialog", "tabs"})
	rightOptions := stagedTestOptions(ProfileHydrate, []string{"tabs", "dialog"})
	reverseServices(rightOptions.Services)
	reverseComponents(rightOptions.Components)
	reverseRequirements(rightOptions.ComponentRequires)

	left, err := BuildStaged(leftOptions)
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildStaged(rightOptions)
	if err != nil {
		t.Fatal(err)
	}
	if left.GraphKey() != right.GraphKey() {
		t.Fatal("equivalent discovery order changed the graph key")
	}
	leftArtifacts := left.Artifacts()
	rightArtifacts := right.Artifacts()
	if len(leftArtifacts) != len(rightArtifacts) {
		t.Fatal("equivalent staged deliveries changed artifact count")
	}
	for index := range leftArtifacts {
		if leftArtifacts[index].Name() != rightArtifacts[index].Name() ||
			!bytes.Equal(leftArtifacts[index].Bytes(), rightArtifacts[index].Bytes()) {
			t.Fatalf("equivalent delivery changed artifact %d", index)
		}
	}

	for _, service := range leftOptions.Services {
		artifact := stagedArtifactByPackage(t, left.Services, service.Name)
		if bytes.Count(artifact.Bytes(), service.Source) != 1 {
			t.Fatalf("service %s source was transformed or duplicated", service.Name)
		}
	}
	for _, component := range leftOptions.Components {
		var source []byte
		if component.Name == "app" {
			source = stagedArtifactByPackage(t, left.Components, component.Name).Bytes()
		} else {
			source = left.ComponentsBundle.Bytes()
		}
		if bytes.Count(source, component.Source) != 1 {
			t.Fatalf("component %s source was transformed or duplicated", component.Name)
		}
		if !bytes.Contains(source, []byte(ContentHash(component.Source))) {
			t.Fatalf("component %s artifact does not carry its raw source hash", component.Name)
		}
	}

	before := left.ComponentsBundle.Bytes()
	leftOptions.Components[0].Source[2] = 'X'
	leftOptions.Services[0].Source[2] = 'X'
	if !bytes.Equal(before, left.ComponentsBundle.Bytes()) {
		t.Fatal("caller mutation changed immutable component bundle bytes")
	}
}

func TestBuildStagedPackageArtifactsAreReusableAcrossGraphs(t *testing.T) {
	baseOptions := stagedTestOptions(ProfileKit, nil)
	baseOptions.Components = []ComponentPackage{baseOptions.Components[1], baseOptions.Components[2]}
	baseOptions.ComponentRequires = baseOptions.ComponentRequires[:1]
	base, err := BuildStaged(baseOptions)
	if err != nil {
		t.Fatal(err)
	}
	extended, err := BuildStaged(stagedTestOptions(ProfileKit, nil))
	if err != nil {
		t.Fatal(err)
	}
	if base.GraphKey() == extended.GraphKey() {
		t.Fatal("different component sets reused one graph key")
	}
	for _, name := range []string{"app", "dialog"} {
		left := stagedArtifactByPackage(t, base.Components, name)
		right := stagedArtifactByPackage(t, extended.Components, name)
		if left.SHA256() != right.SHA256() || !bytes.Equal(left.Bytes(), right.Bytes()) {
			t.Fatalf("component %s artifact depends on the surrounding graph", name)
		}
	}
	for _, name := range []string{"storage", "share"} {
		left := stagedArtifactByPackage(t, base.Services, name)
		right := stagedArtifactByPackage(t, extended.Services, name)
		if left.SHA256() != right.SHA256() || !bytes.Equal(left.Bytes(), right.Bytes()) {
			t.Fatalf("service %s artifact depends on the surrounding graph", name)
		}
	}

	bundled, err := BuildStaged(stagedTestOptions(ProfileKit, []string{"dialog", "tabs"}))
	if err != nil {
		t.Fatal(err)
	}
	if bundled.GraphKey() == extended.GraphKey() {
		t.Fatal("changing only chunk layout did not change graph identity")
	}
	if bundled.Runtime.SHA256() != extended.Runtime.SHA256() {
		t.Fatal("chunk layout changed the stable runtime")
	}
}

func TestBuildStagedRejectsAmbiguousBundlesAndUnsafeSuffixes(t *testing.T) {
	validComponent := ComponentPackage{
		Name: "dialog", Version: "1.0.0",
		Source: []byte("; kit.component(\"dialog\", {});\n"),
	}
	tests := []struct {
		name     string
		options  StagedBuildOptions
		contains string
	}{
		{
			name: "one shared component",
			options: StagedBuildOptions{Profile: ProfileKit, Components: []ComponentPackage{validComponent},
				SharedComponentNames: []string{"dialog"}},
			contains: "at least two",
		},
		{
			name: "missing shared component",
			options: StagedBuildOptions{Profile: ProfileKit, Components: []ComponentPackage{validComponent},
				SharedComponentNames: []string{"dialog", "tabs"}},
			contains: "missing component",
		},
		{
			name: "repeated shared component",
			options: StagedBuildOptions{Profile: ProfileKit, Components: []ComponentPackage{validComponent},
				SharedComponentNames: []string{"dialog", "dialog"}},
			contains: "repeats",
		},
		{
			name: "non URL safe component",
			options: StagedBuildOptions{Profile: ProfileKit, Components: []ComponentPackage{{
				Name: "$dialog", Version: "1.0.0", Source: []byte("; kit.component(\"$dialog\", {});\n"),
			}}},
			contains: "asset suffix",
		},
		{
			name: "reserved component suffix",
			options: StagedBuildOptions{Profile: ProfileKit, Components: []ComponentPackage{{
				Name: "runtime", Version: "1.0.0", Source: []byte("; kit.component(\"runtime\", {});\n"),
			}}},
			contains: "asset suffix",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildStaged(test.options)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("BuildStaged error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestBuildStagedRejectsComponentGraphBeyondBrowserCache(t *testing.T) {
	components := make([]ComponentPackage, stagedComponentCacheLimit+1)
	for index := range components {
		name := fmt.Sprintf("component-%03d", index)
		components[index] = ComponentPackage{
			Name:    name,
			Version: "1.0.0",
			Source:  []byte("; kit.component(\"" + name + "\", {});\n"),
		}
	}
	_, err := BuildStaged(StagedBuildOptions{Profile: ProfileKit, Components: components})
	if err == nil || !strings.Contains(err.Error(), "exceeds cache limit 256") {
		t.Fatalf("BuildStaged error = %v, want component cache limit", err)
	}
}

func stagedTestOptions(profile Profile, shared []string) StagedBuildOptions {
	storageSource := []byte("; kit.service(\"storage\", { get: function () { return \"stored\"; } });\n")
	shareSource := []byte("; kit.service(\"share\", { open: function () { return kit.storage.get(); } });\n")
	return StagedBuildOptions{
		Profile: profile,
		Services: []Service{
			{Name: "share", Version: "1.0.0", Requires: []ServiceVersion{{Name: "storage", Version: "1.0.0"}}, Actions: []string{"open"}, Source: shareSource},
			{Name: "storage", Version: "1.0.0", Actions: []string{"get"}, Source: storageSource},
		},
		Components: []ComponentPackage{
			{Name: "tabs", Version: "1.0.0", Source: []byte("; kit.component(\"tabs\", { selected: 0 });\n")},
			{Name: "app", Version: "1.0.0", Source: []byte("; kit.component(\"app\", { ready: true });\n")},
			{Name: "dialog", Version: "1.0.0", Source: []byte("; kit.component(\"dialog\", { open: false });\n")},
		},
		ComponentRequires: []ComponentServiceRequirement{
			{Component: "app", Service: ServiceVersion{Name: "share", Version: "1.0.0"}},
			{Component: "app", Service: ServiceVersion{Name: "storage", Version: "1.0.0"}},
		},
		SharedComponentNames: append([]string(nil), shared...),
	}
}

func stagedArtifactByPackage(t *testing.T, artifacts []JITArtifact, name string) JITArtifact {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.Package() == name {
			return artifact
		}
	}
	t.Fatalf("artifact package %q is missing", name)
	return JITArtifact{}
}

func reverseServices(values []Service) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseComponents(values []ComponentPackage) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseRequirements(values []ComponentServiceRequirement) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
