package ui

import "testing"

// A word from elsewhere written with a prefix is told what it is told without one (review 13/10/2026):
// the word .ui writes, and, where the table names it, that word with the prefix kept. Before, it was
// told only that it takes no prefix, as if pl were a word of .ui. CONTROL: a word of .ui that takes no
// prefix (if, heading) and a typo the table does not know keep the error that says so.
func TestPrefixedWordsFromElsewhereAreTold(t *testing.T) {
	for _, c := range []struct{ source, at, says string }{
		{`<content><box medium:pl="4">x</box></content>`, "content.ui:1:15",
			`medium:pl="4": pl là chữ viết tắt của Tailwind; .ui viết đủ chữ: padding-left (theo thang khoảng cách). Có tiền tố thì viết medium:padding-left`},
		{`<content><box medium:pl-4>x</box></content>`, "content.ui:1:15", `Có tiền tố thì viết medium:padding-left="4"`},
		{`<content><box hover:rounded-t-lg>x</box></content>`, "content.ui:1:15",
			`.ui viết thuộc tính: radius-top="medium" (.5rem). Các chữ: none, small, medium, large, full. Có tiền tố thì viết hover:radius-top="medium"`},
		{`<content><box medium:border-top-left-radius="8px">x</box></content>`, "content.ui:1:15",
			`medium:border-top-left-radius="8px": góc trên bên trái là radius-top-left (none, small, medium, large, full). Có tiền tố thì viết medium:radius-top-left`},
		{`<content><box large+focus:radius-left-top="large">x</box></content>`, "content.ui:1:15", `Có tiền tố thì viết large+focus:radius-top-left`},
		{`<content><box medium:margin="4">x</box></content>`, "content.ui:1:15", `medium:margin="4": không có margin: khoảng cách giữa các phần là gap`},
		{`<content><box medium:href="/x">x</box></content>`, "content.ui:1:15", `medium:href là chữ của web; đích của link là value`},
	} {
		mustFail(t, content(c.source), c.at, c.says)
	}
	// CONTROL
	for _, c := range []struct{ source, says string }{
		{`<content><box large:if="x">x</box></content>`, "if không nhận tiền tố"},
		{`<content><text color="ink" hover:heading="2">x</text></content>`, "heading không nhận tiền tố (chỉ thuộc tính trình bày và show nhận tiền tố)"},
		{`<content><box medium:paddng="4">x</box></content>`, "paddng không nhận tiền tố (chỉ thuộc tính trình bày và show nhận tiền tố)"},
		{`<content><box medium:pl="4">x</box></content>`, `pl="4": pl là chữ viết tắt của Tailwind`}, // the bare word's own words, kept
	} {
		mustFail(t, content(c.source), "content.ui:1:", c.says)
	}
	mustFail(t, content(`<content><box pl="4">x</box></content>`), "content.ui:1:15", `pl="4": pl là chữ viết tắt của Tailwind; .ui viết đủ chữ: padding-left (theo thang khoảng cách)`)
}
