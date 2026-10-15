package engine

import "testing"

func TestManifestLaunch(t *testing.T) {
	for _, source := range []string{
		`app.desktop({url:"https://kitwork.io"}).launch({width:420,height:240,title:"Kitwork"});`,
		`app.launch({width:420,height:240}).desktop({url:"https://kitwork.io"});`,
		`app.desktop({url:"https://kitwork.io",launch:{width:420,height:240}});`,
		`app.launch(false).desktop({url:"https://kitwork.io"});`,
		`app.desktop({url:"https://kitwork.io",bind:{logo:"logo.svg"},launch:true,splash:{},offline:{}});`,
		`app.desktop({launch:{template:"launch.kitwork.html",width:420}});`,
		`app.desktop({launch:{rounded:0}});`,
		`app.desktop({launch:{rounded:24}});`,
		`app.desktop({launch:{glass:true}});`,
		`app.desktop({launch:{glass:false}});`,
		`app.launch({rounded:128});`,
	} {
		if _, err := ReadManifest(writeServerJS(t, `import { app } from "kitwork";`+source)); err != nil {
			t.Fatal(source, err)
		}
	}
	for _, source := range []string{
		`app.launch("launch.html");`,
		`app.desktop({launch:{rounded:-1}});`,
		`app.desktop({launch:{glass:"true"}});`,
		`app.desktop({launch:{glass:1}});`,
		`app.desktop({launch:{rounded:129}});`,
		`app.desktop({launch:{rounded:1.5}});`,
		`app.desktop({launch:{rounded:true}});`,
		`app.desktop({launch:{rounded:"12"}});`,
		`app.launch({width:0,height:240});`, `app.launch({width:420,height:1201});`,
		`app.launch({width:420.5,height:240});`, `app.launch({width:420,height:240,html:"x"});`,
		`app.launch({width:420,height:240,title:""});`, `app.launch(false).launch(false);`,
		`app.launch(false).desktop({launch:false});`, `app.desktop({launch:false}).launch(false);`,
	} {
		if _, err := ReadManifest(writeServerJS(t, `import { app } from "kitwork";`+source)); err == nil {
			t.Fatal("accepted", source)
		}
	}
}

func TestManifestDesktopBindInvalid(t *testing.T) {
	for _, source := range []string{
		`app.desktop({bind:[]});`, `app.desktop({bind:{fn:()=>1}});`,
		`app.desktop({launch:{template:"../launch.html"}});`,
	} {
		if _, err := ReadManifest(writeServerJS(t, `import { app } from "kitwork";`+source)); err == nil {
			t.Fatal("accepted", source)
		}
	}
}
