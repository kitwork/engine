package engine

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestManifestSplashAPIs(t *testing.T) {
	for _, source := range []string{
		`app.desktop({name:"Kitwork",url:"https://kitwork.io",splash:{template:"./splash.kitwork.html",bind:{heading:"Hello",items:[1,2],nested:{ok:true}}}});`,
		`app.desktop({name:"Kitwork",url:"https://kitwork.io"}).splash("./splash.kitwork.html",{heading:"Hello",items:[1,2],nested:{ok:true}});`,
		`app.splash("./splash.kitwork.html",{heading:"Hello",items:[1,2],nested:{ok:true}}).desktop({name:"Kitwork",url:"https://kitwork.io"});`,
	} {
		raw, err := ReadManifest(writeServerJS(t, `import { app } from "kitwork";`+source))
		if err != nil {
			t.Fatal(err)
		}
		option := raw["splash"]
		if option == nil {
			option = raw["desktop"].(map[string]interface{})["splash"]
		}
		splash := option.(map[string]interface{})
		if splash["template"] != "./splash.kitwork.html" || splash["bind"].(map[string]interface{})["heading"] != "Hello" {
			t.Fatalf("bad normalization: %+v", splash)
		}
	}
}

func TestSplashBindLimits(t *testing.T) {
	deep := value.NULL
	for i := 0; i < 34; i++ {
		deep = value.New(map[string]value.Value{"child": deep})
	}
	items := make([]value.Value, 10001)
	for i := range items {
		items[i] = value.TRUE
	}
	for _, bind := range []value.Value{deep, value.New(map[string]value.Value{"items": value.New(items)})} {
		_, err := normalizeSplashOption(value.New(map[string]value.Value{"template": value.New("./splash.kitwork.html"), "bind": bind}))
		if err == nil {
			t.Fatal("oversized or deep bindings accepted")
		}
	}
}

func TestManifestSplashRejectsInvalidOptions(t *testing.T) {
	for _, source := range []string{
		`app.desktop({splash:false}).splash("./splash.kitwork.html",{});`,
		`app.splash("./splash.kitwork.html",{}).desktop({splash:false});`,
		`app.desktop({}).splash("./splash.html",{}).splash(false);`,
		`app.desktop({}).splash("../splash.html",{});`,
		`app.desktop({}).splash("./splash.html",[]);`,
		`app.desktop({splash:{template:"./splash.html",extra:true}});`,
		`app.desktop({splash:{template:"./splash.html",bind:{callback:()=>1}}});`,
		`app.desktop({}).splash();`,
	} {
		_, err := ReadManifest(writeServerJS(t, `import { app } from "kitwork";`+source))
		if err == nil || !strings.Contains(err.Error(), "splash") {
			t.Fatalf("expected splash error for %s: %v", source, err)
		}
	}
}
