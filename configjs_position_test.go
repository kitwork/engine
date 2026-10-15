package engine

import "testing"

func TestManifestDesktopPosition(t *testing.T) {
	for _, option := range []string{`"center"`, `"top-left"`, `"top-right"`, `"bottom-left"`, `"bottom-right"`, `{x:24,y:32}`} {
		for _, fields := range []string{`position:` + option, `launch:{position:` + option + `}`} {
			if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork"; app.desktop({`+fields+`});`)); err != nil {
				t.Fatal(fields, err)
			}
		}
	}
	for _, option := range []string{`"left"`, `"Top-left"`, `true`, `null`, `12`, `{x:1}`, `{x:1,y:-1}`, `{x:1.5,y:2}`, `{x:100001,y:1}`, `{x:1,y:2,z:3}`} {
		for _, fields := range []string{`position:` + option, `launch:{position:` + option + `}`} {
			if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork"; app.desktop({`+fields+`});`)); err == nil {
				t.Fatal("accepted", fields)
			}
		}
	}
	if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork";app.desktop({position:"top-left",center:true});`)); err == nil {
		t.Fatal("ambiguous placement accepted")
	}
}
