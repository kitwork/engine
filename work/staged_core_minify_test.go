//go:build !stdminify

package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	kitjavascript "github.com/kitwork/engine/jit/javascript"
)

type stagedCorePolicyAsset struct {
	tag   stagedJITTag
	asset kitjavascript.Asset
}

type stagedCorePolicyFixture struct {
	assets map[string]stagedCorePolicyAsset
}

func TestNewRenderPlanAppliesStagedCoreMinifyPolicy(t *testing.T) {
	savedAllowLocal := AllowLocal
	defer func() { AllowLocal = savedAllowLocal }()

	readable := prepareStagedCorePolicyFixture(t, true)
	production := prepareStagedCorePolicyFixture(t, false)

	readableAssembly, err := kitjavascript.BuildStaged(kitjavascript.StagedBuildOptions{
		Profile: kitjavascript.ProfileHydrate,
	})
	if err != nil {
		t.Fatal(err)
	}
	minifiedAssembly, err := kitjavascript.BuildStaged(kitjavascript.StagedBuildOptions{
		Profile:    kitjavascript.ProfileHydrate,
		MinifyCore: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if readableAssembly.Hydrate == nil || minifiedAssembly.Hydrate == nil {
		t.Fatal("Hydrate profile omitted its staged Hydrate core")
	}

	assertStagedCorePolicyBytes(t, readable, "runtime", readableAssembly.Runtime.Bytes())
	assertStagedCorePolicyBytes(t, readable, "hydrate", readableAssembly.Hydrate.Bytes())
	assertStagedCorePolicyBytes(t, production, "runtime", minifiedAssembly.Runtime.Bytes())
	assertStagedCorePolicyBytes(t, production, "hydrate", minifiedAssembly.Hydrate.Bytes())

	for _, role := range []string{"runtime", "hydrate"} {
		localAsset := readable.assets[role].asset
		productionAsset := production.assets[role].asset
		if bytes.Equal(localAsset.JavaScript, productionAsset.JavaScript) {
			t.Fatalf("%s bytes did not change between readable and production generations", role)
		}
		if len(productionAsset.JavaScript) >= len(localAsset.JavaScript) {
			t.Fatalf("production %s was not minified: readable=%d production=%d",
				role, len(localAsset.JavaScript), len(productionAsset.JavaScript))
		}
		if productionAsset.ContentHash == localAsset.ContentHash {
			t.Fatalf("%s readable and minified bytes reused one content hash", role)
		}
	}

	for _, role := range []string{"service", "component"} {
		local := readable.assets[role]
		production := production.assets[role]
		if !bytes.Equal(local.asset.JavaScript, production.asset.JavaScript) ||
			local.asset.ContentHash != production.asset.ContentHash ||
			local.tag.integrity != production.tag.integrity {
			t.Fatalf("%s package bytes changed with staged-core policy: readable=%+v production=%+v",
				role, local.tag, production.tag)
		}
	}

	if readable.assets["graph"].asset.ContentHash == production.assets["graph"].asset.ContentHash {
		t.Fatal("graph identity did not incorporate the selected staged-core bytes")
	}
}

func prepareStagedCorePolicyFixture(t *testing.T, allowLocal bool) stagedCorePolicyFixture {
	t.Helper()
	AllowLocal = allowLocal
	tenant, _ := writeKitJSTestSite(t, `router.jitjs(true);`,
		`<main data-kit-component="progress-bar@2.0.0"></main>`)
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	tags, _ := serveKitJSPage(t, tenant, "/")
	identities := []struct {
		key    string
		role   string
		suffix string
	}{
		{key: "runtime", role: "runtime", suffix: "runtime"},
		{key: "hydrate", role: "hydrate", suffix: "hydrate"},
		{key: "graph", role: "graph", suffix: "graph"},
		{key: "service", role: "service", suffix: "progress"},
		{key: "component", role: "component", suffix: "progress-bar"},
	}
	fixture := stagedCorePolicyFixture{assets: make(map[string]stagedCorePolicyAsset, len(identities))}
	for _, identity := range identities {
		tag, ok := findStagedJITTag(tags, identity.role, identity.suffix)
		if !ok {
			t.Fatalf("%s.%s staged tag missing", identity.role, identity.suffix)
		}
		asset, ok := tenant.renderPlan().kitJSAsset(tag.hash)
		if !ok {
			t.Fatalf("%s.%s staged asset missing", identity.role, identity.suffix)
		}
		assertStagedCorePolicyIdentity(t, tag, asset)

		response := httptest.NewRecorder()
		tenant.Serve(response, httptest.NewRequest(http.MethodGet, "http://localhost"+tag.path, nil))
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), asset.JavaScript) {
			t.Fatalf("GET %s status=%d bytes=%d, want exact prepared asset",
				tag.path, response.Code, response.Body.Len())
		}
		fixture.assets[identity.key] = stagedCorePolicyAsset{tag: tag, asset: asset}
	}
	return fixture
}

func assertStagedCorePolicyBytes(t *testing.T, fixture stagedCorePolicyFixture, role string, want []byte) {
	t.Helper()
	got, ok := fixture.assets[role]
	if !ok {
		t.Fatalf("%s fixture asset missing", role)
	}
	if !bytes.Equal(got.asset.JavaScript, want) {
		t.Fatalf("%s generation policy selected unexpected bytes: got=%d want=%d",
			role, len(got.asset.JavaScript), len(want))
	}
}

func assertStagedCorePolicyIdentity(t *testing.T, tag stagedJITTag, asset kitjavascript.Asset) {
	t.Helper()
	sum := sha256.Sum256(asset.JavaScript)
	wantHash := hex.EncodeToString(sum[:])
	wantIntegrity := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	if tag.hash != wantHash || asset.ContentHash != wantHash || tag.integrity != wantIntegrity ||
		asset.Integrity != wantIntegrity || asset.Name != wantHash+"."+tag.suffix+".js" {
		t.Fatalf("staged identity does not describe served bytes: tag=%+v asset={role:%s hash:%s name:%s integrity:%s}",
			tag, asset.Role, asset.ContentHash, asset.Name, asset.Integrity)
	}
}
