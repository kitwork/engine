//go:build !stdminify

package minifier

import (
	"strings"
	"testing"
)

// Specific to the DEFAULT tdewolff build: prove the regexp registration reaches the content types
// exact-match would miss — JSON-LD structured data, module/plain inline JS, and standalone JSON/JS.

func TestJSONLDInHTML(t *testing.T) {
	in := `<html><head><script type="application/ld+json">  { "@type" : "Article" , "x" : 1 }  </script></head><body>hi</body></html>`
	out := HTML(in)
	if !strings.Contains(out, `{"@type":"Article","x":1}`) {
		t.Errorf("JSON-LD (application/ld+json) was not minified: %q", out)
	}
}

func TestInlineJSInHTML(t *testing.T) {
	in := `<html><body><script>  var   x   =   1  ;   var  y  =  x  +  2 ;  </script></body></html>`
	out := HTML(in)
	if strings.Contains(out, "  ") {
		t.Errorf("inline JS whitespace not collapsed: %q", out)
	}
	if !strings.Contains(out, "x=1") {
		t.Errorf("inline JS not minified (expected `x=1`): %q", out)
	}
}

func TestJSONStandalone(t *testing.T) {
	out := JSON(`{ "a" : [ 1 , 2 ] , "b" : true }`)
	if out != `{"a":[1,2],"b":true}` {
		t.Errorf("JSON not minified: %q", out)
	}
}

func TestJSStandalone(t *testing.T) {
	out := JS(`const  f  =  ( a , b )  =>  a  +  b ;`)
	if strings.Contains(out, "  ") || !strings.Contains(out, "=>") {
		t.Errorf("JS not minified: %q", out)
	}
}
