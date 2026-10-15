package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"@@ -1,2 +1,2 @@\n", "@@ -0,0 +0,0 @@", "@@ -99999999999999999999,1 +1 @@\n", "@@@ -1 -1 +1 @@@",
		"--- a\n+++ b\n", "\\ No newline at end of file", "-- \n", "---\n", "Index: ", "@@ -1, +1 @@",
	)
}

// + and - lines, the headers around them, and the hunk's counts deciding
// what a line that starts with --- is.
func TestDiff(t *testing.T) {
	checkExact(t, "diff", []tokenCase{
		{"", nil},
		{"hello world", tokensOf(Plain, "hello world")},
		{"+added\n-removed\n context", tokensOf(
			Inserted, "+added", Plain, "\n", Deleted, "-removed", Plain, "\n context")},
		{"diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x", tokensOf(
			Heading, "diff --git a/x b/x", Plain, "\n", Heading, "index 1..2 100644", Plain, "\n",
			Heading, "--- a/x", Plain, "\n", Heading, "+++ b/x")},
		{"@@ -1,2 +1,2 @@ func main() {", tokensOf(Heading, "@@ -1,2 +1,2 @@ func main() {")},
		// Inside a hunk the counts decide: --- and +++ here are a removed and an added line.
		{"@@ -1,2 +1,2 @@\n--- old\n+++ new\n same", tokensOf(
			Heading, "@@ -1,2 +1,2 @@", Plain, "\n", Deleted, "--- old", Plain, "\n", Inserted, "+++ new",
			Plain, "\n same")},
		// Once the counts run out, --- starts the next file again.
		{"@@ -1 +1 @@\n-a\n+b\n--- a/next\n+++ b/next", tokensOf(
			Heading, "@@ -1 +1 @@", Plain, "\n", Deleted, "-a", Plain, "\n", Inserted, "+b", Plain, "\n",
			Heading, "--- a/next", Plain, "\n", Heading, "+++ b/next")},
		{"@@ -0,0 +1,2 @@\n+a\n+--- b\n--- c", tokensOf(
			Heading, "@@ -0,0 +1,2 @@", Plain, "\n", Inserted, "+a", Plain, "\n", Inserted, "+--- b", Plain, "\n",
			Heading, "--- c")},
		// An empty line is a context line whose blank was trimmed: it counts.
		{"@@ -1,2 +1,2 @@\n\n-x\n+y\n--- after", tokensOf(
			Heading, "@@ -1,2 +1,2 @@", Plain, "\n\n", Deleted, "-x", Plain, "\n", Inserted, "+y", Plain, "\n",
			Heading, "--- after")},
		{"-a\n\\ No newline at end of file\n+b", tokensOf(
			Deleted, "-a", Plain, "\n", Comment, "\\ No newline at end of file", Plain, "\n", Inserted, "+b")},
		{"@@ -1 +1 @@\n-0.9.0\n\\ No newline at end of file\n+1.0.0", tokensOf(
			Heading, "@@ -1 +1 @@", Plain, "\n", Deleted, "-0.9.0", Plain, "\n", Comment, "\\ No newline at end of file",
			Plain, "\n", Inserted, "+1.0.0")},
		{"new file mode 100644\nrename from a\nrename to b\nsimilarity index 90%\nBinary files a/x and b/x differ", tokensOf(
			Heading, "new file mode 100644", Plain, "\n", Heading, "rename from a", Plain, "\n", Heading, "rename to b",
			Plain, "\n", Heading, "similarity index 90%", Plain, "\n", Heading, "Binary files a/x and b/x differ")},
		// A fragment with no headers is still coloured.
		{"  keep\n+new\n-old", tokensOf(Plain, "  keep\n", Inserted, "+new", Plain, "\n", Deleted, "-old")},
		// A header without counts (a combined diff): the first characters decide.
		{"@@@ -1,2 -1,2 +1,3 @@@\n- a\n++b", tokensOf(
			Heading, "@@@ -1,2 -1,2 +1,3 @@@", Plain, "\n", Deleted, "- a", Plain, "\n", Inserted, "++b")},
		// git format-patch: --- ends the message, "-- " opens the signature.
		{"Subject: x\n---\n file | 2 +-\n-- \n2.46.0", tokensOf(
			Plain, "Subject: x\n", Heading, "---", Plain, "\n file | 2 +-\n-- \n2.46.0")},
		{"Index: a.go\n=====", tokensOf(Heading, "Index: a.go", Plain, "\n", Heading, "=====")},
		{"-a\r\n+b\r\n", tokensOf(Deleted, "-a", Plain, "\r\n", Inserted, "+b", Plain, "\r\n")},
		{"+  \n+\n", tokensOf(Inserted, "+  ", Plain, "\n", Inserted, "+", Plain, "\n")},
	})
}

// The sample reads as a patch: a few lines from each file in it.
func TestDiffSample(t *testing.T) {
	samples := fixturesFor(t, "diff")
	if len(samples) == 0 {
		t.Fatal("testdata/diff/ has no sample")
	}
	checkContains(t, "diff", []tokenCase{{samples[0].source, tokensOf(
		Heading, "---",
		Heading, "diff --git a/engine/jit/highlight/diff.go b/engine/jit/highlight/diff.go",
		Heading, "@@ -1,12 +1,15 @@",
		Deleted, "-import \"strings\"", Plain, "\n", Inserted, "+import (",
		Heading, "@@ -30,8 +33,9 @@ var diffHeaders = []string{\"diff \", \"index \", \"--- \", \"+++ \"}",
		Deleted, "--- a comment that was a line of its own", Plain, "\n", Inserted, "+-- tổng tiền tính bằng đồng",
		Heading, "deleted file mode 100644", Heading, "+++ /dev/null",
		Deleted, "-}",
		Heading, "rename to docs/kitwork-ui.md",
		Inserted, "+Tên ngôn ngữ viết đầy đủ: `javascript`, không `js`.",
		Heading, "new mode 100755",
		Inserted, "+    steps: [{run: go test ./...}]",
		Heading, "Binary files a/docs/logo.png and b/docs/logo.png differ",
		Deleted, "-0.9.0", Plain, "\n", Comment, "\\ No newline at end of file",
		Plain, "\n-- \n2.46.0\n",
	)}})
}
