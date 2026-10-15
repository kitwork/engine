package engine

import (
	"testing"
)

func TestManifestSplashDuration(t *testing.T) {
	for _, source := range []string{
		`app.desktop({splash:1.5});`,
		`app.desktop({splash:{duration:1.5}});`,
		`app.desktop({}).splash(1.5);`,
	} {
		raw, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork";`+source))
		if err != nil {
			t.Fatal(err)
		}
		option := raw["splash"]
		if option == nil {
			option = raw["desktop"].(map[string]interface{})["splash"]
		}
		if option.(map[string]interface{})["duration"] != float64(1.5) {
			t.Fatal(option)
		}
	}
	for _, source := range []string{
		`app.desktop({splash:-1});`,
		`app.desktop({splash:61});`,
		`app.desktop({splash:{duration:"2s"}});`,
		`app.desktop({offline:{duration:2}});`,
	} {
		if _, err := ReadManifest(writeServerJS(t, `import {app} from "kitwork";`+source)); err == nil {
			t.Fatal("accepted", source)
		}
	}
}
