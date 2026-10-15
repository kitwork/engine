package highlight

import "strings"

// tokenizeDiff reads a unified diff (git diff, diff -u, a .patch) line by line;
// each line is one token, its line break plain.
//
//   - + lines are Inserted, - lines Deleted, context lines Plain.
//   - @@ -a,b +c,d @@ hunk headers are Heading, and so are the file headers
//     around them: diff --git, index, --- a/…, +++ b/…, new file mode,
//     rename from, Binary files … differ, and their kin.
//   - "\ No newline at end of file" is Comment: a note about the line above,
//     not a line of either file.
//
// Inside a hunk the header's line counts decide, so a removed line that reads
// "-- x" (written "--- x") is Deleted, not a file header. Outside a hunk, or
// after a header without counts, the line's first characters decide, so a
// fragment of + and - lines with no header is still coloured.
func tokenizeDiff(scan *scanner) {
	diff := &diffTokenizer{}
	lineStart := true
	for loop := scan.loop(); loop.next(); {
		source, start := scan.source, scan.position
		if character := scan.peek(); character == '\n' || character == '\r' {
			if lineStart {
				diff.classify("") // an empty line still counts as a line of the hunk
			}
			scan.emit(Plain, scanNewline(source, start))
			lineStart = true
			continue
		}
		end := scanToLineEnd(source, start)
		scan.emit(diff.classify(source[start:end]), end)
		lineStart = false
	}
}

// diffTokenizer remembers how many lines of each side the current hunk still
// holds.
type diffTokenizer struct {
	inHunk           bool // inside a hunk whose header gave its counts
	oldLeft, newLeft int  // lines of the old and new file the hunk still holds
}

// diffHeaders start the lines that describe a file rather than change it.
var diffHeaders = []string{"diff ", "index ", "--- ", "+++ ", "new file mode", "deleted file mode", "old mode",
	"new mode", "similarity index", "dissimilarity index", "rename from", "rename to", "copy from", "copy to",
	"Binary files ", "Index: ", "==="}

// classify says what one line (without its line break) is.
func (diff *diffTokenizer) classify(line string) Kind {
	if diff.inHunk {
		if kind, inside := diff.hunkLine(line); inside {
			return kind
		}
	}
	switch {
	case line == "":
		return Plain
	case strings.HasPrefix(line, "@@"):
		diff.oldLeft, diff.newLeft, diff.inHunk = diffHunkCounts(line)
		return Heading
	case diffIsHeader(line) || line == "---": // git format-patch: "---" ends the message, the diffstat follows
		return Heading
	case line == "-- ": // git format-patch: the signature's separator
		return Plain
	case line[0] == '+':
		return Inserted
	case line[0] == '-':
		return Deleted
	case line[0] == '\\':
		return Comment
	}
	return Plain
}

// hunkLine reads a line inside a counted hunk; inside is false when the line
// cannot belong to it, and the hunk is over.
func (diff *diffTokenizer) hunkLine(line string) (kind Kind, inside bool) {
	switch {
	case line == "" || line[0] == ' ': // an empty line is a context line whose blank was trimmed
		kind = Plain
		diff.oldLeft--
		diff.newLeft--
	case line[0] == '+':
		kind = Inserted
		diff.newLeft--
	case line[0] == '-':
		kind = Deleted
		diff.oldLeft--
	case line[0] == '\\':
		return Comment, true
	default:
		diff.inHunk = false
		return Plain, false
	}
	if diff.oldLeft <= 0 && diff.newLeft <= 0 {
		diff.inHunk = false
	}
	return kind, true
}

func diffIsHeader(line string) bool {
	for _, prefix := range diffHeaders {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// diffHunkCounts reads "@@ -12,9 +12,11 @@ …": how many lines of the old and
// the new file follow (a range with no count holds one), and whether the
// header had counts at all.
func diffHunkCounts(line string) (oldCount, newCount int, counted bool) {
	rest := strings.TrimLeft(line[2:], " ")
	oldCount, rest, ok := diffRange(rest, '-')
	if !ok {
		return 0, 0, false
	}
	newCount, rest, ok = diffRange(strings.TrimLeft(rest, " "), '+')
	if !ok || !strings.HasPrefix(strings.TrimLeft(rest, " "), "@@") {
		return 0, 0, false
	}
	return oldCount, newCount, oldCount > 0 || newCount > 0
}

// diffRange reads "-12,9" or "+3" from the start of text: the count (1 when
// left out) and the text after it.
func diffRange(text string, sign byte) (count int, rest string, ok bool) {
	if text == "" || text[0] != sign {
		return 0, text, false
	}
	index := scanWhile(text, 1, isDecimalDigit)
	if index == 1 {
		return 0, text, false
	}
	count = 1
	if byteAt(text, index) == ',' {
		countEnd := scanWhile(text, index+1, isDecimalDigit)
		if countEnd == index+1 {
			return 0, text, false
		}
		count = 0
		for _, digit := range []byte(text[index+1 : countEnd]) {
			if count < 1<<30 { // a count past a billion lines is a billion lines
				count = count*10 + int(digit-'0')
			}
		}
		index = countEnd
	}
	return count, text[index:], true
}
