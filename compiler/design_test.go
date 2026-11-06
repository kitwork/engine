package compiler

import (
	"strings"
	"testing"
)

// The rules of design.work (KITWORK_DESIGN.md mục 1, 28/10/2026), each a compile error at file:line:column. The
// CONTROL of each test is in its comment: the code under test disabled through go test -overlay (a copy of the
// file, the tree untouched), the test red for the reason the comment quotes, then green again with the tree as it
// is.

const designImport = "import * as design from \"kitwork/design\";\n"

// Exactly one Kitwork import line, import * as design from "kitwork/design", named design, once (rule 2): any
// other way to kitwork/design is refused at its line, and so is any other module of Kitwork, the router among
// them, and kitwork() written out: the file's vocabulary is closed. A value imported from a file of the app is
// allowed, as config.work imports a guard from .shared.
//
// CONTROL (06/11/2026, -overlay with readDesignFile not calling reader.imports nor reader.kitworkWrittenOut, and
// readLine not refusing design with no import): every row went red, compiled or refused elsewhere: "a named
// import: got <nil>"; "design of the bare module: got <nil>"; "no import: got <nil>"; "a second line: got
// design.work:2:13: design đã được khai báo trong cùng khối …"; "another name", "a default import", "the router",
// "another module", "the bare module", "kitwork() written out": got design.work:…: design.colors(…) chỉ viết ở cấp
// file … (no line read as the design's, so every call was refused as one not at the top level).
func TestDesignOneImportLine(t *testing.T) {
	const colors = "design.colors({ brand: \"#111111\" });\n"
	expectRouterErrors(t, "shop.com/design.work", []routerRow{
		{"a named import", "import { colors } from \"kitwork/design\";\ncolors({ brand: \"#111111\" });\n", "import", 0,
			DesignImportForm(`import { colors } from "kitwork/design"`)},
		{"another name", "import * as d from \"kitwork/design\";\nd.colors({ brand: \"#111111\" });\n", "import", 0,
			DesignImportForm(`import * as d from "kitwork/design"`)},
		{"a default import", "import design from \"kitwork/design\";\n" + colors, "import", 0,
			DesignImportForm(`import design from "kitwork/design"`)},
		{"a second line", designImport + "import * as design from \"kitwork/design\"; // again\n" + colors,
			"import * as design from \"kitwork/design\"; //", 0, DesignImportTwice(1, 2)},
		{"the router", designImport + routerImport + colors, "import * as router", 0,
			DesignImportOther(`import * as router from "kitwork/router"`)},
		{"another module", designImport + "import * as file from \"kitwork/file\";\n" + colors, "import * as file", 0,
			DesignImportOther(`import * as file from "kitwork/file"`)},
		{"the bare module", designImport + "import { safe } from \"kitwork\";\n" + colors, "import { safe", 0,
			DesignImportOther(`import { safe } from "kitwork"`)},
		{"design of the bare module", "import { design } from \"kitwork\";\n" + colors, "import", 0,
			DesignImportOther(`import { design } from "kitwork"`)},
		{"kitwork() written out", designImport + "const brand = kitwork().environment.BRAND;\n" + colors, "kitwork()", 7,
			DesignImportOther("kitwork()")},
		{"no import", colors, "design", 0, DesignWithoutImport("colors")},
	})
	expectRouterCompiles(t, "shop.com/design.work", []string{
		designImport + colors,
		designImport + "import { brand } from \"app/.shared/brand.work\";\ndesign.colors({ brand });\n",
		designImport + "const ink = { light: \"#18181b\", dark: \"#fafafa\" };\ndesign.colors({ ink });\n",
	}, map[string]string{".shared/brand.work": "export const brand = \"#f82244\";\n"})
	// the app's folder and a branch read alike
	expectRouterCompiles(t, "design.work", []string{designImport + colors})
	expectRouterCompiles(t, "shop.com/quan-ly/design.work", []string{designImport + colors})
}

// The seven words and no other (rule 3): a word not of the seven is an error naming the nearest
// (design.colours, design.palette: design.colors), a word of the router (guard, meta, limit, language…) an error
// saying it belongs in router.work or config.work. All seven compile, the accumulating ones as often as written.
//
// CONTROL (06/11/2026, -overlay with designReader.call taking any word as one of the seven and designCall
// letting every word through): every row went red: each line compiled ("design.colours: got <nil>",
// "design.palette: got <nil>", "design.guard: got <nil>", "design.get: got <nil>"…), and design.colours in a
// function was refused only as a call not at the top level.
func TestDesignClosedVocabulary(t *testing.T) {
	if got, want := DesignUnknownWord("colours"), "design.colours không có; ý là design.colors?"; got != want {
		t.Errorf("DesignUnknownWord(colours) = %q, want %q", got, want)
	}
	if got := DesignRouterWord("guard"); !strings.Contains(got, "router.work hay config.work") {
		t.Errorf("DesignRouterWord(guard) = %q, want it to say router.work hay config.work", got)
	}
	rows := []routerRow{
		{"design.colours", designImport + "design.colours({ brand: \"#111111\" });\n", "colours", 0, DesignUnknownWord("colours")},
		{"design.palette", designImport + "design.palette({ brand: \"#111111\" });\n", "palette", 0, "design.palette không có; ý là design.colors?"},
		{"design.font", designImport + "design.font({ body: \"Outfit\" });\n", "font", 0, "design.font không có; ý là design.fonts?"},
		{"design.whatever", designImport + "design.whatever(1);\n", "whatever", 0, DesignUnknownWord("whatever")},
		{"design.colours in a function", designImport + "const paint = () => design.colours({});\n", "colours", 0, DesignUnknownWord("colours")},
	}
	for _, word := range []string{"guard", "meta", "limit", "language", "favicon", "bundle", "reload", "jsonld", "get"} {
		rows = append(rows, routerRow{"design." + word, designImport + "design." + word + "(x);\n", word + "(", 0, DesignRouterWord(word)})
	}
	expectRouterErrors(t, "shop.com/design.work", rows)
	expectRouterCompiles(t, "shop.com/design.work", []string{
		designImport + strings.Join([]string{
			`design.colors({ brand: "#f82244", ink: { light: "#18181b", dark: "#fafafa" } });`,
			`design.gradients({ hero: { type: "linear", direction: "to-right", stops: [ { color: "brand", at: 0 }, { color: "paper", at: 100 } ] } });`,
			`design.fonts({ body: "Outfit", code: "Fira Code" });`,
			`design.highlight({ keyword: { light: "#7847bd", dark: "#bb9af7" } });`,
			`design.appearance("system");`,
			`design.contrast("warning");`,
			`design.transition("fade");`,
			`design.colors({ panel: "#eeeeee" });`,
			`design.fonts({ code: "Google Sans Code" });`,
		}, "\n") + "\n",
	})
	if words := DesignWords(); strings.Join(words, ",") != "colors,gradients,fonts,highlight,appearance,contrast,transition" {
		t.Errorf("DesignWords() = %v, want the seven of the spec", words)
	}
	for _, word := range DesignWords() {
		known, ok := DesignWordOf(word)
		router, _ := RouterWordOf(word)
		if !ok || known != router {
			t.Errorf("DesignWordOf(%s) = %+v, %v; want router.%s's row %+v", word, known, ok, word, router)
		}
	}
}

// One call per line at the file's top level, never chained, a word whose nearest folder wins once per file
// (rule 4): design.colors(…).fonts(…) is an error saying to split it; design.transition twice names both lines;
// the design in an if, a loop, a function's body, as part of another expression or read as a value is refused
// where it is read.
//
// CONTROL (06/11/2026, -overlay with designReader.call not refusing what is chained nor a second
// nearest-wins call, designCall taking every call as a head, designMember and designRead refusing nothing):
// every row compiled: "chained: got <nil>"; "called again: got <nil>"; "transition twice: got <nil>"; "in an
// if: got <nil>"; "in a function: got <nil>"; "const d = design: got <nil>"; "design.fonts read: got <nil>";
// "design[\"colors\"]: got <nil>"….
func TestDesignOneCallPerLine(t *testing.T) {
	expectRouterErrors(t, "shop.com/design.work", []routerRow{
		{"chained", designImport + "design.colors({ brand: \"#111111\" }).fonts({ body: \"Outfit\" });\n", "fonts", 0,
			DesignCallChained("colors", ".fonts(…)")},
		{"called again", designImport + "design.colors({ brand: \"#111111\" })();\n", "();", 0,
			DesignCallChained("colors", "(…)")},
		{"transition twice", designImport + "design.transition(\"fade\");\ndesign.transition(\"none\");\n", `transition("none")`, 0,
			DesignCallTwice("transition", "dòng 2", "dòng 3")},
		{"appearance twice", designImport + "design.appearance(\"dark\");\ndesign.colors({ brand: \"#111111\" });\ndesign.appearance(\"light\");\n", `appearance("light")`, 0,
			DesignCallTwice("appearance", "dòng 2", "dòng 4")},
		{"contrast twice", designImport + "design.contrast(\"strict\");\ndesign.contrast(\"warning\");\n", `contrast("warning")`, 0,
			DesignCallTwice("contrast", "dòng 2", "dòng 3")},
		{"in an if", designImport + "if (true) {\n  design.colors({ brand: \"#111111\" });\n}\n", "colors", 0, DesignNotAtTop("colors")},
		{"in a loop", designImport + "for (const brand of [\"#111111\"]) {\n  design.colors({ brand });\n}\n", "colors", 0, DesignNotAtTop("colors")},
		{"in a function", designImport + "const paint = () => design.fonts({ body: \"Outfit\" });\npaint();\n", "fonts", 0, DesignNotAtTop("fonts")},
		{"part of an expression", designImport + "const done = design.colors({ brand: \"#111111\" });\n", "colors", 0, DesignNotAtTop("colors")},
		{"const d = design", designImport + "const d = design;\n", "design;", 0, DesignAsValue("design")},
		{"design.fonts read", designImport + "const f = design.fonts;\n", "fonts;", 0, DesignAsValue("design.fonts")},
		{"design[\"colors\"]", designImport + "design[\"colors\"]({ brand: \"#111111\" });\n", "design[", 0, DesignAsValue("design")},
	})
	// the accumulating words as often as written; a parameter named design is the function's own
	expectRouterCompiles(t, "shop.com/design.work", []string{
		designImport + "design.colors({ brand: \"#111111\" });\ndesign.colors({ ink: \"#222222\" });\ndesign.highlight({ keyword: \"brand\" });\ndesign.highlight({ string: \"success\" });\n",
		designImport + "const pick = (design) => design.brand;\ndesign.colors({ brand: pick({ brand: \"#111111\" }) });\n",
	})
}

// kitwork/design is a design.work's alone: a router.work, a config.work (of a site, of .shared/queue/), a library
// alone or bundled into a router.work refuse it, in any form, at its line. The design is the folder's look in a
// file of its own (KITWORK_DESIGN.md, "Đã bác": design.colors in router.work with a second import line).
//
// CONTROL (06/11/2026, -overlay with refuseDesignImport refusing nothing): every row compiled ("router.work: got
// <nil>", "a site's config.work: got <nil>", "the queue's config.work: got <nil>", "a library bundled into a
// router.work: <nil>"…).
func TestDesignImportOutsideDesignWork(t *testing.T) {
	const handler = "router.get((context) => context.text(\"x\"));\n"
	expectRouterErrors(t, "shop.com/router.work", []routerRow{
		{"router.work", routerImport + designImport + handler, "import * as design", 0, DesignImportOutside},
	})
	expectRouterErrors(t, "shop.com/config.work", []routerRow{
		{"a site's config.work", routerImport + designImport + "router.language(\"vi\");\n", "import * as design", 0, DesignImportOutside},
	})
	expectRouterErrors(t, "shop.com/quan-ly/config.work", []routerRow{
		{"a branch's config.work", routerImport + "import { design } from \"kitwork\";\nrouter.language(\"vi\");\n", "import { design", 0, DesignImportOutside},
	})
	expectRouterErrors(t, ".shared/queue/config.work", []routerRow{
		{"the queue's config.work", "import * as queue from \"kitwork/queue\";\n" + designImport + "queue.parallel(4);\n", "import * as design", 0, DesignImportOutside},
	})
	library := designImport + "export const helper = () => 1;\n"
	expectRouterErrors(t, ".shared/lib.work", []routerRow{
		{"a library alone", library, "import", 0, DesignImportOutside},
	})
	_, err := compileRouterApp(t, map[string]string{
		".shared/lib.work":     library,
		"shop.com/router.work": routerImport + "import { helper } from \"app/.shared/lib.work\";\nrouter.get((context) => context.json({ n: helper() }));\n",
	}, "shop.com/router.work")
	if want := "../.shared/lib.work:1:1: " + DesignImportOutside; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a library bundled into a router.work: %v, want %s", err, want)
	}
	// router.colors(…) and its neighbours keep working in router.work and config.work
	expectRouterCompiles(t, "shop.com/router.work", []string{routerImport + "router.colors({ brand: \"#111111\" });\nrouter.transition(\"fade\");\n" + handler})
	expectRouterCompiles(t, "shop.com/config.work", []string{routerImport + "router.fonts({ body: \"Outfit\" });\nrouter.highlight({ keyword: \"brand\" });\n"})
}
