package javascript

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestScanHTMLResourceLimitsAcceptExactBoundaryAndRejectOverflow(t *testing.T) {
	tests := []struct {
		name  string
		exact []byte
		over  []byte
		want  string
	}{
		{
			name:  "document source",
			exact: make([]byte, scanHTMLSourceByteLimit),
			over:  scanHTMLSourceOverLimit(),
			want:  "document source bytes",
		},
		{
			name:  "markup candidates",
			exact: []byte(strings.Repeat("<br>", scanHTMLTagLimit)),
			over:  []byte(strings.Repeat("<br>", scanHTMLTagLimit+1)),
			want:  "markup candidates",
		},
		{
			name:  "open element depth",
			exact: nestedScanHTML(scanHTMLDepthLimit),
			over:  nestedScanHTML(scanHTMLDepthLimit + 1),
			want:  "open element depth",
		},
		{
			name:  "tag name",
			exact: []byte("<" + strings.Repeat("a", scanHTMLTagNameByteLimit) + ">"),
			over:  []byte("<" + strings.Repeat("a", scanHTMLTagNameByteLimit+1) + ">"),
			want:  "tag name bytes",
		},
		{
			name:  "attributes per tag",
			exact: scanHTMLWithAttributes(scanHTMLAttributeLimit),
			over:  scanHTMLWithAttributes(scanHTMLAttributeLimit + 1),
			want:  "tag attributes",
		},
		{
			name:  "attribute name",
			exact: []byte("<div " + strings.Repeat("a", scanHTMLAttributeNameByteLimit) + ">"),
			over:  []byte("<div " + strings.Repeat("a", scanHTMLAttributeNameByteLimit+1) + ">"),
			want:  "attribute name bytes",
		},
		{
			name: "quoted attribute value",
			exact: []byte(`<div title="` +
				strings.Repeat("a", scanHTMLAttributeValueByteLimit) + `">`),
			over: []byte(`<div title="` +
				strings.Repeat("a", scanHTMLAttributeValueByteLimit+1) + `">`),
			want: "attribute value bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ScanHTML(test.exact); err != nil {
				t.Fatalf("exact boundary (%d bytes) = %v", len(test.exact), err)
			}
			_, first := ScanHTML(test.over)
			if !errors.Is(first, errHTMLScanLimit) || !strings.Contains(first.Error(), test.want) {
				t.Fatalf("overflow error = %v, want errHTMLScanLimit containing %q", first, test.want)
			}
			_, second := ScanHTML(test.over)
			if second == nil || second.Error() != first.Error() {
				t.Fatalf("overflow diagnostic changed: first=%v second=%v", first, second)
			}
		})
	}
}

func TestScanHTMLRejectsAttribute257BeforeParsingItsValue(t *testing.T) {
	source := strings.TrimSuffix(string(scanHTMLWithAttributes(scanHTMLAttributeLimit)), ">") +
		` overflow="` + strings.Repeat("x", scanHTMLAttributeValueByteLimit+1) + `">`
	_, err := ScanHTML([]byte(source))
	if !errors.Is(err, errHTMLScanLimit) || !strings.Contains(err.Error(), "tag attributes") {
		t.Fatalf("257th oversized attribute error = %v, want pre-parse tag attribute limit", err)
	}
	if strings.Contains(err.Error(), "attribute value") {
		t.Fatalf("257th attribute value was parsed before the count fence: %v", err)
	}
}

func TestScanHTMLDeepPlateauHasDeterministicAncestorWorkFence(t *testing.T) {
	source := deepPlateauScanHTML()
	_, first := ScanHTML(source)
	if !errors.Is(first, errHTMLScanLimit) || !strings.Contains(first.Error(), "ancestor frame visits") {
		t.Fatalf("deep plateau error = %v, want ancestor work limit", first)
	}
	_, second := ScanHTML(source)
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("deep plateau diagnostic changed: first=%v second=%v", first, second)
	}
}

func TestAuthoredExpressionSourceLimitsPrecedeDecodeAndLex(t *testing.T) {
	exactRaw := rawExpressionAtLimit(expressionAuthoredSourceByteLimit)
	if len(exactRaw) != expressionAuthoredSourceByteLimit {
		t.Fatalf("raw expression bytes = %d, want %d", len(exactRaw), expressionAuthoredSourceByteLimit)
	}
	if err := validateExpression(exactRaw, "binding"); err != nil {
		t.Fatalf("exact authored source limit = %v", err)
	}
	overRaw := exactRaw + " "
	for name, validate := range map[string]func(string) error{
		"expression": func(source string) error { return validateExpression(source, "binding") },
		"bind":       validateBindExpression,
		"style":      validateStyleExpression,
		"model":      validateModelExpression,
		"for":        validateForExpression,
	} {
		if err := validate(overRaw); err == nil || !strings.Contains(err.Error(), "authored bytes") {
			t.Errorf("%s raw overflow = %v, want authored-byte rejection", name, err)
		}
	}

	exactDecoded := `"` + strings.Repeat("a", expressionDecodedSourceLimit-2) + `"`
	if got := expressionUTF16Length(exactDecoded); got != expressionDecodedSourceLimit {
		t.Fatalf("decoded expression length = %d, want %d", got, expressionDecodedSourceLimit)
	}
	if err := validateDecodedExpression(exactDecoded, "binding"); err != nil {
		t.Fatalf("exact decoded source limit = %v", err)
	}
	if err := validateDecodedExpression(exactDecoded+" ", "binding"); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("decoded overflow = %v, want UTF-16 rejection", err)
	}

	// Astral characters consume two units in both Go's decoded validator and
	// JavaScript String.length, so the cross-runtime boundary stays exact.
	exactAstral := `"` + strings.Repeat("😀", (expressionDecodedSourceLimit-2)/2) + `"`
	if err := validateDecodedExpression(exactAstral, "binding"); err != nil {
		t.Fatalf("exact astral decoded source limit = %v", err)
	}
	if err := validateDecodedExpression(exactAstral+"😀", "binding"); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("astral decoded overflow = %v, want UTF-16 rejection", err)
	}

	// Browsers retain ambiguous legacy references in attributes when an ASCII
	// alphanumeric byte follows the legacy name. Text-context decoding would
	// incorrectly shrink these strings before applying the source fence.
	exactAmbiguous := `"` + strings.Repeat("&notit;", (expressionDecodedSourceLimit-2)/len("&notit;")) + `"`
	if got := expressionUTF16Length(exactAmbiguous); got != expressionDecodedSourceLimit {
		t.Fatalf("exact ambiguous source length = %d, want %d", got, expressionDecodedSourceLimit)
	}
	if err := validateExpression(exactAmbiguous, "binding"); err != nil {
		t.Fatalf("exact ambiguous attribute source = %v", err)
	}
	if err := validateExpression(exactAmbiguous[:len(exactAmbiguous)-1]+"&notit;\"", "binding"); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("ambiguous attribute overflow = %v, want UTF-16 rejection", err)
	}

	// HTML preprocessing folds CRLF and lone CR before getAttribute exposes the
	// value. The raw-byte fence still runs first, while the decoded fence counts
	// the normalized browser string.
	exactCRLF := `"` + strings.Repeat("\r\n", expressionDecodedSourceLimit-2) + `"`
	if err := validateExpression(exactCRLF, "binding"); err != nil {
		t.Fatalf("exact CRLF-normalized source = %v", err)
	}
	overCRLF := exactCRLF[:len(exactCRLF)-1] + "\r\n\""
	if err := validateExpression(overCRLF, "binding"); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("CRLF-normalized overflow = %v, want UTF-16 rejection", err)
	}

	source := []byte(`<p data-kit-text="` + overRaw + `"></p>`)
	if _, err := ScanHTML(source); !errors.Is(err, ErrInvalidExpressionUse) ||
		!strings.Contains(err.Error(), "authored bytes") {
		t.Fatalf("ScanHTML expression overflow = %v, want ErrInvalidExpressionUse", err)
	}
}

func TestExpressionTokenLimitAcceptsExactStoredBoundaryAndRejectsOverflow(t *testing.T) {
	exact := strings.Repeat(",", expressionTokenLimit-1)
	tokens, err := lexExpression(exact)
	if err != nil {
		t.Fatalf("exact token boundary = %v", err)
	}
	if len(tokens) != expressionTokenLimit {
		t.Fatalf("exact stored tokens = %d, want %d including end sentinel", len(tokens), expressionTokenLimit)
	}
	over := exact + ","
	if _, err := lexExpression(over); err == nil || !strings.Contains(err.Error(), "tokens") {
		t.Fatalf("token overflow = %v, want token-limit rejection", err)
	}
}

func TestScanHTMLCumulativeAuthoredExpressionBudget(t *testing.T) {
	attribute := rawExpressionAtLimit(expressionAuthoredSourceByteLimit)
	var exact strings.Builder
	for range scanHTMLExpressionByteLimit / len(attribute) {
		exact.WriteString(`<output data-kit-text="`)
		exact.WriteString(attribute)
		exact.WriteString(`"></output>`)
	}
	if _, err := ScanHTML([]byte(exact.String())); err != nil {
		t.Fatalf("exact cumulative expression bytes = %v", err)
	}
	over := exact.String() + `<output data-kit-text="x"></output>`
	_, first := ScanHTML([]byte(over))
	if !errors.Is(first, errHTMLScanLimit) || !strings.Contains(first.Error(), "authored expression bytes") {
		t.Fatalf("cumulative expression overflow = %v, want HTML scan limit", first)
	}
	_, second := ScanHTML([]byte(over))
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("cumulative expression diagnostic changed: first=%v second=%v", first, second)
	}
}

func TestBrowserExpressionSourceFenceMatchesGenerationLimit(t *testing.T) {
	coreSource, err := sources.ReadFile("src/core.js")
	if err != nil {
		t.Fatal(err)
	}
	coreText := string(coreSource)
	for _, required := range []string{
		"var EXPRESSION_SOURCE_LIMIT = " + strconv.Itoa(expressionDecodedSourceLimit) + ";",
		"source.length > EXPRESSION_SOURCE_LIMIT",
		"expressionSource: expressionSource",
	} {
		if !strings.Contains(coreText, required) {
			t.Fatalf("browser core lost expression source fence %q", required)
		}
	}
	lexerSource, err := sources.ReadFile("src/lexer.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"var TOKEN_LIMIT = " + strconv.Itoa(expressionTokenLimit) + ";",
		"tokens.length >= TOKEN_LIMIT",
		"tokens.push({ type: type, value: value, position: position })",
	} {
		if !strings.Contains(string(lexerSource), required) {
			t.Fatalf("browser lexer lost token fence %q", required)
		}
	}
	for _, fragment := range []string{"evaluator.js", "dom.js", "structure.js", "model.js"} {
		source, readErr := sources.ReadFile("src/" + fragment)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(source), "core.expressionSource(") {
			t.Fatalf("src/%s bypasses the shared expression source fence", fragment)
		}
	}
}

func TestScannerMetadataUsesBrowserAttributeDecoding(t *testing.T) {
	result, err := ScanHTML([]byte(`<main data-kit-component="&#97;pp@1.1.0" data-kit-as="$&#97;pp" data-kit-retain="row&#45;1"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 {
		t.Fatalf("managed components = %#v", result.Components)
	}
	component := result.Components[0]
	if component.Name != "app" || component.Version != "1.1.0" || component.Alias != "$app" || component.Retain != "row-1" {
		t.Fatalf("decoded component metadata = %#v", component)
	}

	result, err = ScanHTML([]byte(`<div data-kit-component="dialog@1&#46;2&#46;3"></div>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 || result.Components[0].Version != "1.2.3" {
		t.Fatalf("decoded inline version = %#v", result.Components)
	}

	if _, _, err := parseComponentSpec("dialog&notit;"); err == nil || !strings.Contains(err.Error(), "&notit;") {
		t.Fatalf("ambiguous component reference = %v, want literal browser value", err)
	}
	if retain, err := parseRetainKey("row&#45;1"); err != nil || retain != "row-1" {
		t.Fatalf("numeric retain decode = %q, %v", retain, err)
	}
	if _, err := ScanHTML([]byte(`<div data-kit-component="dialog" data-kit-local="&notit;"></div>`)); !errors.Is(err, ErrUnsupportedAttribute) || !strings.Contains(err.Error(), "is not implemented") {
		t.Fatalf("removed local marker = %v", err)
	}

	charset, csp := deliveryMetaSecurity(map[string]deliveryHeadAttribute{
		"http-equiv": {hasValue: true, value: "content-security&#45;policy"},
	})
	if charset || !csp {
		t.Fatalf("encoded CSP metadata = charset %t, csp %t", charset, csp)
	}
	charset, csp = deliveryMetaSecurity(map[string]deliveryHeadAttribute{
		"http-equiv": {hasValue: true, value: "content&#45;type"},
		"content":    {hasValue: true, value: "text/html; charset&#61;utf-8"},
	})
	if !charset || csp {
		t.Fatalf("encoded content-type metadata = charset %t, csp %t", charset, csp)
	}
	if !reservedDeliveryMarker("script", "data-kitwork-jit", "runt&#105;me", true) {
		t.Fatal("encoded engine JIT role was not recognized")
	}
	if reservedDeliveryMarker("script", "data-kitwork-jit", "runt&notit;ime", true) {
		t.Fatal("ambiguous engine JIT role was decoded with text-context rules")
	}
}

func FuzzScanHTMLResourceBounds(f *testing.F) {
	for _, source := range [][]byte{
		[]byte(`<main data-kit-scope="count: 0"><b data-kit-text="count"></b></main>`),
		[]byte(`<div data-kit-text="`),
		[]byte(`<!-- unterminated <div data-kit-component="dialog@1.0.0">`),
		[]byte(`<section data-kit-ignore><table><div data-kit-text="broken ="></table>`),
		[]byte(`</` + strings.Repeat("a", scanHTMLTagNameByteLimit+1) + `>`),
		[]byte(`<p data-kit-text="'&notit;&copycat'"></p>`),
		[]byte("<p data-kit-text=\"'a\r\nb'\"></p>"),
		bytesRepeatNestedMalformed(48),
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		_, _ = ScanHTML(source)
	})
}

func BenchmarkScanHTMLNestedDepth(b *testing.B) {
	for _, depth := range []int{256, 512, 1024, 2048} {
		source := nestedScanHTML(depth)
		wantLimit := depth > scanHTMLDepthLimit
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			for range b.N {
				_, err := ScanHTML(source)
				if wantLimit != errors.Is(err, errHTMLScanLimit) {
					b.Fatalf("ScanHTML(depth=%d) error = %v, wantLimit=%t", depth, err, wantLimit)
				}
			}
		})
	}
}

func BenchmarkScanHTMLDeepPlateauWorkFence(b *testing.B) {
	source := deepPlateauScanHTML()
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	for range b.N {
		_, err := ScanHTML(source)
		if !errors.Is(err, errHTMLScanLimit) || !strings.Contains(err.Error(), "ancestor frame visits") {
			b.Fatalf("ScanHTML deep plateau error = %v", err)
		}
	}
}

func BenchmarkScanHTMLNearSourceLimitPunctuationExpressions(b *testing.B) {
	source := nearSourceLimitExpressionHTML()
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	for range b.N {
		_, err := ScanHTML(source)
		if !errors.Is(err, errHTMLScanLimit) || !strings.Contains(err.Error(), "authored expression bytes") {
			b.Fatalf("ScanHTML punctuation page error = %v", err)
		}
	}
}

func nestedScanHTML(depth int) []byte {
	return []byte(strings.Repeat("<div>", depth) + strings.Repeat("</div>", depth))
}

func deepPlateauScanHTML() []byte {
	return []byte(strings.Repeat("<span>", scanHTMLDepthLimit) +
		strings.Repeat("<br>", scanHTMLTagLimit-scanHTMLDepthLimit))
}

func punctuationHeavyExpression() string {
	properties := (expressionTokenLimit - 2) / 4
	return "{" + strings.Repeat("a:0,", properties-1) + "a:0}"
}

func nearSourceLimitExpressionHTML() []byte {
	expression := punctuationHeavyExpression()
	tag := `<output data-kit-text="` + expression + `"></output>`
	var source strings.Builder
	source.Grow(scanHTMLSourceByteLimit)
	for source.Len()+len(tag) <= scanHTMLSourceByteLimit {
		source.WriteString(tag)
	}
	if source.Len() < scanHTMLSourceByteLimit {
		source.WriteString(strings.Repeat("x", scanHTMLSourceByteLimit-source.Len()))
	}
	return []byte(source.String())
}

func scanHTMLSourceOverLimit() []byte {
	source := make([]byte, scanHTMLSourceByteLimit+1)
	copy(source, `<div data-kit-unknown="must-not-parse">`)
	return source
}

func scanHTMLWithAttributes(count int) []byte {
	var source strings.Builder
	source.Grow(6 + count*8)
	source.WriteString("<div")
	for index := 0; index < count; index++ {
		source.WriteString(" a")
		source.WriteString(strconv.Itoa(index))
	}
	source.WriteByte('>')
	return []byte(source.String())
}

func rawExpressionAtLimit(limit int) string {
	const prefix = "value"
	const encodedSpace = "&#32;"
	encoded := (limit - len(prefix)) / len(encodedSpace)
	remainder := limit - len(prefix) - encoded*len(encodedSpace)
	return prefix + strings.Repeat(encodedSpace, encoded) + strings.Repeat(" ", remainder)
}

func bytesRepeatNestedMalformed(depth int) []byte {
	return []byte(fmt.Sprintf("%s<span data-kit-click='value ='>", strings.Repeat("<div>", depth)))
}
