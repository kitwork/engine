package highlight

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// shortNames are the names people type out of habit. None of them is accepted:
// a language has one name, written in full, so a page reads the same everywhere.
// Each one maps to what to write instead.
var shortNames = map[string][]string{
	"js":        {"javascript"},
	"ts":        {"typescript"},
	"sh":        {"bash"},
	"shell":     {"bash"},
	"zsh":       {"bash"},
	"ps":        {"powershell"},
	"ps1":       {"powershell"},
	"pwsh":      {"powershell"},
	"py":        {"python"},
	"yml":       {"yaml"},
	"golang":    {"go"},
	"htm":       {"html"},
	"patch":     {"diff"},
	"plaintext": {"text"},
	"txt":       {"text"},
	"kitwork":   {"ui", "work"},
	"kitworkjs": {"work"}, // what the template highlighter called the subset
}

// Suggest says what to write instead of name: the full name of a short one
// ("js" → javascript), the right spelling of a near miss ("javasript" →
// javascript, "Python" → python), or both choices when the name is ambiguous
// ("kitwork" → ui, work). A name that is already right comes back as itself.
// Nothing close returns nil.
//
// A near miss is at most one edit away for names of three to five characters,
// two for longer ones. Names under three characters only match the short-name
// table: at that length one edit reaches almost anything ("cs" is not css).
func Suggest(name string) []string {
	trimmed := strings.TrimSpace(name)
	if utf8.RuneCountInString(trimmed) > longestCandidate()+2 {
		// Two edits cannot reach any name: a pasted paragraph costs one count.
		return nil
	}
	key := strings.ToLower(trimmed)
	if _, known := registry[key]; known {
		return []string{key}
	}
	if targets, found := shortNames[key]; found {
		return append([]string(nil), targets...)
	}

	length := utf8.RuneCountInString(key)
	allowed := 0
	switch {
	case length >= 6:
		allowed = 2
	case length >= 3:
		allowed = 1
	}
	if allowed == 0 {
		return nil
	}

	best := allowed + 1
	var found []string
	consider := func(candidate string, targets ...string) {
		distance := editDistance(key, candidate, allowed)
		if distance > allowed || distance > best {
			return
		}
		if distance < best {
			best, found = distance, nil
		}
		found = append(found, targets...)
	}
	for language := range registry {
		consider(language, language)
	}
	for short, targets := range shortNames {
		// A two-letter short name is too close to too much: "php" is one edit
		// from "py", and python is not what a PHP author meant.
		if utf8.RuneCountInString(short) >= 4 {
			consider(short, targets...)
		}
	}
	return uniqueSorted(found)
}

// UnknownLanguageError is what Tokenize returns for a language= value it does
// not have. Its text is the message an author sees.
type UnknownLanguageError struct {
	Name        string   // what was written
	Suggestions []string // what to write instead; empty when nothing is close
}

func (failure *UnknownLanguageError) Error() string {
	name := clipName(failure.Name)
	if len(failure.Suggestions) > 0 {
		return fmt.Sprintf("ngôn ngữ %q chưa có; viết %s", name, strings.Join(failure.Suggestions, " hoặc "))
	}
	return fmt.Sprintf("ngôn ngữ %q chưa có. Có: %s", name, strings.Join(Languages(), ", "))
}

// clipName keeps the first 64 characters of a name for a message, marking a cut
// with "…": the name comes from a page and may be any length.
func clipName(name string) string {
	count := 0
	for index := range name {
		if count == 64 {
			return name[:index] + "…"
		}
		count++
	}
	return name
}

// longestCandidate is the length, in characters, of the longest name Suggest
// compares against: a language, or a short name of four characters or more.
func longestCandidate() int {
	longest := 0
	for language := range registry {
		longest = max(longest, utf8.RuneCountInString(language))
	}
	for short := range shortNames {
		longest = max(longest, utf8.RuneCountInString(short))
	}
	return longest
}

func unknownLanguage(name string) *UnknownLanguageError {
	return &UnknownLanguageError{Name: name, Suggestions: Suggest(name)}
}

// editDistance counts the edits (insert, delete, replace, swap two neighbours)
// between two short names, giving up as soon as the answer exceeds limit, so a
// pasted paragraph costs one length comparison rather than a full table.
func editDistance(left, right string, limit int) int {
	if difference := utf8.RuneCountInString(left) - utf8.RuneCountInString(right); difference > limit || -difference > limit {
		return limit + 1
	}
	first, second := []rune(left), []rune(right)
	// Three rows of the table: two back (for a swap), one back, and this one.
	twoBack := make([]int, len(second)+1)
	oneBack := make([]int, len(second)+1)
	current := make([]int, len(second)+1)
	for column := range oneBack {
		oneBack[column] = column
	}
	for row := 1; row <= len(first); row++ {
		current[0] = row
		smallest := current[0]
		for column := 1; column <= len(second); column++ {
			cost := 1
			if first[row-1] == second[column-1] {
				cost = 0
			}
			value := min(oneBack[column]+1, current[column-1]+1, oneBack[column-1]+cost)
			if row > 1 && column > 1 && first[row-1] == second[column-2] && first[row-2] == second[column-1] {
				value = min(value, twoBack[column-2]+1)
			}
			current[column] = value
			smallest = min(smallest, value)
		}
		if smallest > limit {
			return limit + 1
		}
		twoBack, oneBack, current = oneBack, current, twoBack
	}
	return oneBack[len(second)]
}

func uniqueSorted(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	unique := names[:1]
	for _, name := range names[1:] {
		if name != unique[len(unique)-1] {
			unique = append(unique, name)
		}
	}
	return unique
}
