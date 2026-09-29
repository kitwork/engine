package hydrate

import (
	"strings"
	"testing"
)

// ++ and -- came into the grammar on 29/09, ported from the standalone KitJS 1.0.0, which had
// always had them. kitjs.org's home page used data-kit-click="count++" for its demo counter after
// the site moved to the kernel, and the kernel refused to parse it: the two buttons did nothing
// and nothing said so. The verify pass found it.
//
// What each ACCEPTS is pinned by the shared conformance corpus (both engines, one file). This file
// pins what each REFUSES, because a parser that accepted too much would pass the corpus happily.

var updateRefusals = []struct{ src, why string }{
	{"obj.count++", "a member of an object — general member assignment is not in the grammar"},
	{"count++++", "an update of an update — JavaScript refuses it too"},
	{"5++", "a literal is not a place to store anything"},
	{"$++", "the page scope itself is not a counter"},
	{"++obj.count", "the prefix form takes the same targets as the postfix one"},
}

func TestServerRefusesWhatCannotBeUpdated(t *testing.T) {
	for _, c := range updateRefusals {
		if _, err := Compile(c.src); err == nil {
			t.Errorf("Compile(%q) was accepted, but it is %s", c.src, c.why)
		}
	}
	// The message has to point at the fix, not just at a token.
	_, err := Compile("obj.count++")
	if err == nil || !strings.Contains(err.Error(), "count++") {
		t.Errorf("the refusal should show a spelling that works, got: %v", err)
	}
}

func TestKernelRefusesWhatCannotBeUpdated(t *testing.T) {
	sources := make([]string, len(updateRefusals))
	for i, c := range updateRefusals {
		sources[i] = `"` + c.src + `"`
	}
	runNodeDOMScript(t, "update_refusals.test.js", `
var kit = window.kit;
var accepted = [];
[`+strings.Join(sources, ", ")+`].forEach(function (src) {
  try { kit.compile(src); accepted.push(src); } catch (e) {}
});
if (accepted.length) throw new Error("the kernel accepted what the server refuses: " + accepted.join(", "));

// CONTROL: the parser is not simply refusing every ++, or the loop above would pass for nothing.
kit.compile("count++"); kit.compile("--count"); kit.compile("$.count++");
console.log("the kernel refuses the same updates the server does");
`)
}
