package javascript

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kitwork/engine/jit/internal/htmlattr"
)

func TestBrowserMalformedUTF8AttributeParity(t *testing.T) {
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	sequences := [][]byte{
		{0xe2, 0x82},
		{0xf0, 0x9f, 0x98},
		{0xe0, 0x80, 0x80},
		{0xed, 0xa0, 0x80},
		{0xf0, 0x80, 0x80, 0x80},
		{0xf4, 0x90, 0x80, 0x80},
		{0x80, 0x80},
		{0xe2, 0x82, '\r', '\n'},
	}
	expected := make([]string, len(sequences))
	var fixture bytes.Buffer
	fixture.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>attribute UTF-8 parity</title></head><body>")
	for index, sequence := range sequences {
		raw := append([]byte{'a'}, sequence...)
		raw = append(raw, 'b')
		expected[index] = htmlattr.Decode(string(raw))
		fixture.WriteString(`<p id="case-`)
		fixture.WriteString(strconv.Itoa(index))
		fixture.WriteString(`" data-value="`)
		fixture.Write(raw)
		fixture.WriteString(`"></p>`)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	fixture.WriteString(`<script>(function(){try{var expected=`)
	fixture.Write(expectedJSON)
	fixture.WriteString(`;expected.forEach(function(want,index){var got=document.getElementById("case-"+index).getAttribute("data-value");if(got!==want)throw new Error("case "+index+" = "+JSON.stringify(got)+", want "+JSON.stringify(want));});document.documentElement.setAttribute("data-kit-test","passed");}catch(error){document.documentElement.setAttribute("data-kit-test","failed");document.documentElement.setAttribute("data-kit-test-error",String(error&&error.message||error));}})();</script></body></html>`)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(fixture.Bytes())
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL)
}

func TestBrowserScriptLikeCustomTagNamesMatchScanner(t *testing.T) {
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	wrappers := [][]byte{
		[]byte("script_"),
		[]byte("script."),
		append([]byte("script"), 0),
		[]byte("scripté"),
	}
	wantLocalNames := []string{"script_", "script.", "script\ufffd", "scripté"}
	wantJSON, err := json.Marshal(wantLocalNames)
	if err != nil {
		t.Fatal(err)
	}

	var fixture bytes.Buffer
	fixture.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>tag-name parity</title></head><body>`)
	for index, wrapper := range wrappers {
		fixture.WriteByte('<')
		fixture.Write(wrapper)
		fixture.WriteString(` id="wrapper-`)
		fixture.WriteString(strconv.Itoa(index))
		fixture.WriteString(`"><div data-kit-component="dialog@1.0.0"></div></`)
		fixture.Write(wrapper)
		fixture.WriteByte('>')
	}
	fixture.WriteString(`<script>globalThis.__scriptLikeFake = '<div data-kit-component="theme@2.0.0"></div>';</script>`)
	fixture.WriteString(`<DIV><span id="known-live" data-kit-component="toast@1.0.0"></span></DIV>`)
	fixture.WriteString(`<script>(function(){try{var names=`)
	fixture.Write(wantJSON)
	fixture.WriteString(`;names.forEach(function(name,index){var wrapper=document.getElementById("wrapper-"+index);if(!wrapper||wrapper.localName!==name)throw new Error("wrapper "+index+" localName="+JSON.stringify(wrapper&&wrapper.localName));if(!wrapper.querySelector('[data-kit-component="dialog@1.0.0"]'))throw new Error("wrapper "+index+" lost live component");});if(!document.getElementById("known-live"))throw new Error("ordinary known tags lost live component");if(document.querySelector('[data-kit-component="theme@2.0.0"]'))throw new Error("real script text became live markup");document.documentElement.setAttribute("data-kit-test","passed");}catch(error){document.documentElement.setAttribute("data-kit-test","failed");document.documentElement.setAttribute("data-kit-test-error",String(error&&error.message||error));}})();</script></body></html>`)

	result, err := ScanHTML(fixture.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != len(wrappers)+1 {
		t.Fatalf("scanner components = %#v, want %d live components", result.Components, len(wrappers)+1)
	}
	for index := range wrappers {
		if result.Components[index].Name != "dialog" || result.Components[index].Version != "1.0.0" {
			t.Fatalf("scanner component %d = %#v, want dialog@1.0.0", index, result.Components[index])
		}
	}
	last := result.Components[len(result.Components)-1]
	if last.Name != "toast" || last.Version != "1.0.0" {
		t.Fatalf("ordinary known-tag component = %#v, want toast@1.0.0", last)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(fixture.Bytes())
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL)
}
