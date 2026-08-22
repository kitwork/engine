package search

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHighlightPreservesVietnameseSourceAndBoundsMemoryWindow(t *testing.T) {
	text := "Mo dau, Nguy\u1ec5n dang viet ve Kitwork va Turso."
	fragment, err := Highlight(
		context.Background(), VietnameseAnalyzer(), text, "nguyen",
		FragmentOptions{MaxTokens: 5, ContextTokens: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Text != "dau, Nguy\u1ec5n dang viet ve" || !fragment.PrefixElided || !fragment.SuffixElided {
		t.Fatalf("fragment = %#v", fragment)
	}
	if len(fragment.Matches) != 1 {
		t.Fatalf("matches = %#v", fragment.Matches)
	}
	match := fragment.Matches[0]
	if got := fragment.Text[match.Start:match.End]; got != "Nguy\u1ec5n" {
		t.Fatalf("highlighted source = %q", got)
	}
	if text[fragment.Start:fragment.End] != fragment.Text {
		t.Fatalf("source offsets [%d:%d] do not identify %q", fragment.Start, fragment.End, fragment.Text)
	}
}

func TestHighlightHandlesDecomposedUnicodeAndRepeatedMatches(t *testing.T) {
	text := "Nguye\u0302\u0303n, gap nguyen."
	fragment, err := Highlight(
		context.Background(), VietnameseAnalyzer(), text, "NGUYEN", FragmentOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Text != text || fragment.PrefixElided || fragment.SuffixElided || len(fragment.Matches) != 2 {
		t.Fatalf("fragment = %#v", fragment)
	}
	if got := fragment.Text[fragment.Matches[0].Start:fragment.Matches[0].End]; got != "Nguye\u0302\u0303n" {
		t.Fatalf("first match = %q", got)
	}
	if got := fragment.Text[fragment.Matches[1].Start:fragment.Matches[1].End]; got != "nguyen" {
		t.Fatalf("second match = %q", got)
	}
}

func TestHighlightFallsBackToBoundedHeadWithoutMatch(t *testing.T) {
	fragment, err := Highlight(
		context.Background(), StandardAnalyzer(), "alpha, beta gamma delta", "missing",
		FragmentOptions{MaxTokens: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Text != "alpha, beta" || fragment.PrefixElided || !fragment.SuffixElided || len(fragment.Matches) != 0 {
		t.Fatalf("fragment = %#v", fragment)
	}
}

func TestHighlightValidationAndCancellation(t *testing.T) {
	tests := []struct {
		name     string
		analyzer Analyzer
		text     string
		query    string
		options  FragmentOptions
	}{
		{name: "nil analyzer", text: "alpha", query: "alpha"},
		{name: "invalid text", analyzer: StandardAnalyzer(), text: string([]byte{0xff}), query: "alpha"},
		{name: "oversized text", analyzer: StandardAnalyzer(), text: strings.Repeat("a", maximumFragmentBytes+1), query: "a"},
		{name: "invalid limit", analyzer: StandardAnalyzer(), text: "alpha", query: "alpha", options: FragmentOptions{MaxTokens: maximumFragmentTokens + 1}},
		{name: "invalid context", analyzer: StandardAnalyzer(), text: "alpha", query: "alpha", options: FragmentOptions{MaxTokens: 2, ContextTokens: 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Highlight(context.Background(), test.analyzer, test.text, test.query, test.options); err == nil {
				t.Fatal("Highlight succeeded")
			}
		})
	}
	if _, err := Highlight(nil, StandardAnalyzer(), "alpha", "alpha", FragmentOptions{}); err == nil {
		t.Fatal("nil context Highlight succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Highlight(canceled, StandardAnalyzer(), "alpha", "alpha", FragmentOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Highlight error = %v", err)
	}
}
