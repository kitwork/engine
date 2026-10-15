package hydrate

import "strings"

// composedRuntime is every part of the client runtime in one string, in the order a page loads its
// files — bridge, kernel, platform modules, then Drive and the morph it drives, and boot last. The
// Node harness (runNodeDOMScript) has no network to load files from, so it runs this instead; a
// browser proof loads the real files (KernelFile, DriveFile).
func composedRuntime() string {
	return strings.Join([]string{bridgeJS, kernelJS, nativeJS, storageJS, webJS, morphJS, driveJS, bootJS}, "\n")
}

// pageRuntimeTags is how a page of a site with router.drive() loads the runtime: the kernel's file,
// then Drive's — both deferred, in that order (jit/js Deliver). A browser proof
// serves the two files at these names with pageRuntimeFile.
const pageRuntimeTags = `<script data-kitwork-generated="runtime" src="/kit/kernel.js" defer></script>` +
	`<script data-kitwork-generated="drive" src="/kit/drive.js" defer></script>`

func pageRuntimeFile(path string) string {
	if path == "/kit/drive.js" {
		return DriveFile()
	}
	return KernelFile(true)
}
