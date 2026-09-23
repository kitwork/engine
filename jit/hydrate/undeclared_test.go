package hydrate

import (
	"strings"
	"testing"
)

// The pass has exactly one job: say when an expression reads a name nothing declares. Every case
// below comes in a pair — the mistake, and the same page one edit away from being correct — so a
// case cannot pass because the scanner reported nothing at all.
func TestUndeclaredNames(t *testing.T) {
	cases := []struct {
		name string
		html string
		want []string // names warned about, in order
	}{
		{
			name: "a typo in the boundary that declares the state",
			html: `<body><div data-kit-scope="{ darkMode: false }"><span data-kit-show="dark">…</span></div></body>`,
			want: []string{"dark"},
		},
		{
			name: "control: the same page spelling the name right",
			html: `<body><div data-kit-scope="{ darkMode: false }"><span data-kit-show="darkMode">…</span></div></body>`,
		},
		{
			name: "a nested boundary reads a name the outer one declares",
			html: `<body><div data-kit-scope="{ open: false }"><div data-kit-scope="{ row: 1 }">` +
				`<span data-kit-show="open">…</span></div></div></body>`,
		},
		{
			name: "control: the nested boundary reads a name neither declares",
			html: `<body><div data-kit-scope="{ open: false }"><div data-kit-scope="{ row: 1 }">` +
				`<span data-kit-show="shut">…</span></div></div></body>`,
			want: []string{"shut"},
		},
		{
			// The real page that made this pass wrong once: a page-scope model above every boundary,
			// read from inside one. The kernel falls through to the page scope, so the scan must too.
			name: "page scope declared by a model outside every boundary",
			html: `<body><input data-kit-model="query">` +
				`<div data-kit-scope="{ title: 'a' }"><span data-kit-show="query">…</span></div></body>`,
		},
		{
			name: "control: the page scope declares one name and the page reads another",
			html: `<body><input data-kit-model="query">` +
				`<div data-kit-scope="{ title: 'a' }"><span data-kit-show="search">…</span></div></body>`,
			want: []string{"search"},
		},
		{
			// A component's state lives in JavaScript, so the markup cannot prove anything about it.
			name: "a component boundary hides its state on purpose",
			html: `<body><div data-kit-component="tabs"><span data-kit-show="current">…</span></div></body>`,
		},
		{
			name: "control: the same markup with a scope instead of a component",
			html: `<body><div data-kit-scope="{ other: 1 }"><span data-kit-show="current">…</span></div></body>`,
			want: []string{"current"},
		},
		{
			name: "a page that writes its own page scope from a script",
			html: `<body><span data-kit-show="ready">…</span><script>kit.set("ready", true)</script></body>`,
		},
		{
			name: "control: the same page without that script",
			html: `<body><span data-kit-show="ready">…</span></body>`,
			want: []string{"ready"},
		},
		{
			name: "a fragment, where every boundary above it is missing",
			html: `<div data-kit-scope="{ a: 1 }"><span data-kit-show="b">…</span></div>`,
		},
		{
			name: "control: the same fragment inside the document it belongs to",
			html: `<body><div data-kit-scope="{ a: 1 }"><span data-kit-show="b">…</span></div></body>`,
			want: []string{"b"},
		},
		{
			name: "for, model and seed declare the names they write",
			html: `<body><div data-kit-scope="{ rows: [] }">` +
				`<li data-kit-for="row, index of rows" data-kit-text="row.title + index">…</li>` +
				`<input data-kit-model="draft"><span data-kit-seed="loaded" data-kit-show="draft && loaded">…</span>` +
				`</div></body>`,
		},
		{
			name: "control: a for that reads the item under the wrong name",
			html: `<body><div data-kit-scope="{ rows: [] }">` +
				`<li data-kit-for="row of rows" data-kit-text="item.title">…</li></div></body>`,
			want: []string{"item"},
		},
		{
			// Member names, object keys and lambda parameters are not scope lookups. Reporting them
			// would make the pass unusable on the expressions a real page writes.
			name: "members, keys and lambda parameters are not reads",
			html: `<body><div data-kit-scope="{ user: {}, rows: [] }">` +
				`<span data-kit-text="user.email"></span>` +
				`<span data-kit-text="rows.filter((row) =&gt; row.done).length"></span>` +
				`<span data-kit-bind:title="({ label: user.name }).label"></span></div></body>`,
		},
		{
			name: "control: the same expressions reading one name nothing declares",
			html: `<body><div data-kit-scope="{ user: {}, rows: [] }">` +
				`<span data-kit-text="rows.filter((row) =&gt; row.done).length + total"></span></div></body>`,
			want: []string{"total"},
		},
		{
			name: "a name declared further down the same boundary still counts",
			html: `<body><div data-kit-scope="{ }"><span data-kit-text="query"></span>` +
				`<input data-kit-model="query"></div></body>`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines := undeclaredWarnings(c.html)
			if len(lines) != len(c.want) {
				t.Fatalf("want %d warnings %v, got %d:\n%s", len(c.want), c.want, len(lines), strings.Join(lines, "\n"))
			}
			for i, name := range c.want {
				if !strings.Contains(lines[i], `"`+name+`"`) {
					t.Fatalf("warning %d should name %q: %s", i, name, lines[i])
				}
			}
		})
	}
}

// The message has to carry the three things an author needs to find the mistake: the attribute, the
// name, and enough of the tag to locate it on the page.
func TestUndeclaredNameMessage(t *testing.T) {
	lines := undeclaredWarnings(`<body><div data-kit-scope="{ darkMode: false }">` +
		`<span class="badge" data-kit-show="dark">…</span></div></body>`)
	if len(lines) != 1 {
		t.Fatalf("want one warning, got %d", len(lines))
	}
	for _, part := range []string{"data-kit-show", `"dark"`, "reads as 0", `class="badge"`} {
		if !strings.Contains(lines[0], part) {
			t.Fatalf("message should contain %q: %s", part, lines[0])
		}
	}
}

// Markup the scanner cannot read confidently silences the whole pass rather than guessing at it.
func TestUndeclaredNamesBailOut(t *testing.T) {
	bail := []struct{ name, html string }{
		{"an unterminated comment", `<body><!-- <div data-kit-scope="{ a: 1 }"><span data-kit-show="b">`},
		{"a tag that never closes", `<body><div data-kit-scope="{ a: 1 }"><span data-kit-show="b"`},
		{"a script that never closes", `<body><span data-kit-show="b">…</span><script>var x = 1;`},
	}
	for _, c := range bail {
		t.Run(c.name, func(t *testing.T) {
			if lines := undeclaredWarnings(c.html); lines != nil {
				t.Fatalf("unreadable markup should report nothing, got:\n%s", strings.Join(lines, "\n"))
			}
		})
	}
	// …and the same markup, closed, does report.
	if lines := undeclaredWarnings(`<body><!-- a note --><div data-kit-scope="{ a: 1 }"><span data-kit-show="b">…</span></div></body>`); len(lines) != 1 {
		t.Fatalf("control: want one warning once the comment is closed, got %d", len(lines))
	}
}
