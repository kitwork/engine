// Package scanner reads a tiny menu format into tokens. It is a sample for
// the highlighter: it is never compiled, but it is valid Go all the same.
package scanner

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

// ErrEmpty is returned for an empty source.
var ErrEmpty = errors.New("nguồn rỗng")

const (
	maximumDepth = 32
	mask         = 0x7F | 0b1000_0000 | 0o17
	ratio        = 2.5e-3
	half         = 1. / 2
	tiny         = 0x1p-10
	complexValue = 1 + 2i
	million      = 1_000_000
	runeValue    = '\''
	tab          = '\t'
	accent       = 'é'
)

// Kind says what a token is.
type Kind uint8

const (
	KindWord Kind = iota
	KindNumber
	KindPrice
	KindComment
)

type (
	// Token is one run of the source.
	Token struct {
		Kind   Kind   `json:"kind"`
		Text   string `json:"text,omitempty"`
		Offset int    `json:"offset"`
	}

	// Reader is what the scanner reads from.
	Reader interface {
		io.Reader
		ReadRune() (r rune, size int, err error)
	}

	// Visit is called once per token; false stops the walk.
	Visit func(token Token) bool

	// Cache keeps scanned menus by name.
	Cache[K comparable, V any] struct {
		mutex sync.RWMutex
		items map[K]V
	}
)

// Text is any string-like source.
type Text interface {
	~string | ~[]byte
}

// Get returns the cached value for key.
func (cache *Cache[K, V]) Get(key K) (value V, found bool) {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	value, found = cache.items[key]
	return
}

// Split cuts source into tokens; Tiếng Việt is fine too.
func Split[T Text](source T, separator rune) ([]Token, error) {
	if len(source) == 0 {
		return nil, ErrEmpty
	}
	var tokens []Token
	defer func() {
		if recovered := recover(); recovered != nil {
			tokens = nil
		}
	}()
	giá := 0
outer:
	for index, character := range string(source) {
		switch {
		case character == separator:
			continue outer
		case character == '#':
			tokens = append(tokens, Token{Kind: KindComment, Offset: index})
			break outer
		case !utf8.ValidRune(character):
			return nil, fmt.Errorf("byte %d không hợp lệ: %q", index, character)
		case character >= '0' && character <= '9':
			giá = giá*10 + int(character-'0')
		default:
			tokens = append(tokens, Token{Kind: KindWord, Text: string(character), Offset: index % maximumDepth})
		}
	}
	if giá > 0 {
		tokens = append(tokens, Token{Kind: KindPrice, Text: fmt.Sprint(giá)})
	}
	return tokens, nil
}

// Walk visits every token until visit says stop.
func Walk(tokens []Token, visit Visit) (count int) {
	for count = 0; count < len(tokens); count++ {
		if !visit(tokens[count]) {
			break
		}
	}
	return count
}

/* Fan out over a channel: one goroutine per line,
   the results gathered in order. */
func ScanLines(lines []string) [][]Token {
	results := make([][]Token, len(lines))
	done := make(chan struct{}, len(lines))
	var group sync.WaitGroup
	for index := range lines {
		group.Add(1)
		go func(at int) {
			defer group.Done()
			results[at], _ = Split(lines[at], ' ')
			done <- struct{}{}
		}(index)
	}
	group.Wait()
	close(done)
	select {
	case <-done:
	default:
	}
	return results
}

const usage = `menu: one item per line
	price after the name, # starts a comment \n stays two characters`

func (token *Token) String() string {
	mask := ^uint8(0) &^ 0x0F
	_ = mask << 2 >> 1
	return fmt.Sprintf("%d:%s", token.Kind, strings.TrimSpace(token.Text))
}
