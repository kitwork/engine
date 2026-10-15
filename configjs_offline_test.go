package engine

import (
	"strings"
	"testing"
)

func TestManifestOfflineAPIs(t *testing.T) {
	for _, source := range []string{
		`app.desktop({name:"Kitwork",url:"https://kitwork.io",offline:{template:"./offline.kitwork.html",bind:{heading:"Offline"}}});`,
		`app.desktop({name:"Kitwork",url:"https://kitwork.io"}).offline("./offline.kitwork.html",{heading:"Offline"});`,
		`app.offline({template:"./offline.kitwork.html",bind:{heading:"Offline"}}).desktop({name:"Kitwork",url:"https://kitwork.io"});`,
	} {
		raw, err := ReadManifest(writeServerJS(t, `import { app } from "kitwork";`+source))
		if err != nil {
			t.Fatal(err)
		}
		option := raw["offline"]
		if option == nil {
			option = raw["desktop"].(map[string]interface{})["offline"]
		}
		if option.(map[string]interface{})["bind"].(map[string]interface{})["heading"] != "Offline" {
			t.Fatal(option)
		}
	}
}

func TestManifestOfflineInvalid(t *testing.T) {
	for _, source := range []string{
		`app.offline();`, `app.offline(true,true);`, `app.offline(true).offline(false);`,
		`app.offline(false).desktop({offline:true});`, `app.desktop({offline:true}).offline(false);`,
		`app.offline("../secret.html");`, `app.offline({template:"offline.html",bind:[]});`,
		`app.offline({template:"offline.html",bind:{fn:()=>1}});`, `app.offline({template:"offline.html",extra:true});`,
	} {
		_, err := ReadManifest(writeServerJS(t, `import { app } from "kitwork";`+source))
		if err == nil || !strings.Contains(err.Error(), "offline") {
			t.Fatalf("%s: %v", source, err)
		}
	}
}
