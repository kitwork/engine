package engine

import "testing"

func TestManifestDesktopMaterial(t *testing.T) {
	for _, mode := range []string{"none", "glass", "mica"} {
		if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork"; app.desktop({material:"`+mode+`"});`)); err != nil {
			t.Fatal(mode, err)
		}
	}
	for _, option := range []string{`true`, `1`, `null`, `"acrylic"`, `"Mica"`, `{}`} {
		if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork"; app.desktop({material:`+option+`});`)); err == nil {
			t.Fatal("accepted invalid material", option)
		}
	}
}
