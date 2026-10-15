package highlight

import "strconv"

// Kind says what a piece of source IS, never what colour it gets. The set is
// closed on purpose: a renderer maps each kind to one class (or one native text
// attribute) and a contrast check runs over that table, so a kind nobody planned
// for would be text nobody checked. Adding one is a decision, not a convenience.
type Kind uint8

// The order is part of nothing: compare kinds by name, never by number.
const (
	Plain             Kind = iota // whitespace, identifiers nobody named, text between tags
	Keyword                       // if, const, SELECT, function, fi
	String                        // quoted text, template text, heredoc bodies, YAML block scalars
	Number                        // 42, 0x1F, 1_000, 2.5e-3, 10px
	Comment                       // // …, /* … */, # …, <!-- … -->, -- …
	Function                      // a name being called or defined; a shell command
	Type                          // int, string, interface names, YAML tags
	Constant                      // true, false, null, nil, None, undefined, entities
	Property                      // object keys, JSON keys, YAML keys, CSS properties
	Variable                      // shell $VAR, PowerShell $var, template names
	Operator                      // = + - * / && || => ?. ??
	Punctuation                   // ( ) [ ] { } , ; . : and the brackets of a tag
	Tag                           // an element name in markup
	Attribute                     // an attribute name in markup; a command-line flag
	RegularExpression             // a regular-expression literal
	Inserted                      // a diff line that was added
	Deleted                       // a diff line that was removed
	Heading                       // diff hunk headers @@ … @@ and file headers
	Prompt                        // reserved for the terminal component; no tokenizer produces it

	kindCount // not a kind: the size of the set, so every table over kinds is checked complete
)

// kindNames are the names a renderer builds class names from, so they are
// lower-case full words joined by a hyphen, and they never change once shipped.
var kindNames = [kindCount]string{
	Plain:             "plain",
	Keyword:           "keyword",
	String:            "string",
	Number:            "number",
	Comment:           "comment",
	Function:          "function",
	Type:              "type",
	Constant:          "constant",
	Property:          "property",
	Variable:          "variable",
	Operator:          "operator",
	Punctuation:       "punctuation",
	Tag:               "tag",
	Attribute:         "attribute",
	RegularExpression: "regular-expression",
	Inserted:          "inserted",
	Deleted:           "deleted",
	Heading:           "heading",
	Prompt:            "prompt",
}

// String is the kind's name: "keyword", "regular-expression". A value outside
// the set prints as Kind(n) rather than borrowing a real kind's name.
func (kind Kind) String() string {
	if kind < kindCount {
		return kindNames[kind]
	}
	return "Kind(" + strconv.Itoa(int(kind)) + ")"
}

// Kinds lists every kind in declaration order, for a renderer that builds one
// rule per kind and a check that every kind got one.
func Kinds() []Kind {
	kinds := make([]Kind, kindCount)
	for index := range kinds {
		kinds[index] = Kind(index)
	}
	return kinds
}
