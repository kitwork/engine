package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"$((", "((", "<(", ">(", "=(", "x=(", "[[", "]]", "case x in", ";;", "esac", "<<<", "2>&1", "&>",
		"$'", `$"`, "${#", "${x:-${", "`$(", "\\\n", "<<A <<B\n", "<<-\tEND\n\tEND\n", "f() {", "for ((",
	)
}

// The first word of a command is the command; flags, numbers, keywords,
// strings with expansions, heredocs, arithmetic and operators around it.
func TestBash(t *testing.T) {
	checkExact(t, "bash", []tokenCase{
		{"", nil},
		{"echo hello", tokensOf(Function, "echo", Plain, " hello")},
		{"ls -la --color=auto /tmp", tokensOf(
			Function, "ls", Plain, " ", Attribute, "-la", Plain, " ", Attribute, "--color", Operator, "=", Plain, "auto /tmp")},
		{"# note\necho done", tokensOf(Comment, "# note", Plain, "\n", Function, "echo", Plain, " done")},
		// # starts a comment only where a word could start.
		{"echo a#b # c", tokensOf(Function, "echo", Plain, " a#b ", Comment, "# c")},
		// … and not where a word goes on after an expansion or a quote.
		{`echo ${name}#tag "a"#b`, tokensOf(
			Function, "echo", Plain, " ", Variable, "${name}", Plain, "#tag ", String, `"a"`, Plain, "#b")},
		// Keywords only where a command could start.
		{"echo if then done in", tokensOf(Function, "echo", Plain, " if then done in")},
		{`if [ -f "$file" ]; then exit 1; fi`, tokensOf(
			Keyword, "if", Plain, " ", Function, "[", Plain, " ", Attribute, "-f", Plain, " ",
			String, `"`, Variable, "$file", String, `"`, Plain, " ", Punctuation, "]", Operator, ";", Plain, " ",
			Keyword, "then", Plain, " ", Function, "exit", Plain, " ", Number, "1", Operator, ";", Plain, " ", Keyword, "fi")},
		// An assignment before a command leaves the next word the command.
		{"ALLOW_LOCAL=true PORT=8107 go run .", tokensOf(
			Variable, "ALLOW_LOCAL", Operator, "=", Plain, "true ", Variable, "PORT", Operator, "=", Number, "8107",
			Plain, " ", Function, "go", Plain, " run .")},
		{"STAMP=$(date +%s) make build", tokensOf(
			Variable, "STAMP", Operator, "=", Punctuation, "$(", Function, "date", Plain, " +%s", Punctuation, ")",
			Plain, " ", Function, "make", Plain, " build")},
		// Inside double quotes every expansion is a variable; the text around it stays one string.
		{`echo "home: $HOME, dir: ${PWD##*/}, now: $(date -I), $1 $@"`, tokensOf(
			Function, "echo", Plain, " ", String, `"home: `, Variable, "$HOME", String, ", dir: ", Variable, "${PWD##*/}",
			String, ", now: ", Variable, "$(date -I)", String, ", ", Variable, "$1", Plain, " ", Variable, "$@", String, `"`)},
		{`echo "$(printf "%s" ")")" end`, tokensOf(
			Function, "echo", Plain, " ", String, `"`, Variable, `$(printf "%s" ")")`, String, `"`, Plain, " end")},
		{`echo 'no $expansion here' $'tab\t' "it's" " "`, tokensOf(
			Function, "echo", Plain, " ", String, "'no $expansion here'", Plain, " ", String, `$'tab\t'`, Plain, " ",
			String, `"it's"`, Plain, " ", String, `" "`)},
		{`echo "open $x`, tokensOf(Function, "echo", Plain, " ", String, `"open `, Variable, "$x")},
		{`export PATH="$HOME/bin:$PATH"`, tokensOf(
			Function, "export", Plain, " ", Variable, "PATH", Operator, "=", String, `"`, Variable, "$HOME",
			String, "/bin:", Variable, "$PATH", String, `"`)},
		{"cat file | grep -v x && echo ok || echo no; sleep 2 &", tokensOf(
			Function, "cat", Plain, " file ", Operator, "|", Plain, " ", Function, "grep", Plain, " ", Attribute, "-v",
			Plain, " x ", Operator, "&&", Plain, " ", Function, "echo", Plain, " ok ", Operator, "||", Plain, " ",
			Function, "echo", Plain, " no", Operator, ";", Plain, " ", Function, "sleep", Plain, " ", Number, "2",
			Plain, " ", Operator, "&")},
		// Redirections: a descriptor is a number, the target is not a command.
		{"make 2>&1 >>build.log < input.txt", tokensOf(
			Function, "make", Plain, " ", Number, "2", Operator, ">&", Number, "1", Plain, " ", Operator, ">>",
			Plain, "build.log ", Operator, "<", Plain, " input.txt")},
		// A redirection may come first: the descriptor is a number, the command is still to come.
		{"2>err.log make all", tokensOf(
			Number, "2", Operator, ">", Plain, "err.log ", Function, "make", Plain, " all")},
		{`read -r a b <<< "$line"`, tokensOf(
			Function, "read", Plain, " ", Attribute, "-r", Plain, " a b ", Operator, "<<<", Plain, " ",
			String, `"`, Variable, "$line", String, `"`)},
		// Heredocs: the body is a string through the line that is the word.
		{"cat <<EOF\nhello $USER\nEOF\necho after", tokensOf(
			Function, "cat", Plain, " ", Operator, "<<", String, "EOF", Plain, "\n",
			String, "hello ", Variable, "$USER", String, "\nEOF", Plain, "\n", Function, "echo", Plain, " after")},
		{"cat <<-'RAW' >out\n\t$NOT expanded\n\tRAW\nls", tokensOf(
			Function, "cat", Plain, " ", Operator, "<<-", String, "'RAW'", Plain, " ", Operator, ">", Plain, "out\n",
			String, "\t$NOT expanded\n\tRAW", Plain, "\n", Function, "ls")},
		{"paste <<A <<B\na1\nA\nb1\nB\nwc", tokensOf(
			Function, "paste", Plain, " ", Operator, "<<", String, "A", Plain, " ", Operator, "<<", String, "B",
			Plain, "\n", String, "a1\nA", Plain, "\n", String, "b1\nB", Plain, "\n", Function, "wc")},
		{"cat <<END\nline one\nline two", tokensOf(
			Function, "cat", Plain, " ", Operator, "<<", String, "END", Plain, "\n", String, "line one\nline two")},
		{"cat <<EOF\r\nbody\r\nEOF\r\n", tokensOf(
			Function, "cat", Plain, " ", Operator, "<<", String, "EOF", Plain, "\r\n", String, "body\r\nEOF", Plain, "\r\n")},
		// Loops, the loop variable, and in only where it belongs.
		{`for file in *.go; do echo "$file"; done`, tokensOf(
			Keyword, "for", Plain, " ", Variable, "file", Plain, " ", Keyword, "in", Plain, " *.go", Operator, ";",
			Plain, " ", Keyword, "do", Plain, " ", Function, "echo", Plain, " ", String, `"`, Variable, "$file",
			String, `"`, Operator, ";", Plain, " ", Keyword, "done")},
		{"case \"$1\" in\n  start|up) run ;;\n  *) exit 2 ;;\nesac", tokensOf(
			Keyword, "case", Plain, " ", String, `"`, Variable, "$1", String, `"`, Plain, " ", Keyword, "in",
			Plain, "\n  start", Operator, "|", Plain, "up", Punctuation, ")", Plain, " ", Function, "run", Plain, " ",
			Operator, ";;", Plain, "\n  *", Punctuation, ")", Plain, " ", Function, "exit", Plain, " ", Number, "2",
			Plain, " ", Operator, ";;", Plain, "\n", Keyword, "esac")},
		// Arithmetic: names are variables, symbols operators.
		{"count=$((count + 0x1F * 2))", tokensOf(
			Variable, "count", Operator, "=", Punctuation, "$((", Variable, "count", Plain, " ", Operator, "+",
			Plain, " ", Number, "0x1F", Plain, " ", Operator, "*", Plain, " ", Number, "2", Punctuation, "))")},
		{"for ((i = 0; i < 3; i++)); do :; done", tokensOf(
			Keyword, "for", Plain, " ", Punctuation, "((", Variable, "i", Plain, " ", Operator, "=", Plain, " ",
			Number, "0", Punctuation, ";", Plain, " ", Variable, "i", Plain, " ", Operator, "<", Plain, " ",
			Number, "3", Punctuation, ";", Plain, " ", Variable, "i", Operator, "++", Punctuation, "))",
			Operator, ";", Plain, " ", Keyword, "do", Plain, " ", Function, ":", Operator, ";", Plain, " ", Keyword, "done")},
		// Functions, groups, declarations.
		{"log() {\n  local level=$1; shift\n}", tokensOf(
			Function, "log", Punctuation, "()", Plain, " ", Punctuation, "{", Plain, "\n  ", Function, "local",
			Plain, " ", Variable, "level", Operator, "=", Variable, "$1", Operator, ";", Plain, " ", Function, "shift",
			Plain, "\n", Punctuation, "}")},
		{"function deploy {", tokensOf(Keyword, "function", Plain, " ", Function, "deploy", Plain, " ", Punctuation, "{")},
		{"(cd /tmp && ls) | wc -l", tokensOf(
			Punctuation, "(", Function, "cd", Plain, " /tmp ", Operator, "&&", Plain, " ", Function, "ls",
			Punctuation, ")", Plain, " ", Operator, "|", Plain, " ", Function, "wc", Plain, " ", Attribute, "-l")},
		{"if ! grep -q x f; then :; fi", tokensOf(
			Keyword, "if", Plain, " ", Operator, "!", Plain, " ", Function, "grep", Plain, " ", Attribute, "-q",
			Plain, " x f", Operator, ";", Plain, " ", Keyword, "then", Plain, " ", Function, ":", Operator, ";",
			Plain, " ", Keyword, "fi")},
		// Inside [[ ]] && and < are tests, not command syntax.
		{`[[ $line =~ ^# && -n "$x" ]] && continue`, tokensOf(
			Keyword, "[[", Plain, " ", Variable, "$line", Plain, " =~ ^# ", Operator, "&&", Plain, " ",
			Attribute, "-n", Plain, " ", String, `"`, Variable, "$x", String, `"`, Plain, " ", Keyword, "]]",
			Plain, " ", Operator, "&&", Plain, " ", Function, "continue")},
		{"files=(a.txt \"b c\" $d) && echo ${#files[@]}", tokensOf(
			Variable, "files", Operator, "=", Punctuation, "(", Plain, "a.txt ", String, `"b c"`, Plain, " ",
			Variable, "$d", Punctuation, ")", Plain, " ", Operator, "&&", Plain, " ", Function, "echo", Plain, " ",
			Variable, "${#files[@]}")},
		{"diff <(sort a) <(sort b)", tokensOf(
			Function, "diff", Plain, " ", Punctuation, "<(", Function, "sort", Plain, " a", Punctuation, ")", Plain, " ",
			Punctuation, "<(", Function, "sort", Plain, " b", Punctuation, ")")},
		// An escape, backticks and a line continuation.
		{"echo \\$HOME `whoami` \\\n  --flag", tokensOf(
			Function, "echo", Plain, " \\$HOME ", Variable, "`whoami`", Plain, " \\\n  ", Attribute, "--flag")},
		{"echo hi\r\nls -l\r\n", tokensOf(Function, "echo", Plain, " hi\r\n", Function, "ls", Plain, " ", Attribute, "-l", Plain, "\r\n")},
	})
}

// The sample reads as a script: a few tokens from each part of it.
func TestBashSample(t *testing.T) {
	samples := fixturesFor(t, "bash")
	if len(samples) == 0 {
		t.Fatal("testdata/bash/ has no sample")
	}
	checkContains(t, "bash", []tokenCase{{samples[0].source, tokensOf(
		Comment, "#!/usr/bin/env bash",
		Function, "set", Attribute, "-euo",
		Variable, "IFS", Operator, "=", String, `$'\n\t'`,
		Function, "readonly", Variable, "SITE", Operator, "=", String, `"`, Variable, "${1:-kitwork.io}",
		Function, "log", Punctuation, "()",
		Function, "printf", String, "'[%s] %s %s\\n'",
		Operator, "<<-", String, "USAGE",
		Keyword, "while", Function, "getopts",
		Keyword, "case", Keyword, "in", Keyword, "esac",
		Function, "shift", Punctuation, "$((", Variable, "OPTIND", Operator, "-", Number, "1", Punctuation, "))",
		Keyword, "elif",
		Keyword, "for", Variable, "file", Keyword, "in",
		Operator, "<<", String, "EOF",
		Variable, "$(git rev-parse --short HEAD 2>/dev/null || echo unknown)",
		String, "'RAW'",
		Keyword, "done", Operator, "<",
		Function, "mapfile", Attribute, "-t", Plain, " releases ", Operator, "<", Plain, " ", Punctuation, "<(", Function, "ls",
		Keyword, "select", Variable, "choice", Keyword, "in",
		Function, "exit", Plain, " ", Number, "0",
	)}})
}
