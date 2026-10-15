package hydrate

import (
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// grouped_corpus.json is the contract the Go reader (pairs.go) and the kernel's reader both run: the
// same value must split into the same pairs, or be refused, on both sides. This is the Go half.

//go:embed grouped_corpus.json
var groupedCorpus []byte

type groupedCase struct {
	Kind  string          `json:"kind"`
	Name  string          `json:"name"`
	Src   string          `json:"src"`
	Want  json.RawMessage `json:"want"`
	Error bool            `json:"error"`
}

func loadGroupedCases(t *testing.T) []groupedCase {
	t.Helper()
	var cases []groupedCase
	if err := json.Unmarshal(groupedCorpus, &cases); err != nil {
		t.Fatalf("grouped corpus is not valid JSON: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("grouped corpus is empty")
	}
	return cases
}

func TestGroupedCorpusServer(t *testing.T) {
	kinds := map[string]int{}
	for _, c := range loadGroupedCases(t) {
		kinds[c.Kind]++
		got, err := runGroupedCase(c)
		if c.Error {
			if err == nil {
				t.Errorf("%s/%s: %q must be refused, got %v", c.Kind, c.Name, c.Src, got)
				continue
			}
			// A refusal says where: the author is pointed at a place in the attribute.
			var grouped *GroupedError
			if !errors.As(err, &grouped) || grouped.Column < 1 || grouped.Column > len([]rune(c.Src))+1 {
				t.Errorf("%s/%s: the refusal must carry a position inside the value, got %#v", c.Kind, c.Name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s/%s: %q: %v", c.Kind, c.Name, c.Src, err)
			continue
		}
		var want any
		if err := json.Unmarshal(c.Want, &want); err != nil {
			t.Fatalf("%s/%s: want: %v", c.Kind, c.Name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s/%s: %q\n  got  %#v\n  want %#v", c.Kind, c.Name, c.Src, got, want)
		}
	}
	// every kind the contract names is run, so a case of a new kind cannot pass by being skipped
	for _, kind := range []string{"pairs", "component", "for"} {
		if kinds[kind] == 0 {
			t.Errorf("the corpus has no %s case", kind)
		}
	}
	if len(kinds) != 3 {
		t.Errorf("the corpus has a kind this runner does not know: %v", kinds)
	}
}

// runGroupedCase answers a case in the corpus's own JSON shape, so the comparison is the same one the
// kernel's runner makes.
func runGroupedCase(c groupedCase) (any, error) {
	switch c.Kind {
	case "pairs":
		pairs, err := Pairs(c.Src)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, p := range pairs {
			out = append(out, []any{p.Name, p.Value})
		}
		return out, nil
	case "component":
		name, alias, err := ComponentValue(c.Src)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": name, "alias": alias}, nil
	case "for":
		item, index, list, key, err := ForValue(c.Src)
		if err != nil {
			return nil, err
		}
		return map[string]any{"item": item, "index": index, "list": list, "key": key}, nil
	}
	return nil, errors.New("unknown kind " + c.Kind)
}

// The position a refusal names is the place to look, not the start of the value: the unclosed
// parenthesis, the name with a space, the pair with no value.
func TestGroupedErrorsPointAtTheMistake(t *testing.T) {
	cases := []struct {
		src    string
		column int
		reason string
	}{
		{"text: f(a; show: b", 8, `"(" is never closed`},
		{"text: a); show: b", 8, `")" closes nothing`},
		{"text: 'abc", 7, "never closed"},
		{"show: y; te xt: a", 10, `"te xt" is not a name`},
		{"show: y; text:", 15, "text has no value"},
		{"show: y; text", 10, "has no ':'"},
		{"title: 'Đóng'; text: (]", 23, `"]" closes the "("`},
	}
	for _, c := range cases {
		_, err := Pairs(c.src)
		var grouped *GroupedError
		if !errors.As(err, &grouped) {
			t.Errorf("%q: want a GroupedError, got %v", c.src, err)
			continue
		}
		if grouped.Column != c.column || !strings.Contains(grouped.Reason, c.reason) {
			t.Errorf("%q: got %q at %d, want %q at %d", c.src, grouped.Reason, grouped.Column, c.reason, c.column)
		}
	}
	// control: the same values written correctly split
	for _, src := range []string{"text: f(a); show: b", "text: a; show: b", "text: 'abc'", "show: y; text: a", "title: 'Đóng'; text: (1)"} {
		if _, err := Pairs(src); err != nil {
			t.Errorf("%q must split: %v", src, err)
		}
	}
}

// A name's parentheses are a modifier's argument, one flat pair, so a ':' inside one is never read as
// the end of the name — whichever way a reader looks for the first ':', it refuses the same value.
// The kernel's twin reads names with /^([\w.+-]|\([\w.+-]*\))+$/, which refuses all four.
func TestPairNameParenthesesBalance(t *testing.T) {
	for _, src := range []string{"click.debounce(3:00): x", "click.debounce(300: x", "click.debounce)300(: x", "click.debounce((300)): x"} {
		if pairs, err := Pairs(src); err == nil {
			t.Errorf("%q must be refused, got %v", src, pairs)
		}
	}
	// control
	if pairs, err := Pairs("click.debounce(300): x"); err != nil || len(pairs) != 1 || pairs[0].Name != "click.debounce(300)" {
		t.Errorf("a modifier's argument is part of the name: %v %v", pairs, err)
	}
}
