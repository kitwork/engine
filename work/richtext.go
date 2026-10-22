package work

// richtext(source) of "kitwork" (20/10/2026): rich text a person wrote or pasted (an editor's value, a
// paste from Google Docs or Word, the plain text of a form field) cleaned into the one closed grammar
// of utilities/richtext:
//
//	import { richtext } from "kitwork";
//	const note = richtext(request.form("note"));
//	note.value       // the cleaned HTML, "" when nothing is kept
//	note.text        // its words without markup
//	note.words       // a Vietnamese syllable is a word
//	note.characters  // code points of text, line breaks not counted
//	note.empty       // nothing kept
//
// It never fails: null, undefined or no source is the empty document; a number or a boolean is its
// text; any other value (an object, a list, a function) is the empty document.

import (
	"github.com/kitwork/engine/utilities/richtext"
	"github.com/kitwork/engine/value"
)

// Richtext is what `import { richtext } from "kitwork"` resolves to (0-arg getter, auto-called): the
// cleaning function.
func (w *KitWork) Richtext() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		return richtextObject(richtext.Clean(richtextSource(args)))
	})
}

// richtextSource is the text richtext(…) cleans: a string as it is, a number or a boolean as its
// text, "" for anything else.
func richtextSource(args []value.Value) string {
	if len(args) == 0 {
		return ""
	}
	switch source := args[0]; source.K {
	case value.String, value.Number, value.Bool:
		return source.Text()
	}
	return ""
}

// richtextObject is the document as the program reads it.
func richtextObject(document richtext.Document) value.Value {
	return value.New(map[string]value.Value{
		"value":      value.New(document.HTML),
		"text":       value.New(document.Text),
		"words":      value.New(document.Words),
		"characters": value.New(document.Characters),
		"empty":      value.New(document.Empty()),
	})
}
