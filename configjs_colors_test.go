package engine

import "testing"

func TestManifestDesktopColors(t *testing.T) {
	for _, fields := range []string{
		`background:"system",color:"system"`,
		`launch:{background:"system"}`,
		`background:"#123456",color:"#FAFAFA"`,
		`background:{light:"#ffffff",dark:"#09090b"},color:{light:"#171717",dark:"#fafafa"}`,
		`launch:{background:"#123456"}`,
		`launch:{background:{light:"#ffffff",dark:"#09090b"}}`,
	} {
		if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork"; app.desktop({`+fields+`});`)); err != nil {
			t.Fatal(fields, err)
		}
	}
	for _, option := range []string{`"default"`, `"System"`, `" system"`, `{light:"system",dark:"#000000"}`, `true`, `null`, `12`, `"red"`, `"#fff"`, `"#12345678"`, `"#12GG56"`, `" #123456"`, `{light:"#ffffff"}`, `{light:"#ffffff",dark:false}`, `{light:"#ffffff",dark:"#000000",other:"#123456"}`} {
		for _, field := range []string{"background", "color"} {
			if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork"; app.desktop({`+field+`:`+option+`});`)); err == nil {
				t.Fatal("accepted", field, option)
			}
		}
		if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork"; app.desktop({launch:{background:`+option+`}});`)); err == nil {
			t.Fatal("accepted launch background", option)
		}
	}
}
