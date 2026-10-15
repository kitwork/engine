package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"@\"\n", "@'\n", "\n\"@", "\n'@", "$(", "${", "@{", "[int]", "[", "::", "-eq", "-Name", "--x", "`",
		"$env:", "2>&1", "& ", ". ", "} else {", "function f {", `"$(""`, "''''", "<#", "#>",
	)
}

// A statement that starts with a name is a command: -Name is a parameter. Any
// other statement is an expression: -eq is an operator.
func TestPowerShell(t *testing.T) {
	checkExact(t, "powershell", []tokenCase{
		{"", nil},
		{"Get-ChildItem -Path . -Recurse", tokensOf(
			Function, "Get-ChildItem", Plain, " ", Attribute, "-Path", Plain, " . ", Attribute, "-Recurse")},
		{"# comment\nWrite-Host hi", tokensOf(Comment, "# comment", Plain, "\n", Function, "Write-Host", Plain, " hi")},
		{"<# block\n comment #>\n$x = 1", tokensOf(
			Comment, "<# block\n comment #>", Plain, "\n", Variable, "$x", Plain, " ", Operator, "=", Plain, " ", Number, "1")},
		{"<# open", tokensOf(Comment, "<# open")},
		{"$env:PATH; $_; ${my var}; $true; $NULL", tokensOf(
			Variable, "$env:PATH", Punctuation, ";", Plain, " ", Variable, "$_", Punctuation, ";", Plain, " ",
			Variable, "${my var}", Punctuation, ";", Plain, " ", Constant, "$true", Punctuation, ";", Plain, " ",
			Constant, "$NULL")},
		{"if ($a -eq 1 -and -not $b) { Get-Item x -Force }", tokensOf(
			Keyword, "if", Plain, " ", Punctuation, "(", Variable, "$a", Plain, " ", Operator, "-eq", Plain, " ",
			Number, "1", Plain, " ", Operator, "-and", Plain, " ", Operator, "-not", Plain, " ", Variable, "$b",
			Punctuation, ")", Plain, " ", Punctuation, "{", Plain, " ", Function, "Get-Item", Plain, " x ",
			Attribute, "-Force", Plain, " ", Punctuation, "}")},
		// Case does not matter to keywords and command names.
		{"IF ($x) { get-childitem }", tokensOf(
			Keyword, "IF", Plain, " ", Punctuation, "(", Variable, "$x", Punctuation, ")", Plain, " ",
			Punctuation, "{", Plain, " ", Function, "get-childitem", Plain, " ", Punctuation, "}")},
		// Strings: '' inside '…', `$ and "" inside "…", expansions as variables.
		{"'it''s $not' + \"a $b `$c \"\"q\"\" $($d.e)\"", tokensOf(
			String, "'it''s $not'", Plain, " ", Operator, "+", Plain, " ", String, `"a `, Variable, "$b",
			String, " `$c \"\"q\"\" ", Variable, "$($d.e)", String, `"`)},
		{`"open $x`, tokensOf(String, `"open `, Variable, "$x")},
		// '' does not end the key: the = after the whole string makes it one.
		{"@{ 'it''s' = 1 }", tokensOf(
			Punctuation, "@{", Plain, " ", Property, "'it''s'", Plain, " ", Operator, "=", Plain, " ", Number, "1",
			Plain, " ", Punctuation, "}")},
		{"@\"\nHi $name\n\"@ | Out-File a.txt", tokensOf(
			String, "@\"\nHi ", Variable, "$name", String, "\n\"@", Plain, " ", Operator, "|", Plain, " ",
			Function, "Out-File", Plain, " a.txt")},
		{"@'\n$not $(here)\n'@", tokensOf(String, "@'\n$not $(here)\n'@")},
		{`@"x"`, tokensOf(Operator, "@", String, `"x"`)},
		// Types, static members, methods and properties.
		{"[System.IO.Path]::GetTempPath().Length", tokensOf(
			Punctuation, "[", Type, "System.IO.Path", Punctuation, "]", Operator, "::", Function, "GetTempPath",
			Punctuation, "().", Property, "Length")},
		// An attribute's arguments are properties; a [ right after a type literal
		// opens another, a [ right after an index indexes again.
		{"[Parameter(Mandatory, Position = 0)][string]$Name", tokensOf(
			Punctuation, "[", Type, "Parameter", Punctuation, "(", Property, "Mandatory", Punctuation, ",", Plain, " ",
			Property, "Position", Plain, " ", Operator, "=", Plain, " ", Number, "0", Punctuation, ")][", Type, "string",
			Punctuation, "]", Variable, "$Name")},
		{"$grid[0][1]", tokensOf(
			Variable, "$grid", Punctuation, "[", Number, "0", Punctuation, "][", Number, "1", Punctuation, "]")},
		{"class Point { [int]$X }", tokensOf(
			Keyword, "class", Plain, " ", Type, "Point", Plain, " ", Punctuation, "{", Plain, " ", Punctuation, "[",
			Type, "int", Punctuation, "]", Variable, "$X", Plain, " ", Punctuation, "}")},
		{"@{ Name = 'x'; Count = 2 }", tokensOf(
			Punctuation, "@{", Plain, " ", Property, "Name", Plain, " ", Operator, "=", Plain, " ", String, "'x'",
			Punctuation, ";", Plain, " ", Property, "Count", Plain, " ", Operator, "=", Plain, " ", Number, "2",
			Plain, " ", Punctuation, "}")},
		{"$params = @{ Path = '.' }; Get-ChildItem @params; $a = @(1, 2)[0]", tokensOf(
			Variable, "$params", Plain, " ", Operator, "=", Plain, " ", Punctuation, "@{", Plain, " ", Property, "Path",
			Plain, " ", Operator, "=", Plain, " ", String, "'.'", Plain, " ", Punctuation, "};", Plain, " ",
			Function, "Get-ChildItem", Plain, " ", Variable, "@params", Punctuation, ";", Plain, " ", Variable, "$a",
			Plain, " ", Operator, "=", Plain, " ", Punctuation, "@(", Number, "1", Punctuation, ",", Plain, " ",
			Number, "2", Punctuation, ")[", Number, "0", Punctuation, "]")},
		{"Get-Process | Where-Object { $_.CPU -gt 10 } | Sort-Object CPU", tokensOf(
			Function, "Get-Process", Plain, " ", Operator, "|", Plain, " ", Function, "Where-Object", Plain, " ",
			Punctuation, "{", Plain, " ", Variable, "$_", Punctuation, ".", Property, "CPU", Plain, " ",
			Operator, "-gt", Plain, " ", Number, "10", Plain, " ", Punctuation, "}", Plain, " ", Operator, "|",
			Plain, " ", Function, "Sort-Object", Plain, " CPU")},
		{"function Get-Thing {\n  param([int]$Count = 3)\n}", tokensOf(
			Keyword, "function", Plain, " ", Function, "Get-Thing", Plain, " ", Punctuation, "{", Plain, "\n  ",
			Keyword, "param", Punctuation, "([", Type, "int", Punctuation, "]", Variable, "$Count", Plain, " ",
			Operator, "=", Plain, " ", Number, "3", Punctuation, ")", Plain, "\n", Punctuation, "}")},
		{"try { throw 'x' } catch [System.Exception] { $_ } finally { }", tokensOf(
			Keyword, "try", Plain, " ", Punctuation, "{", Plain, " ", Keyword, "throw", Plain, " ", String, "'x'",
			Plain, " ", Punctuation, "}", Plain, " ", Keyword, "catch", Plain, " ", Punctuation, "[",
			Type, "System.Exception", Punctuation, "]", Plain, " ", Punctuation, "{", Plain, " ", Variable, "$_",
			Plain, " ", Punctuation, "}", Plain, " ", Keyword, "finally", Plain, " ", Punctuation, "{", Plain, " ",
			Punctuation, "}")},
		{"foreach ($item in $list) { $item }", tokensOf(
			Keyword, "foreach", Plain, " ", Punctuation, "(", Variable, "$item", Plain, " ", Keyword, "in", Plain, " ",
			Variable, "$list", Punctuation, ")", Plain, " ", Punctuation, "{", Plain, " ", Variable, "$item", Plain, " ",
			Punctuation, "}")},
		// A Verb-Noun is a command anywhere; a culture name is not.
		{"Set-Culture en-US; Write-Output Get-Date", tokensOf(
			Function, "Set-Culture", Plain, " en-US", Punctuation, ";", Plain, " ", Function, "Write-Output",
			Plain, " ", Function, "Get-Date")},
		// The call operator: the string is the command, the rest its arguments.
		{`& "C:\tools\app.exe" --dry-run 2>&1 > log.txt`, tokensOf(
			Operator, "&", Plain, " ", String, `"C:\tools\app.exe"`, Plain, " ", Attribute, "--dry-run", Plain, " ",
			Operator, "2>&1", Plain, " ", Operator, ">", Plain, " log.txt")},
		{". .\\lib.ps1; 1..3 | % { $_ * 2 }", tokensOf(
			Operator, ".", Plain, " ", Function, ".\\lib.ps1", Punctuation, ";", Plain, " ", Number, "1",
			Operator, "..", Number, "3", Plain, " ", Operator, "|", Plain, " ", Function, "%", Plain, " ",
			Punctuation, "{", Plain, " ", Variable, "$_", Plain, " ", Operator, "*", Plain, " ", Number, "2",
			Plain, " ", Punctuation, "}")},
		{"$size = 1MB + 0x1F - 2.5e3 + .5", tokensOf(
			Variable, "$size", Plain, " ", Operator, "=", Plain, " ", Number, "1MB", Plain, " ", Operator, "+",
			Plain, " ", Number, "0x1F", Plain, " ", Operator, "-", Plain, " ", Number, "2.5e3", Plain, " ",
			Operator, "+", Plain, " ", Number, ".5")},
		// A backtick at the end of a line continues the command.
		{"Get-Item x `\r\n  -Force\r\n", tokensOf(
			Function, "Get-Item", Plain, " x `\r\n  ", Attribute, "-Force", Plain, "\r\n")},
	})
}

// The sample reads as a script: a few tokens from each part of it.
func TestPowerShellSample(t *testing.T) {
	samples := fixturesFor(t, "powershell")
	if len(samples) == 0 {
		t.Fatal("testdata/powershell/ has no sample")
	}
	checkContains(t, "powershell", []tokenCase{{samples[0].source, tokensOf(
		Type, "CmdletBinding", Property, "SupportsShouldProcess", Operator, "=", Constant, "$true",
		Keyword, "param",
		Type, "ValidateRange", Number, "1024",
		Type, "string", Punctuation, "[]]", Variable, "$Sites",
		Function, "Set-StrictMode", Attribute, "-Version",
		Keyword, "function", Function, "Test-Port",
		Type, "System.Net.Sockets.TcpListener", Operator, "::", Function, "new",
		Keyword, "catch", Type, "System.Net.Sockets.SocketException",
		Keyword, "elseif", Variable, "$go", Punctuation, ".", Property, "Version",
		Variable, "$($go.Version)",
		Operator, "&", Function, "go", Operator, "-replace",
		Variable, "$(Get-Date -Format 'yyyy-MM-dd HH:mm')",
		String, "@'\nKhông mở rộng: $Port, $(Get-Date) và `n giữ nguyên.\n'@",
		Type, "pscustomobject", Punctuation, "]@{", Property, "Identity",
		Keyword, "for", Operator, "-lt",
		Keyword, "switch", Attribute, "-Wildcard",
		Keyword, "default",
		Number, "1KB",
		Operator, "2>&1",
		Variable, "$env:ProgramFiles", Attribute, "-C", Attribute, "--short",
		Operator, "2>", Constant, "$null",
		Function, "Start-Process", Attribute, "-NoNewWindow",
	)}})
}
