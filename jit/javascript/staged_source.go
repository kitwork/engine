package javascript

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func stagedServiceSource(service Service) ([]byte, error) {
	nameJSON, _ := json.Marshal(service.Name)
	versionJSON, _ := json.Marshal(service.Version)
	var output bytes.Buffer
	writeStagedPackageStart(&output, "service", nameJSON, versionJSON)
	_, _ = output.WriteString("    if (core.reuse) return;\n")
	_, _ = output.WriteString("    if (core.graph.services[name] !== version || !(core.serviceRegistry instanceof Map)) {\n")
	_, _ = output.WriteString("      throw new Error(\"KitJS: staged service does not match the open graph\");\n")
	_, _ = output.WriteString("    }\n")
	_, _ = output.WriteString("    var before = core.serviceRegistry.size;\n")
	_, _ = output.WriteString("    ; (function (kit) {\n")
	_, _ = output.Write(service.Source)
	_, _ = output.WriteString("    })(core.kit);\n")
	_, _ = output.WriteString("    if (core.serviceRegistry.size !== before + 1 || !core.serviceRegistry.has(name)) {\n")
	_, _ = output.WriteString("      throw new Error(\"KitJS: staged service must register exactly its declared package\");\n")
	_, _ = output.WriteString("    }\n")
	writeStagedPackageEnd(&output)
	return output.Bytes(), nil
}

func stagedComponentSource(component normalizedComponentPackage) ([]byte, error) {
	nameJSON, _ := json.Marshal(component.identity.Name)
	versionJSON, _ := json.Marshal(component.identity.Version)
	sourceHashJSON, _ := json.Marshal(component.sourceHash)
	var output bytes.Buffer
	writeStagedPackageHeader(&output)
	_, _ = output.WriteString("  var install = Object.freeze(function (kit) {\n")
	_, _ = output.Write(component.source)
	_, _ = output.WriteString("  });\n")
	_, _ = output.WriteString("  var handoffScript = document.currentScript;\n")
	_, _ = output.WriteString("  if (handoffScript && handoffScript.hasAttribute(\"data-kitwork-handoff\")) {\n")
	_, _ = output.WriteString("    var handoff = document[Symbol.for(\"kitjs:handoff\")];\n")
	_, _ = output.WriteString("    if (!handoff || typeof handoff.component !== \"function\") return;\n")
	_, _ = output.WriteString("    handoff.component(handoffScript, Object.freeze({ name: ")
	_, _ = output.Write(nameJSON)
	_, _ = output.WriteString(", version: ")
	_, _ = output.Write(versionJSON)
	_, _ = output.WriteString(", sourceHash: ")
	_, _ = output.Write(sourceHashJSON)
	_, _ = output.WriteString(", install: install }));\n")
	_, _ = output.WriteString("    return;\n  }\n")
	writeStagedPackageBodyStart(&output, "component", nameJSON, versionJSON)
	_, _ = output.WriteString("    if (core.reuse) return;\n")
	_, _ = output.WriteString("    var sourceHash = ")
	_, _ = output.Write(sourceHashJSON)
	_, _ = output.WriteString(";\n")
	writeStagedComponentRegistration(&output, "install")
	writeStagedPackageEnd(&output)
	return output.Bytes(), nil
}

func stagedComponentsBundleSource(components []stagedComponentArtifact) ([]byte, error) {
	var output bytes.Buffer
	writeStagedPackageHeader(&output)
	_, _ = output.WriteString("  var componentPackages = Object.freeze([\n")
	for _, entry := range components {
		nameJSON, _ := json.Marshal(entry.component.identity.Name)
		versionJSON, _ := json.Marshal(entry.component.identity.Version)
		sourceHashJSON, _ := json.Marshal(entry.component.sourceHash)
		_, _ = output.WriteString("    Object.freeze({ name: ")
		_, _ = output.Write(nameJSON)
		_, _ = output.WriteString(", version: ")
		_, _ = output.Write(versionJSON)
		_, _ = output.WriteString(", sourceHash: ")
		_, _ = output.Write(sourceHashJSON)
		_, _ = output.WriteString(", install: Object.freeze(function (kit) {\n")
		_, _ = output.Write(entry.component.source)
		_, _ = output.WriteString("    }) }),\n")
	}
	_, _ = output.WriteString("  ]);\n")
	_, _ = output.WriteString("  var handoffScript = document.currentScript;\n")
	_, _ = output.WriteString("  if (handoffScript && handoffScript.hasAttribute(\"data-kitwork-handoff\")) {\n")
	_, _ = output.WriteString("    var handoff = document[Symbol.for(\"kitjs:handoff\")];\n")
	_, _ = output.WriteString("    if (!handoff || typeof handoff.components !== \"function\") return;\n")
	_, _ = output.WriteString("    handoff.components(handoffScript, componentPackages);\n")
	_, _ = output.WriteString("    return;\n  }\n")
	writeStagedPackageBodyStart(&output, "components", []byte(`""`), []byte(`""`))
	_, _ = output.WriteString("    if (core.reuse) return;\n")
	_, _ = output.WriteString("    for (var packageIndex = 0; packageIndex < componentPackages.length; packageIndex++) {\n")
	_, _ = output.WriteString("      var componentPackage = componentPackages[packageIndex];\n")
	_, _ = output.WriteString("      name = componentPackage.name;\n")
	_, _ = output.WriteString("      version = componentPackage.version;\n")
	_, _ = output.WriteString("      var sourceHash = componentPackage.sourceHash;\n")
	writeStagedComponentRegistration(&output, "componentPackage.install")
	_, _ = output.WriteString("    }\n")
	writeStagedPackageEnd(&output)
	return output.Bytes(), nil
}

func writeStagedPackageStart(output *bytes.Buffer, role string, nameJSON, versionJSON []byte) {
	writeStagedPackageHeader(output)
	writeStagedPackageBodyStart(output, role, nameJSON, versionJSON)
}

func writeStagedPackageHeader(output *bytes.Buffer) {
	_, _ = output.WriteString("; (function (document) {\n")
	_, _ = output.WriteString("  \"use strict\";\n\n")
}

func writeStagedPackageBodyStart(output *bytes.Buffer, role string, nameJSON, versionJSON []byte) {
	roleJSON, _ := json.Marshal(role)
	_, _ = output.WriteString("  var core = document[Symbol.for(\"kitjs:assembly\")];\n")
	_, _ = output.WriteString("  if (!core || !core.graph || typeof core.assertStagedPackage !== \"function\") {\n")
	_, _ = output.WriteString("    throw new Error(\"KitJS: staged package loaded without an open graph\");\n")
	_, _ = output.WriteString("  }\n")
	_, _ = output.WriteString("  try {\n")
	_, _ = output.WriteString("    var name = ")
	_, _ = output.Write(nameJSON)
	_, _ = output.WriteString(";\n    var version = ")
	_, _ = output.Write(versionJSON)
	_, _ = output.WriteString(";\n")
	_, _ = output.WriteString("    core.assertStagedPackage(document.currentScript, ")
	_, _ = output.Write(roleJSON)
	_, _ = output.WriteString(", name, version);\n")
}

func writeStagedPackageEnd(output *bytes.Buffer) {
	_, _ = output.WriteString("  } catch (error) {\n")
	_, _ = output.WriteString("    core.packageError = error;\n")
	_, _ = output.WriteString("    throw error;\n")
	_, _ = output.WriteString("  }\n")
	_, _ = output.WriteString("})(document);\n")
}

func writeStagedComponentRegistration(output *bytes.Buffer, installer string) {
	_, _ = output.WriteString("    if (core.graph.components[name] !== version || !(core.registry instanceof Map)) {\n")
	_, _ = output.WriteString("      throw new Error(\"KitJS: staged component does not match the open graph\");\n")
	_, _ = output.WriteString("    }\n")
	_, _ = output.WriteString("    if (!core.graph.componentHashes || core.graph.componentHashes[name] !== sourceHash ||\n")
	_, _ = output.WriteString("      typeof core.recordStagedComponentPackage !== \"function\") {\n")
	_, _ = output.WriteString("      throw new Error(\"KitJS: staged component source does not match the open graph\");\n")
	_, _ = output.WriteString("    }\n")
	_, _ = output.WriteString("    var before = core.registry.size;\n")
	_, _ = output.WriteString("    ")
	_, _ = output.WriteString(installer)
	_, _ = output.WriteString("(core.kit);\n")
	_, _ = output.WriteString("    if (core.registry.size !== before + 1 || !core.registry.has(name)) {\n")
	_, _ = output.WriteString("      throw new Error(\"KitJS: staged component must register exactly its declared package\");\n")
	_, _ = output.WriteString("    }\n")
	_, _ = output.WriteString("    core.recordStagedComponentPackage(name, version, sourceHash);\n")
}

func stagedGraphSource(
	profile Profile,
	graphKey string,
	runtime JITArtifact,
	hydrate *JITArtifact,
	services []Service,
	serviceArtifacts []JITArtifact,
	components []normalizedComponentPackage,
	requirements []ComponentServiceRequirement,
	shared []stagedComponentArtifact,
	componentsBundle *JITArtifact,
	individual []stagedComponentArtifact,
) ([]byte, error) {
	markerName := "src/profile-kit.js"
	if profile == ProfileHydrate {
		markerName = "src/profile-hydrate.js"
	}
	profileMarker, err := sources.ReadFile(markerName)
	if err != nil {
		return nil, fmt.Errorf("kitjs: read %s: %w", markerName, err)
	}
	if err := validateRuntimeFragment(markerName, profileMarker); err != nil {
		return nil, err
	}
	bootSource, err := sources.ReadFile("src/boot.js")
	if err != nil {
		return nil, fmt.Errorf("kitjs: read src/boot.js: %w", err)
	}
	if err := validateRuntimeFragment("src/boot.js", bootSource); err != nil {
		return nil, err
	}

	var output bytes.Buffer
	_, _ = output.WriteString("; (function (global, document) {\n")
	_, _ = output.WriteString("  \"use strict\";\n\n")
	_, _ = output.WriteString("  var handoffScript = document.currentScript;\n")
	_, _ = output.WriteString("  if (handoffScript && handoffScript.hasAttribute(\"data-kitwork-handoff\")) {\n")
	_, _ = output.WriteString("    var handoff = document[Symbol.for(\"kitjs:handoff\")];\n")
	_, _ = output.WriteString("    if (!handoff || typeof handoff.graph !== \"function\") return;\n")
	if err := writeStagedGraphHandoff(&output, profile, graphKey, runtime, hydrate, services, serviceArtifacts, components, requirements, shared, componentsBundle, individual); err != nil {
		return nil, err
	}
	_, _ = output.WriteString("    return;\n  }\n")
	_, _ = output.Write(profileMarker)
	if len(services) > 0 {
		serviceRuntime, readErr := sources.ReadFile("src/service.js")
		if readErr != nil {
			return nil, fmt.Errorf("kitjs: read src/service.js: %w", readErr)
		}
		if validationErr := validateRuntimeFragment("src/service.js", serviceRuntime); validationErr != nil {
			return nil, validationErr
		}
		_, _ = output.Write(serviceRuntime)
	}
	if err := writeStagedGraphOpener(&output, profile, graphKey, runtime, hydrate, services, serviceArtifacts, components, requirements, shared, componentsBundle, individual, bootSource); err != nil {
		return nil, err
	}
	_, _ = output.WriteString("})(globalThis, document);\n")
	return output.Bytes(), nil
}

func writeStagedGraphHandoff(
	output *bytes.Buffer,
	profile Profile,
	graphKey string,
	runtime JITArtifact,
	hydrate *JITArtifact,
	services []Service,
	serviceArtifacts []JITArtifact,
	components []normalizedComponentPackage,
	requirements []ComponentServiceRequirement,
	shared []stagedComponentArtifact,
	componentsBundle *JITArtifact,
	individual []stagedComponentArtifact,
) error {
	profileJSON, err := json.Marshal(string(profile))
	if err != nil {
		return err
	}
	graphJSON, err := json.Marshal(graphKey)
	if err != nil {
		return err
	}
	writeStagedGraphManifest(output, profileJSON, graphJSON, services, components, requirements)
	_, _ = output.WriteString("    var graphScript = document.currentScript;\n")
	_, _ = output.WriteString("    var graphHash = graphScript && graphScript.getAttribute(\"data-kitwork-hash\");\n")
	_, _ = output.WriteString("    var graphIntegrity = graphScript && graphScript.getAttribute(\"integrity\");\n")
	_, _ = output.WriteString("    if (!/^[0-9a-f]{64}$/.test(String(graphHash || \"\")) || !graphIntegrity) {\n")
	_, _ = output.WriteString("      throw new Error(\"KitJS: staged handoff graph has no canonical content identity\");\n")
	_, _ = output.WriteString("    }\n")
	_, _ = output.WriteString("    var graphName = graphHash + \".graph.js\";\n")
	_, _ = output.WriteString("    graph.artifact = graphHash;\n")
	graphIndex := writeStagedDeliverySpecs(output, runtime, hydrate, serviceArtifacts, shared, componentsBundle, individual)
	_, _ = output.WriteString("    var assets = specs.map(function (asset) {\n")
	_, _ = output.WriteString("      return Object.freeze({ role: asset.role, package: asset.package, version: asset.version,\n")
	_, _ = output.WriteString("        hash: asset.hash, integrity: asset.integrity, name: asset.name,\n")
	_, _ = output.WriteString("        url: new global.URL(\"/jit/\" + asset.name, global.location.href).href,\n")
	_, _ = output.WriteString("        packages: asset.packages || null, components: asset.components || null,\n")
	_, _ = output.WriteString("        sourceHash: asset.sourceHash || null });\n")
	_, _ = output.WriteString("    });\n")
	_, _ = output.WriteString("    Object.freeze(assets);\n")
	_, _ = output.WriteString("    var graphAsset = assets[")
	_, _ = output.WriteString(fmt.Sprintf("%d", graphIndex))
	_, _ = output.WriteString("];\n")
	_, _ = output.WriteString("    var delivery = Object.freeze({ profile: ")
	_, _ = output.Write(profileJSON)
	_, _ = output.WriteString(", graphKey: ")
	_, _ = output.Write(graphJSON)
	_, _ = output.WriteString(", runtimeHash: ")
	runtimeHash, _ := json.Marshal(runtime.sha256)
	_, _ = output.Write(runtimeHash)
	_, _ = output.WriteString(", hydrateHash: ")
	if hydrate == nil {
		_, _ = output.WriteString("null")
	} else {
		hydrateHash, _ := json.Marshal(hydrate.sha256)
		_, _ = output.Write(hydrateHash)
	}
	_, _ = output.WriteString(", graphHash: graphAsset.hash, graphIntegrity: graphAsset.integrity, graphName: graphAsset.name, graphURL: graphAsset.url, assets: assets });\n")
	_, _ = output.WriteString("    handoff.graph(graphScript, graph, delivery);\n")
	return nil
}

func writeStagedGraphOpener(
	output *bytes.Buffer,
	profile Profile,
	graphKey string,
	runtime JITArtifact,
	hydrate *JITArtifact,
	services []Service,
	serviceArtifacts []JITArtifact,
	components []normalizedComponentPackage,
	requirements []ComponentServiceRequirement,
	shared []stagedComponentArtifact,
	componentsBundle *JITArtifact,
	individual []stagedComponentArtifact,
	bootSource []byte,
) error {
	profileJSON, err := json.Marshal(string(profile))
	if err != nil {
		return err
	}
	graphJSON, err := json.Marshal(graphKey)
	if err != nil {
		return err
	}
	expectedPhase := "events"
	if profile == ProfileHydrate {
		expectedPhase = "drive"
	}
	phaseJSON, _ := json.Marshal(expectedPhase)

	_, _ = output.WriteString("; (function (global, document) {\n")
	_, _ = output.WriteString("  \"use strict\";\n\n")
	_, _ = output.WriteString("  var ASSEMBLY = Symbol.for(\"kitjs:assembly\");\n")
	_, _ = output.WriteString("  var GRAPH = Symbol.for(\"kitjs:graph\");\n")
	_, _ = output.WriteString("  var core = document[ASSEMBLY];\n")
	_, _ = output.WriteString("  if (!core || core.phase !== ")
	_, _ = output.Write(phaseJSON)
	_, _ = output.WriteString(") throw new Error(\"KitJS: staged graph loaded out of order\");\n")
	writeStagedGraphManifest(output, profileJSON, graphJSON, services, components, requirements)
	writeStagedDeliveryContract(output, profileJSON, graphJSON, runtime, hydrate, serviceArtifacts, shared, componentsBundle, individual)
	writeStagedGraphInstall(output)
	writeStagedFinalizer(output, bootSource)
	_, _ = output.WriteString("})(globalThis, document);\n")
	return nil
}

func writeStagedGraphManifest(output *bytes.Buffer, profileJSON, graphJSON []byte, services []Service, components []normalizedComponentPackage, requirements []ComponentServiceRequirement) {
	_, _ = output.WriteString("  var services = Object.create(null);\n")
	for _, service := range services {
		writeStagedJSAssignment(output, "services", service.Name, service.Version)
	}
	_, _ = output.WriteString("  var components = Object.create(null);\n")
	for _, component := range components {
		writeStagedJSAssignment(output, "components", component.identity.Name, component.identity.Version)
	}
	_, _ = output.WriteString("  var componentHashes = Object.create(null);\n")
	for _, component := range components {
		writeStagedJSAssignment(output, "componentHashes", component.identity.Name, component.sourceHash)
	}
	_, _ = output.WriteString("  var actions = Object.create(null);\n")
	for _, service := range services {
		name, _ := json.Marshal(service.Name)
		_, _ = output.WriteString("  actions[")
		_, _ = output.Write(name)
		_, _ = output.WriteString("] = Object.create(null);\n")
		for _, action := range service.Actions {
			member, _ := json.Marshal(action)
			_, _ = output.WriteString("  actions[")
			_, _ = output.Write(name)
			_, _ = output.WriteString("][")
			_, _ = output.Write(member)
			_, _ = output.WriteString("] = true;\n")
		}
	}
	_, _ = output.WriteString("  var grants = Object.create(null);\n")
	for _, component := range components {
		name, _ := json.Marshal(component.identity.Name)
		_, _ = output.WriteString("  grants[")
		_, _ = output.Write(name)
		_, _ = output.WriteString("] = Object.create(null);\n")
	}
	for _, requirement := range requirements {
		componentName, _ := json.Marshal(requirement.Component)
		serviceName, _ := json.Marshal(requirement.Service.Name)
		serviceVersion, _ := json.Marshal(requirement.Service.Version)
		_, _ = output.WriteString("  grants[")
		_, _ = output.Write(componentName)
		_, _ = output.WriteString("][")
		_, _ = output.Write(serviceName)
		_, _ = output.WriteString("] = ")
		_, _ = output.Write(serviceVersion)
		_, _ = output.WriteString(";\n")
	}
	_, _ = output.WriteString("  var graph = { id: ")
	_, _ = output.Write(graphJSON)
	_, _ = output.WriteString(", profile: ")
	_, _ = output.Write(profileJSON)
	_, _ = output.WriteString(", services: services, components: components, componentHashes: componentHashes, actions: actions, grants: grants };\n")
}

func writeStagedJSAssignment(output *bytes.Buffer, object, name, value string) {
	nameJSON, _ := json.Marshal(name)
	valueJSON, _ := json.Marshal(value)
	_, _ = output.WriteString("  " + object + "[")
	_, _ = output.Write(nameJSON)
	_, _ = output.WriteString("] = ")
	_, _ = output.Write(valueJSON)
	_, _ = output.WriteString(";\n")
}

func writeStagedDeliveryContract(
	output *bytes.Buffer,
	profileJSON, graphJSON []byte,
	runtime JITArtifact,
	hydrate *JITArtifact,
	serviceArtifacts []JITArtifact,
	shared []stagedComponentArtifact,
	componentsBundle *JITArtifact,
	individual []stagedComponentArtifact,
) {
	_, _ = output.WriteString(`  function taggedScripts() {
    return Array.prototype.slice.call(document.querySelectorAll(
      "script[data-kitwork-hash]," +
      "script[data-kitwork-jit='runtime'],script[data-kitwork-jit='hydrate']," +
      "script[data-kitwork-jit='graph'],script[data-kitwork-jit='service']," +
      "script[data-kitwork-jit='component'],script[data-kitwork-jit='components']"
    ));
  }
  function absoluteURL(source) {
    try { return new URL(source, global.location.href); }
    catch (_) { return null; }
  }
  function integrityForHash(hash) {
    var binary = "";
    for (var index = 0; index < hash.length; index += 2) {
      binary += String.fromCharCode(parseInt(hash.slice(index, index + 2), 16));
    }
    return "sha256-" + global.btoa(binary);
  }
  function assertScript(script, asset) {
    var rawSource = script && script.getAttribute("src");
    var expectedSource = "/jit/" + asset.name;
    if (!script || String(script.localName || "").toLowerCase() !== "script" ||
      script.getAttribute("data-kitwork-jit") !== asset.role ||
      script.getAttribute("data-kitwork-hash") !== asset.hash ||
      script.getAttribute("integrity") !== asset.integrity ||
      script.getAttribute("crossorigin") !== "anonymous" || script.crossOrigin !== "anonymous" ||
      rawSource !== expectedSource ||
      !script.hasAttribute("defer") || script.defer !== true || script.async === true ||
      script.hasAttribute("nomodule") || script.noModule === true) {
      throw new Error("KitJS: staged script metadata does not match the sealed delivery");
    }
    var type = String(script.getAttribute("type") || "").trim().toLowerCase();
    if (type && type !== "text/javascript" && type !== "application/javascript") {
      throw new Error("KitJS: staged scripts must be classic JavaScript");
    }
    var expected = absoluteURL(expectedSource);
    if (!expected || expected.origin !== global.location.origin ||
      expected.username || expected.password) {
      throw new Error("KitJS: staged script URL does not match its content hash");
    }
    return expected.href;
  }
`)
	_, _ = output.WriteString("  var graphScript = document.currentScript;\n")
	_, _ = output.WriteString("  var graphHash = graphScript && graphScript.getAttribute(\"data-kitwork-hash\");\n")
	_, _ = output.WriteString("  if (!/^[0-9a-f]{64}$/.test(String(graphHash || \"\"))) {\n")
	_, _ = output.WriteString("    delete document[ASSEMBLY];\n")
	_, _ = output.WriteString("    throw new Error(\"KitJS: staged graph has no canonical content hash\");\n")
	_, _ = output.WriteString("  }\n")
	_, _ = output.WriteString("  var graphName = graphHash + \".graph.js\";\n")
	_, _ = output.WriteString("  var graphIntegrity = integrityForHash(graphHash);\n")
	_, _ = output.WriteString("  graph.artifact = graphHash;\n")
	graphIndex := writeStagedDeliverySpecs(output, runtime, hydrate, serviceArtifacts, shared, componentsBundle, individual)
	_, _ = output.WriteString(`  function validateScripts(prior) {
    var scripts = taggedScripts();
    if (scripts.length !== specs.length) {
      throw new Error("KitJS: staged delivery has missing or undeclared scripts");
    }
    for (var index = 0; index < specs.length; index++) {
      assertScript(scripts[index], specs[index]);
      if (prior && scripts[index] !== prior[index]) {
        throw new Error("KitJS: staged script nodes changed before publication");
      }
    }
    return scripts;
  }
`)
	_, _ = output.WriteString("  try {\n")
	_, _ = output.WriteString("    var deliveryScripts = validateScripts(null);\n")
	_, _ = output.WriteString(fmt.Sprintf("    if (deliveryScripts[%d] !== graphScript) {\n", graphIndex))
	_, _ = output.WriteString("      throw new Error(\"KitJS: staged graph script is out of order\");\n")
	_, _ = output.WriteString("    }\n")
	_, _ = output.WriteString("    var assets = specs.map(function (asset, index) {\n")
	_, _ = output.WriteString("      return Object.freeze({ role: asset.role, package: asset.package, version: asset.version,\n")
	_, _ = output.WriteString("        hash: asset.hash, integrity: asset.integrity, name: asset.name, url: assertScript(deliveryScripts[index], asset),\n")
	_, _ = output.WriteString("        packages: asset.packages || null, components: asset.components || null,\n")
	_, _ = output.WriteString("        sourceHash: asset.sourceHash || null });\n")
	_, _ = output.WriteString("    });\n")
	_, _ = output.WriteString("    Object.freeze(assets);\n")
	_, _ = output.WriteString("    var graphAsset = assets[")
	_, _ = output.WriteString(fmt.Sprintf("%d", graphIndex))
	_, _ = output.WriteString("];\n")
	_, _ = output.WriteString("    var delivery = Object.freeze({ profile: ")
	_, _ = output.Write(profileJSON)
	_, _ = output.WriteString(", graphKey: ")
	_, _ = output.Write(graphJSON)
	_, _ = output.WriteString(", runtimeHash: ")
	runtimeHash, _ := json.Marshal(runtime.sha256)
	_, _ = output.Write(runtimeHash)
	_, _ = output.WriteString(", hydrateHash: ")
	if hydrate == nil {
		_, _ = output.WriteString("null")
	} else {
		hydrateHash, _ := json.Marshal(hydrate.sha256)
		_, _ = output.Write(hydrateHash)
	}
	_, _ = output.WriteString(", graphHash: graphAsset.hash, graphIntegrity: graphAsset.integrity, graphName: graphAsset.name, graphURL: graphAsset.url, assets: assets });\n")
	_, _ = output.WriteString(`    var packageAssets = Object.create(null);
    var packageIndexes = Object.create(null);
    var loadedPackages = Object.create(null);
    function packageKey(role, name, version) { return role + "\u0000" + name + "\u0000" + version; }
    assets.forEach(function (asset, index) {
      if (asset.role !== "service" && asset.role !== "component" && asset.role !== "components") return;
      var key = packageKey(asset.role, asset.package, asset.version);
      if (packageAssets[key]) throw new Error("KitJS: duplicate package identity in staged delivery");
      packageAssets[key] = asset;
      packageIndexes[key] = index;
    });
    Object.defineProperty(core, "assertStagedPackage", { value: function (script, role, name, version) {
      var key = packageKey(role, name, version);
      var asset = packageAssets[key];
      if (!asset || loadedPackages[key] || deliveryScripts[packageIndexes[key]] !== script) {
        throw new Error("KitJS: staged package tag does not match the sealed delivery");
      }
      assertScript(script, asset);
      loadedPackages[key] = true;
      if (typeof core.queueStagedFinalize === "function") core.queueStagedFinalize();
    } });
    function packagesComplete() {
      return Object.keys(packageAssets).every(function (key) { return loadedPackages[key] === true; });
    }
    Object.defineProperty(core, "validateStagedDelivery", { value: function () {
      validateScripts(deliveryScripts);
      if (!packagesComplete()) throw new Error("KitJS: staged delivery did not execute every package");
      return true;
    } });
    Object.defineProperty(core, "stagedPackagesComplete", { value: packagesComplete });
`)
}

func writeStagedDeliverySpecs(
	output *bytes.Buffer,
	runtime JITArtifact,
	hydrate *JITArtifact,
	serviceArtifacts []JITArtifact,
	shared []stagedComponentArtifact,
	componentsBundle *JITArtifact,
	individual []stagedComponentArtifact,
) int {
	_, _ = output.WriteString("  var specs = [];\n")
	writeStagedDeliverySpec(output, runtime, nil)
	if hydrate != nil {
		writeStagedDeliverySpec(output, *hydrate, nil)
	}
	graphIndex := 1
	if hydrate != nil {
		graphIndex++
	}
	_, _ = output.WriteString("  specs.push({ role: \"graph\", package: \"\", version: \"\", hash: graphHash, integrity: graphIntegrity, name: graphName });\n")
	for _, artifact := range serviceArtifacts {
		writeStagedDeliverySpec(output, artifact, nil)
	}
	if componentsBundle != nil {
		components := make([]normalizedComponentPackage, len(shared))
		for index, entry := range shared {
			components[index] = entry.component
		}
		writeStagedDeliverySpec(output, *componentsBundle, components)
	}
	for _, entry := range individual {
		writeStagedDeliverySpec(output, entry.artifact, []normalizedComponentPackage{entry.component})
	}
	return graphIndex
}

func writeStagedDeliverySpec(output *bytes.Buffer, artifact JITArtifact, components []normalizedComponentPackage) {
	role, _ := json.Marshal(string(artifact.role))
	packageName, _ := json.Marshal(artifact.packageName)
	version, _ := json.Marshal(artifact.version)
	hash, _ := json.Marshal(artifact.sha256)
	integrity, _ := json.Marshal(artifact.integrity)
	name, _ := json.Marshal(artifact.name)
	_, _ = output.WriteString("  specs.push({ role: ")
	_, _ = output.Write(role)
	_, _ = output.WriteString(", package: ")
	_, _ = output.Write(packageName)
	_, _ = output.WriteString(", version: ")
	_, _ = output.Write(version)
	_, _ = output.WriteString(", hash: ")
	_, _ = output.Write(hash)
	_, _ = output.WriteString(", integrity: ")
	_, _ = output.Write(integrity)
	_, _ = output.WriteString(", name: ")
	_, _ = output.Write(name)
	if components != nil {
		packages := make([]string, len(components))
		_, _ = output.WriteString(", components: Object.freeze([")
		for index, component := range components {
			if index > 0 {
				_, _ = output.WriteString(", ")
			}
			name, _ := json.Marshal(component.identity.Name)
			version, _ := json.Marshal(component.identity.Version)
			sourceHash, _ := json.Marshal(component.sourceHash)
			_, _ = output.WriteString("Object.freeze({ name: ")
			_, _ = output.Write(name)
			_, _ = output.WriteString(", version: ")
			_, _ = output.Write(version)
			_, _ = output.WriteString(", sourceHash: ")
			_, _ = output.Write(sourceHash)
			_, _ = output.WriteString(" })")
			packages[index] = component.identity.Name + "@" + component.identity.Version
		}
		_, _ = output.WriteString("])")
		if artifact.role == JITRoleComponent && len(components) == 1 {
			sourceHash, _ := json.Marshal(components[0].sourceHash)
			_, _ = output.WriteString(", sourceHash: ")
			_, _ = output.Write(sourceHash)
		}
		packagesJSON, _ := json.Marshal(packages)
		_, _ = output.WriteString(", packages: Object.freeze(")
		_, _ = output.Write(packagesJSON)
		_, _ = output.WriteString(")")
	}
	_, _ = output.WriteString(" });\n")
}

func writeStagedGraphInstall(output *bytes.Buffer) {
	_, _ = output.WriteString(`    if (core.reuse) {
      var installed = global.kit && global.kit[GRAPH];
      if (!installed || installed.id !== graph.id || installed.profile !== graph.profile ||
        installed.artifact !== graph.artifact) {
        throw new Error("KitJS: installed component graph does not match this artifact");
      }
      core.graphValidated = true;
    } else {
      if (typeof core.installComponentGraph !== "function") {
        throw new Error("KitJS: component graph installer is unavailable");
      }
      core.installComponentGraph(graph);
      if (!core.kit || core.kit.version !== core.version || core.kit.component !== core.component) {
        throw new Error("KitJS: package facade is unavailable");
      }
    }
    if (!core.reuse) {
      if (typeof core.installStagedDelivery !== "function") {
        throw new Error("KitJS: staged delivery installer is unavailable");
      }
      delivery = core.installStagedDelivery(delivery);
    }
  } catch (error) {
    delete document[ASSEMBLY];
    throw error;
  }
`)
}

func writeStagedFinalizer(output *bytes.Buffer, bootSource []byte) {
	_, _ = output.WriteString(`  var finalized = false;
  var finalizeQueued = false;
  function finalize() {
    if (finalized) return;
    finalized = true;
    if (document[ASSEMBLY] !== core) return;
    try {
      if (core.packageError) throw core.packageError;
      core.validateStagedDelivery();
      if (!core.reuse && core.serviceRegistry && core.servicesSealed !== true) {
        if (typeof core.sealServices !== "function") {
          throw new Error("KitJS: service graph sealer is unavailable");
        }
        core.sealServices();
      }
    } catch (error) {
      delete document[ASSEMBLY];
      throw error;
    }
`)
	_, _ = output.Write(bootSource)
	_, _ = output.WriteString(`  }
  function maybeFinalize() {
    finalizeQueued = false;
    if (finalized || core.packageError || !core.stagedPackagesComplete()) return;
    finalize();
  }
  function queueFinalize() {
    if (finalized || finalizeQueued) return;
    finalizeQueued = true;
    if (typeof global.queueMicrotask === "function") global.queueMicrotask(maybeFinalize);
    else Promise.resolve().then(maybeFinalize);
  }
  Object.defineProperty(core, "queueStagedFinalize", { value: queueFinalize });
  if (document.readyState === "loading" || document.readyState === "interactive") {
    document.addEventListener("DOMContentLoaded", function () {
      if (finalized) return;
      try {
        if (core.packageError) throw core.packageError;
        core.validateStagedDelivery();
        throw new Error("KitJS: staged delivery did not finalize before DOMContentLoaded");
      } catch (error) {
        delete document[ASSEMBLY];
        throw error;
      }
    }, { once: true });
  } else {
    delete document[ASSEMBLY];
    throw new Error("KitJS: staged graph must be parser-inserted before DOMContentLoaded");
  }
  queueFinalize();
`)
}
