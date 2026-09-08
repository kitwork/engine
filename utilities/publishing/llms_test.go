package publishing

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func llmsInput(fields map[string]value.Value) value.Value { return value.New(fields) }

func TestLlmsRendersTitleSummaryAndSections(t *testing.T) {
	document := Llms(llmsInput(map[string]value.Value{
		"title":   value.New("Kitwork"),
		"summary": value.New("One runtime for your whole stack."),
		"sections": value.New([]value.Value{
			value.New(map[string]value.Value{
				"title": value.New("Docs"),
				"links": value.New([]value.Value{
					value.New(map[string]value.Value{
						"title":       value.New("Routing"),
						"url":         value.New("/docs/routing"),
						"description": value.New("A folder is a site."),
					}),
				}),
			}),
		}),
	}), "https://kitwork.io")

	for _, want := range []string{
		"# Kitwork",
		"> One runtime for your whole stack.",
		"## Docs",
		"- [Routing](https://kitwork.io/docs/routing): A folder is a site.",
	} {
		if !strings.Contains(document, want) {
			t.Fatalf("missing %q\ngot:\n%s", want, document)
		}
	}
}

// The readers of this file are usually not the browser that fetched it, so a
// site-relative link is useless to them.
func TestLlmsMakesRelativeLinksAbsolute(t *testing.T) {
	document := Llms(llmsInput(map[string]value.Value{
		"title": value.New("Kitwork"),
		"sections": value.New([]value.Value{value.New(map[string]value.Value{
			"title": value.New("Docs"),
			"links": value.New([]value.Value{value.New(map[string]value.Value{
				"title": value.New("Home"), "url": value.New("/"),
			})}),
		})}),
	}), "https://kitwork.io")

	if !strings.Contains(document, "(https://kitwork.io/)") {
		t.Fatalf("a relative link was left relative.\ngot:\n%s", document)
	}
}

func TestLlmsLeavesAbsoluteLinksAlone(t *testing.T) {
	document := Llms(llmsInput(map[string]value.Value{
		"title": value.New("Kitwork"),
		"sections": value.New([]value.Value{value.New(map[string]value.Value{
			"title": value.New("Project"),
			"links": value.New([]value.Value{value.New(map[string]value.Value{
				"title": value.New("GitHub"), "url": value.New("https://github.com/kitwork/engine"),
			})}),
		})}),
	}), "https://kitwork.io")

	if strings.Contains(document, "kitwork.io/https") {
		t.Fatalf("an absolute link was rewritten against the base.\ngot:\n%s", document)
	}
}

// A blockquote that wraps onto a second line stops being a blockquote.
func TestLlmsKeepsTheSummaryOnOneLine(t *testing.T) {
	document := Llms(llmsInput(map[string]value.Value{
		"title":   value.New("Kitwork"),
		"summary": value.New("one\ntwo\n\nthree"),
	}), "https://kitwork.io")

	if !strings.Contains(document, "> one two three\n") {
		t.Fatalf("the summary was not collapsed onto one blockquote line.\ngot:\n%q", document)
	}
}

// An entry with no title or no url would render as broken Markdown.
func TestLlmsSkipsIncompleteEntries(t *testing.T) {
	document := Llms(llmsInput(map[string]value.Value{
		"title": value.New("Kitwork"),
		"sections": value.New([]value.Value{value.New(map[string]value.Value{
			"title": value.New("Docs"),
			"links": value.New([]value.Value{
				value.New(map[string]value.Value{"title": value.New("No url")}),
				value.New(map[string]value.Value{"url": value.New("/no-title")}),
				value.New(map[string]value.Value{"title": value.New("Good"), "url": value.New("/good")}),
			}),
		})}),
	}), "https://kitwork.io")

	if strings.Contains(document, "No url") || strings.Contains(document, "/no-title") {
		t.Fatalf("an incomplete entry was rendered.\ngot:\n%s", document)
	}
	if !strings.Contains(document, "[Good]") {
		t.Fatalf("the complete entry was dropped along with the incomplete ones.\ngot:\n%s", document)
	}
}

// No configuration at all must still be a valid document, not an empty file.
func TestLlmsWithNoDataStillRendersATitle(t *testing.T) {
	if document := Llms(value.New(map[string]value.Value{}), "https://kitwork.io"); !strings.HasPrefix(document, "# ") {
		t.Fatalf("an empty declaration produced no heading.\ngot: %q", document)
	}
}
