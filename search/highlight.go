package search

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	defaultFragmentTokens     = 24
	defaultFragmentContext    = 4
	maximumFragmentTokens     = 256
	maximumFragmentBytes      = defaultMaxDocument
	maximumFragmentScanTokens = defaultMaxFieldTokens
)

// TextRange identifies one match in Fragment.Text using byte offsets.
type TextRange struct {
	Start int
	End   int
}

// Fragment is a bounded source excerpt. Match offsets are relative to Text;
// Start and End identify the same excerpt in the original source.
type Fragment struct {
	Text         string
	Matches      []TextRange
	Start        int
	End          int
	PrefixElided bool
	SuffixElided bool
}

// FragmentOptions bound an excerpt around the first analyzed query match.
// ContextTokens controls how many tokens precede that first match.
type FragmentOptions struct {
	MaxTokens     int
	ContextTokens int
}

type fragmentToken struct {
	start   int
	end     int
	matched bool
}

// Highlight builds a presentation-neutral excerpt using analyzer byte
// offsets. It preserves the source spelling and returns ranges rather than
// injecting markup, so callers can safely render HTML, terminals, or native UI.
func Highlight(
	ctx context.Context,
	analyzer Analyzer,
	text string,
	query string,
	options FragmentOptions,
) (Fragment, error) {
	if ctx == nil {
		return Fragment{}, fmt.Errorf("search: highlight context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Fragment{}, err
	}
	if analyzer == nil {
		return Fragment{}, fmt.Errorf("search: highlight analyzer is nil")
	}
	if !utf8.ValidString(text) || len(text) > maximumFragmentBytes {
		return Fragment{}, fmt.Errorf(
			"search: highlight text is invalid or exceeds %d bytes", maximumFragmentBytes,
		)
	}
	normalized, err := normalizeFragmentOptions(options)
	if err != nil {
		return Fragment{}, err
	}
	terms, err := analyzeQuery(ctx, analyzer, query)
	if err != nil {
		return Fragment{}, err
	}
	queryTerms := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		queryTerms[term] = struct{}{}
	}

	before := make([]fragmentToken, 0, normalized.ContextTokens)
	selected := make([]fragmentToken, 0, normalized.MaxTokens)
	fallback := make([]fragmentToken, 0, normalized.MaxTokens)
	found := false
	stopped := false
	prefixElided := false
	suffixElided := false
	tokenCount := 0
	previousEnd := 0
	var tokenErr error
	err = analyzer.Analyze(ctx, text, func(token Token) bool {
		if stopped || tokenErr != nil {
			return false
		}
		if token.Term == "" || !utf8.ValidString(token.Term) || len(token.Term) > maximumQueryTermBytes {
			tokenErr = fmt.Errorf("search: highlight analyzer emitted an invalid term")
			return false
		}
		if token.Start < previousEnd || token.End <= token.Start || token.End > len(text) ||
			!validUTF8Boundary(text, token.Start) || !validUTF8Boundary(text, token.End) {
			tokenErr = fmt.Errorf("search: highlight analyzer emitted invalid or unordered offsets")
			return false
		}
		if tokenCount >= maximumFragmentScanTokens {
			tokenErr = fmt.Errorf("search: highlight text exceeds %d tokens", maximumFragmentScanTokens)
			return false
		}
		previousEnd = token.End
		tokenCount++
		_, matched := queryTerms[token.Term]
		current := fragmentToken{start: token.Start, end: token.End, matched: matched}

		if !found {
			if len(fallback) < normalized.MaxTokens {
				fallback = append(fallback, current)
			}
			if matched {
				found = true
				prefixElided = tokenCount-len(before)-1 > 0
				selected = append(selected, before...)
				selected = append(selected, current)
				return true
			}
			if normalized.ContextTokens > 0 {
				if len(before) == normalized.ContextTokens {
					copy(before, before[1:])
					before[len(before)-1] = current
				} else {
					before = append(before, current)
				}
			}
			if len(queryTerms) == 0 && tokenCount > normalized.MaxTokens {
				suffixElided = true
				stopped = true
				return false
			}
			return true
		}

		if len(selected) == normalized.MaxTokens {
			suffixElided = true
			stopped = true
			return false
		}
		selected = append(selected, current)
		return true
	})
	if err != nil {
		return Fragment{}, fmt.Errorf("search: analyze highlight text: %w", err)
	}
	if tokenErr != nil {
		return Fragment{}, tokenErr
	}
	if !found {
		selected = fallback
		prefixElided = false
		suffixElided = suffixElided || tokenCount > len(selected)
	}
	return materializeFragment(text, selected, prefixElided, suffixElided), nil
}

func normalizeFragmentOptions(options FragmentOptions) (FragmentOptions, error) {
	if options.MaxTokens == 0 {
		options.MaxTokens = defaultFragmentTokens
	}
	if options.ContextTokens == 0 {
		options.ContextTokens = min(defaultFragmentContext, options.MaxTokens-1)
	}
	if options.MaxTokens < 1 || options.MaxTokens > maximumFragmentTokens {
		return FragmentOptions{}, fmt.Errorf(
			"search: fragment token limit must be between 1 and %d", maximumFragmentTokens,
		)
	}
	if options.ContextTokens < 0 || options.ContextTokens >= options.MaxTokens {
		return FragmentOptions{}, fmt.Errorf(
			"search: fragment context must be between 0 and MaxTokens-1",
		)
	}
	return options, nil
}

func materializeFragment(
	source string,
	tokens []fragmentToken,
	prefixElided bool,
	suffixElided bool,
) Fragment {
	if len(tokens) == 0 {
		return Fragment{}
	}
	start := tokens[0].start
	if !prefixElided {
		start = 0
	}
	end := tokens[len(tokens)-1].end
	if !suffixElided {
		end = len(source)
	}
	matches := make([]TextRange, 0, len(tokens))
	for _, token := range tokens {
		if token.matched {
			matches = append(matches, TextRange{Start: token.start - start, End: token.end - start})
		}
	}
	return Fragment{
		Text: strings.Clone(source[start:end]), Matches: matches, Start: start, End: end,
		PrefixElided: prefixElided, SuffixElided: suffixElided,
	}
}

func validUTF8Boundary(text string, offset int) bool {
	return offset == 0 || offset == len(text) || utf8.RuneStart(text[offset])
}
