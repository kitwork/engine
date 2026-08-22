package search

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type textAnalyzer struct {
	identifier     string
	foldVietnamese bool
}

// StandardAnalyzer lowercases Unicode letters and splits on non-alphanumeric characters.
func StandardAnalyzer() Analyzer {
	return textAnalyzer{identifier: "standard-v1"}
}

// VietnameseAnalyzer additionally folds Vietnamese diacritics so an unaccented
// query such as "ao" matches "ao" with Vietnamese tone marks.
func VietnameseAnalyzer() Analyzer {
	return textAnalyzer{identifier: "vietnamese-fold-v1", foldVietnamese: true}
}

func (analyzer textAnalyzer) Identifier() string {
	return analyzer.identifier
}

func (analyzer textAnalyzer) Analyze(ctx context.Context, text string, emit func(Token) bool) error {
	if ctx == nil {
		return fmt.Errorf("search: analyzer context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if emit == nil {
		return fmt.Errorf("search: analyzer emit callback is nil")
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("search: analyzer input is not valid UTF-8")
	}

	var term strings.Builder
	start := -1
	end := 0
	position := uint32(0)
	nextContextCheck := 0
	flush := func() bool {
		if start < 0 || term.Len() == 0 {
			term.Reset()
			start = -1
			return true
		}
		keepGoing := emit(Token{
			Term: term.String(), Position: position, Start: start, End: end,
		})
		position++
		term.Reset()
		start = -1
		return keepGoing
	}

	for offset := 0; offset < len(text); {
		if offset >= nextContextCheck {
			if err := ctx.Err(); err != nil {
				return err
			}
			nextContextCheck = offset + 4096
		}
		r, size := utf8.DecodeRuneInString(text[offset:])
		runeEnd := offset + size
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = offset
			}
			end = runeEnd
			term.WriteRune(analyzer.foldRune(r))
			offset = runeEnd
			continue
		}

		if start >= 0 && unicode.IsMark(r) {
			if analyzer.foldVietnamese && isVietnameseMark(r) {
				end = runeEnd
				offset = runeEnd
				continue
			}
			if !analyzer.foldVietnamese {
				end = runeEnd
				term.WriteRune(unicode.ToLower(r))
				offset = runeEnd
				continue
			}
		}

		if !flush() {
			return nil
		}
		offset = runeEnd
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func (analyzer textAnalyzer) foldRune(r rune) rune {
	r = unicode.ToLower(r)
	if !analyzer.foldVietnamese {
		return r
	}
	if folded, ok := vietnameseFoldTable[r]; ok {
		return folded
	}
	return r
}

var vietnameseFoldTable = buildVietnameseFoldTable()

func buildVietnameseFoldTable() map[rune]rune {
	groups := map[rune]string{
		'a': "áàảãạăắằẳẵặâấầẩẫậ",
		'e': "éèẻẽẹêếềểễệ",
		'i': "íìỉĩị",
		'o': "óòỏõọôốồổỗộơớờởỡợ",
		'u': "úùủũụưứừửữự",
		'y': "ýỳỷỹỵ",
		'd': "đ",
		'c': "ç",
		'n': "ñ",
	}
	table := make(map[rune]rune, 128)
	for base, variants := range groups {
		for _, variant := range variants {
			table[variant] = base
		}
	}
	return table
}

func isVietnameseMark(r rune) bool {
	switch r {
	case '\u0300', '\u0301', '\u0302', '\u0303', '\u0306', '\u0309', '\u031b', '\u0323', '\u0327':
		return true
	default:
		return false
	}
}
