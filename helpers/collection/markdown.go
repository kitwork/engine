package collection

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	inlineCodeRE = regexp.MustCompile("`([^`\n]+)`")
	linkRE       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	strongRE     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	emphasisRE   = regexp.MustCompile(`\*([^*]+)\*`)
)

type markdownRenderer struct {
	html     strings.Builder
	toc      []Heading
	ids      map[string]int
	title    string
	skippedH bool
}

// Bump this when rendered HTML semantics change so persisted documents rebuild.
const markdownRendererVersion = "3"

func renderMarkdown(body []byte, title string) (string, []Heading) {
	r := &markdownRenderer{ids: make(map[string]int), title: strings.TrimSpace(title)}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")

	var paragraph []string
	var listItems []string
	var quote []string
	var code []string
	listTag := ""
	inCode := false
	codeLanguage := ""

	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		r.html.WriteString("<p>")
		r.html.WriteString(renderInline(strings.Join(paragraph, " ")))
		r.html.WriteString("</p>\n")
		paragraph = nil
	}
	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		r.html.WriteByte('<')
		r.html.WriteString(listTag)
		r.html.WriteString(">\n")
		for _, item := range listItems {
			r.html.WriteString("<li>")
			r.html.WriteString(renderInline(item))
			r.html.WriteString("</li>\n")
		}
		r.html.WriteString("</")
		r.html.WriteString(listTag)
		r.html.WriteString(">\n")
		listItems = nil
		listTag = ""
	}
	flushQuote := func() {
		if len(quote) == 0 {
			return
		}
		r.html.WriteString("<blockquote>\n")
		var paragraph []string
		flushQuoteParagraph := func() {
			if len(paragraph) == 0 {
				return
			}
			r.html.WriteString("<p>")
			r.html.WriteString(renderInline(strings.Join(paragraph, " ")))
			r.html.WriteString("</p>\n")
			paragraph = nil
		}
		for _, line := range quote {
			if line == "" {
				flushQuoteParagraph()
				continue
			}
			paragraph = append(paragraph, line)
		}
		flushQuoteParagraph()
		r.html.WriteString("</blockquote>\n")
		quote = nil
	}
	flushCode := func() {
		r.html.WriteString("<pre><code")
		if codeLanguage != "" {
			r.html.WriteString(` class="language-`)
			r.html.WriteString(html.EscapeString(codeLanguage))
			r.html.WriteByte('"')
		}
		r.html.WriteByte('>')
		r.html.WriteString(html.EscapeString(strings.Join(code, "\n")))
		r.html.WriteString("</code></pre>\n")
		code = nil
		codeLanguage = ""
	}
	flushBlocks := func() {
		flushParagraph()
		flushList()
		flushQuote()
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			flushBlocks()
			if inCode {
				flushCode()
			} else {
				codeLanguage = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			inCode = !inCode
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		if trimmed == "" {
			flushBlocks()
			continue
		}
		if trimmed == "---" || trimmed == "***" {
			flushBlocks()
			r.html.WriteString("<hr>\n")
			continue
		}
		if level, text, ok := markdownHeading(trimmed); ok {
			flushBlocks()
			if level == 1 && !r.skippedH && r.title != "" {
				r.skippedH = true
				continue
			}
			id := r.headingID(text)
			fmt.Fprintf(&r.html, "<h%d id=\"%s\">%s</h%d>\n", level, id, renderInline(text), level)
			if level >= 2 && level <= 4 {
				r.toc = append(r.toc, Heading{ID: id, Title: text, Level: level})
			}
			continue
		}
		if item, ok := unorderedItem(trimmed); ok {
			flushParagraph()
			flushQuote()
			if listTag != "" && listTag != "ul" {
				flushList()
			}
			listTag = "ul"
			listItems = append(listItems, item)
			continue
		}
		if item, ok := orderedItem(trimmed); ok {
			flushParagraph()
			flushQuote()
			if listTag != "" && listTag != "ol" {
				flushList()
			}
			listTag = "ol"
			listItems = append(listItems, item)
			continue
		}
		if trimmed == ">" {
			flushParagraph()
			flushList()
			if len(quote) > 0 && quote[len(quote)-1] != "" {
				quote = append(quote, "")
			}
			continue
		}
		if strings.HasPrefix(trimmed, "> ") {
			flushParagraph()
			flushList()
			quote = append(quote, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
			continue
		}

		flushList()
		flushQuote()
		paragraph = append(paragraph, trimmed)
	}

	flushBlocks()
	if inCode || len(code) > 0 {
		flushCode()
	}
	return r.html.String(), r.toc
}

func markdownHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

func unorderedItem(line string) (string, bool) {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return strings.TrimSpace(line[2:]), true
	}
	return "", false
}

func orderedItem(line string) (string, bool) {
	dot := strings.IndexByte(line, '.')
	if dot <= 0 || dot+1 >= len(line) || line[dot+1] != ' ' {
		return "", false
	}
	if _, err := strconv.Atoi(line[:dot]); err != nil {
		return "", false
	}
	return strings.TrimSpace(line[dot+2:]), true
}

func (r *markdownRenderer) headingID(title string) string {
	var b strings.Builder
	pendingDash := false
	for _, ch := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case unicode.IsLetter(ch) || unicode.IsDigit(ch):
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(ch)
		case ch == ' ' || ch == '-' || ch == '_' || ch == '/':
			pendingDash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "section"
	}
	r.ids[id]++
	if r.ids[id] > 1 {
		return id + "-" + strconv.Itoa(r.ids[id])
	}
	return id
}

func renderInline(input string) string {
	escaped := html.EscapeString(input)
	var codeSpans []string
	escaped = inlineCodeRE.ReplaceAllStringFunc(escaped, func(match string) string {
		sub := inlineCodeRE.FindStringSubmatch(match)
		codeSpans = append(codeSpans, "<code>"+sub[1]+"</code>")
		return "\x00CODE" + strconv.Itoa(len(codeSpans)-1) + "\x00"
	})
	escaped = linkRE.ReplaceAllStringFunc(escaped, func(match string) string {
		sub := linkRE.FindStringSubmatch(match)
		href := sub[2]
		if !safeHref(href) {
			href = "#"
		}
		attributes := ""
		if strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://") {
			attributes = ` target="_blank" rel="noopener noreferrer"`
		}
		return `<a href="` + href + `"` + attributes + `>` + sub[1] + "</a>"
	})
	escaped = strongRE.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = emphasisRE.ReplaceAllString(escaped, "<em>$1</em>")
	for i, span := range codeSpans {
		escaped = strings.ReplaceAll(escaped, "\x00CODE"+strconv.Itoa(i)+"\x00", span)
	}
	return escaped
}

func safeHref(href string) bool {
	return strings.HasPrefix(href, "https://") ||
		strings.HasPrefix(href, "http://") ||
		strings.HasPrefix(href, "/") ||
		strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "mailto:")
}
