package hydrate

import (
	"reflect"
	"strconv"
)

// Regular expressions, for match, matchAll and search, which read their argument as a pattern:
// 'a1b2'.match('\\d') is ["1"] in the browser, so it is here (16/10/2026; the server answered nothing).
// The grammar has no regular expression literal, so a pattern always comes as text, with no flags but
// the g that matchAll adds. This is ECMAScript's backtracking matcher over UTF-16 code units, with the
// syntax JavaScript accepts outside the u flag (Annex B: a { that starts no quantifier is a character,
// \8 is "8", \1 with no first group the character U+0001): captures, named groups, lookahead and
// lookbehind, backreferences, classes. Past regexpStepLimit steps a match fails with a RangeError, where
// the browser would go on as long as it takes.

const regexpStepLimit = 1000000

var errRegexpSteps = rangeError("Maximum regular expression backtracking exceeded")

// regexpMethod is match, matchAll and search with a pattern given as text (or anything, made text).
func (m *machine) regexpMethod(f *frame, name, s string, args []any) (any, error) {
	pattern := ""
	if a := arg(args, 0); !isUndefined(a) {
		text, err := m.toString(f, a)
		if err != nil {
			return nil, err
		}
		pattern = text
	}
	re, err := compileRegexp(pattern)
	if err != nil {
		return nil, err
	}
	input := units(s)
	switch name {
	case "search":
		caps, err := re.exec(input, 0)
		if err != nil {
			return nil, err
		}
		if caps == nil {
			return float64(-1), nil
		}
		return float64(caps[0]), nil
	case "match":
		caps, err := re.exec(input, 0)
		if err != nil {
			return nil, err
		}
		if caps == nil {
			return nil, nil
		}
		return m.matchArray(re, input, s, caps), nil
	}
	// matchAll: RegExp(pattern, "g"), read one match at a time
	last := 0
	return &iterator{tag: "RegExp String Iterator", next: func(m *machine, f *frame) (any, bool, error) {
		if last > len(input) {
			return nil, true, nil
		}
		caps, err := re.exec(input, last)
		if err != nil {
			return nil, true, err
		}
		if caps == nil {
			last = len(input) + 1
			return nil, true, nil
		}
		last = caps[1]
		if caps[1] == caps[0] {
			last++
		}
		return m.matchArray(re, input, s, caps), false, nil
	}}, nil
}

// matchArray is what exec gives: the match and each capture (undefined where a group took no part),
// with index, input and groups beside the items.
func (m *machine) matchArray(re *regexpProgram, input []uint16, s string, caps []int) []any {
	out := newList(len(caps) / 2)
	for i := range out {
		if caps[2*i] < 0 {
			out[i] = Undefined
		} else {
			out[i] = fromUnits(input[caps[2*i]:caps[2*i+1]])
		}
	}
	var groups any = Undefined
	if len(re.names) > 0 {
		g := newObject(len(re.names))
		g.bare = true
		for _, n := range re.names {
			g.set(n.name, out[n.index])
		}
		groups = g
	}
	m.setExtra(out, "index", float64(caps[0]))
	m.setExtra(out, "input", s)
	m.setExtra(out, "groups", groups)
	return out
}

// ---- the program ----

type groupName struct {
	name  string
	index int
}

type regexpProgram struct {
	root   rnode
	groups int // capturing groups, the whole match not counted
	names  []groupName
}

// rnode matches at pos in direction dir (1 forward, -1 backward, inside a lookbehind) and calls k with
// where it ended; true when k succeeded.
type rnode func(st *rstate, pos, dir int, k func(int) bool) bool

type rstate struct {
	in    []uint16
	caps  []int
	steps int
	over  bool
}

func (st *rstate) step() bool {
	st.steps++
	if st.steps > regexpStepLimit {
		st.over = true
	}
	return !st.over
}

// exec finds the first match at or after from: the captures (start and end of the match first), or nil.
func (re *regexpProgram) exec(input []uint16, from int) ([]int, error) {
	st := &rstate{in: input, caps: make([]int, 2*(re.groups+1))}
	for start := from; start <= len(input); start++ {
		for i := range st.caps {
			st.caps[i] = -1
		}
		end := -1
		if re.root(st, start, 1, func(e int) bool { end = e; return true }) {
			st.caps[0], st.caps[1] = start, end
			return append([]int(nil), st.caps...), nil
		}
		if st.over {
			return nil, errRegexpSteps
		}
	}
	return nil, nil
}

// ---- the parser ----

type rparser struct {
	src     []uint16
	at      int
	groups  int
	total   int  // every capturing group in the pattern, which \N is read against
	named   bool // whether the pattern names a group, which makes \k a backreference
	names   []groupName
	pending []namedRef
}

type namedRef struct {
	name string
	set  func(int)
}

func regexpError(message string) error {
	return syntaxError("Invalid regular expression: " + message)
}

// compileRegexp reads a pattern as RegExp(pattern) does.
func compileRegexp(pattern string) (*regexpProgram, error) {
	p := &rparser{src: units(pattern)}
	p.total, p.named = p.countGroups()
	root, err := p.disjunction()
	if err != nil {
		return nil, err
	}
	if p.at < len(p.src) {
		if p.src[p.at] == ')' {
			return nil, regexpError("Unmatched ')'")
		}
		return nil, regexpError("Unexpected character")
	}
	for _, ref := range p.pending {
		found := false
		for _, n := range p.names {
			if n.name == ref.name {
				ref.set(n.index)
				found = true
				break
			}
		}
		if !found {
			return nil, regexpError("Invalid named capture referenced")
		}
	}
	return &regexpProgram{root: root, groups: p.groups, names: p.names}, nil
}

// countGroups counts the capturing groups before the pattern is read, and whether any is named.
func (p *rparser) countGroups() (int, bool) {
	n, named := 0, false
	inClass := false
	for i := 0; i < len(p.src); i++ {
		c := p.src[i]
		switch {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			if i+1 < len(p.src) && p.src[i+1] == '?' {
				if i+2 < len(p.src) && p.src[i+2] == '<' && i+3 < len(p.src) && p.src[i+3] != '=' && p.src[i+3] != '!' {
					n++
					named = true
				}
				continue
			}
			n++
		}
	}
	return n, named
}

func (p *rparser) peek() int {
	if p.at < len(p.src) {
		return int(p.src[p.at])
	}
	return -1
}

func (p *rparser) disjunction() (rnode, error) {
	var alts []rnode
	for {
		alt, err := p.alternative()
		if err != nil {
			return nil, err
		}
		alts = append(alts, alt)
		if p.peek() != '|' {
			break
		}
		p.at++
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return func(st *rstate, pos, dir int, k func(int) bool) bool {
		for _, a := range alts {
			if a(st, pos, dir, k) {
				return true
			}
			if st.over {
				return false
			}
		}
		return false
	}, nil
}

func (p *rparser) alternative() (rnode, error) {
	var terms []rnode
	for p.at < len(p.src) && p.peek() != '|' && p.peek() != ')' {
		t, err := p.term()
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	return sequence(terms), nil
}

// sequence matches its terms one after another: left to right forward, right to left backward.
func sequence(terms []rnode) rnode {
	switch len(terms) {
	case 0:
		return func(st *rstate, pos, dir int, k func(int) bool) bool { return k(pos) }
	case 1:
		return terms[0]
	}
	return func(st *rstate, pos, dir int, k func(int) bool) bool {
		var run func(i, pos int) bool
		run = func(i, pos int) bool {
			if i == len(terms) {
				return k(pos)
			}
			t := terms[i]
			if dir < 0 {
				t = terms[len(terms)-1-i]
			}
			return t(st, pos, dir, func(next int) bool { return run(i+1, next) })
		}
		return run(0, pos)
	}
}

func (p *rparser) term() (rnode, error) {
	c := p.peek()
	switch c {
	case '^':
		p.at++
		return func(st *rstate, pos, dir int, k func(int) bool) bool { return pos == 0 && k(pos) }, nil
	case '$':
		p.at++
		return func(st *rstate, pos, dir int, k func(int) bool) bool { return pos == len(st.in) && k(pos) }, nil
	case '\\':
		if p.at+1 < len(p.src) && (p.src[p.at+1] == 'b' || p.src[p.at+1] == 'B') {
			want := p.src[p.at+1] == 'b'
			p.at += 2
			return func(st *rstate, pos, dir int, k func(int) bool) bool {
				a := pos > 0 && wordUnit(st.in[pos-1])
				b := pos < len(st.in) && wordUnit(st.in[pos])
				return (a != b) == want && k(pos)
			}, nil
		}
	case '(':
		if p.at+2 < len(p.src) && p.src[p.at+1] == '?' {
			kind := p.src[p.at+2]
			behind := kind == '<' && p.at+3 < len(p.src) && (p.src[p.at+3] == '=' || p.src[p.at+3] == '!')
			if kind == '=' || kind == '!' || behind {
				negate := kind == '!' || behind && p.src[p.at+3] == '!'
				before := p.groups
				p.at += 3
				if behind {
					p.at++
				}
				body, err := p.disjunction()
				if err != nil {
					return nil, err
				}
				if p.peek() != ')' {
					return nil, regexpError("Unterminated group")
				}
				p.at++
				look := lookaround(body, !behind, negate)
				if behind {
					if q := p.peek(); q == '*' || q == '+' || q == '?' || q == '{' && p.braced() {
						return nil, regexpError("Nothing to repeat")
					}
					return look, nil
				}
				// a lookahead takes a quantifier outside the u flag (Annex B)
				return p.quantified(look, before, p.groups-before)
			}
		}
	case '*', '+', '?':
		return nil, regexpError("Nothing to repeat")
	case '{':
		if p.braced() {
			return nil, regexpError("Nothing to repeat")
		}
	}
	before := p.groups
	atom, err := p.atom()
	if err != nil {
		return nil, err
	}
	return p.quantified(atom, before, p.groups-before)
}

// braced reports whether a valid {n}, {n,} or {n,m} starts at p.at.
func (p *rparser) braced() bool {
	_, _, _, ok := p.readBraces()
	return ok
}

// readBraces reads {n}, {n,} or {n,m} at p.at without moving: min, max (-1 for none), its length.
func (p *rparser) readBraces() (int, int, int, bool) {
	i := p.at
	if i >= len(p.src) || p.src[i] != '{' {
		return 0, 0, 0, false
	}
	i++
	number := func() (int, bool) {
		start := i
		n := 0
		for i < len(p.src) && p.src[i] >= '0' && p.src[i] <= '9' {
			if n < 1<<30 {
				n = n*10 + int(p.src[i]-'0')
			}
			i++
		}
		return n, i > start
	}
	lo, ok := number()
	if !ok {
		return 0, 0, 0, false
	}
	hi := lo
	if i < len(p.src) && p.src[i] == ',' {
		i++
		if i < len(p.src) && p.src[i] == '}' {
			hi = -1
		} else {
			var ok bool
			hi, ok = number()
			if !ok {
				return 0, 0, 0, false
			}
		}
	}
	if i >= len(p.src) || p.src[i] != '}' {
		return 0, 0, 0, false
	}
	return lo, hi, i + 1 - p.at, true
}

// quantified reads the quantifier after an atom, if any; the groups the atom holds are captures
// first..first+count, cleared at each repetition.
func (p *rparser) quantified(atom rnode, first, count int) (rnode, error) {
	lo, hi := -1, -1
	switch p.peek() {
	case '*':
		lo, hi = 0, -1
		p.at++
	case '+':
		lo, hi = 1, -1
		p.at++
	case '?':
		lo, hi = 0, 1
		p.at++
	case '{':
		min, max, width, ok := p.readBraces()
		if !ok {
			return atom, nil
		}
		if max >= 0 && max < min {
			return nil, regexpError("numbers out of order in {} quantifier")
		}
		lo, hi = min, max
		p.at += width
	default:
		return atom, nil
	}
	greedy := true
	if p.peek() == '?' {
		greedy = false
		p.at++
	}
	return repeat(atom, lo, hi, greedy, first+1, count), nil
}

// repeat is RepeatMatcher: atom at least lo and at most hi (-1 unbounded) times, the captures it
// holds cleared before each try, and an iteration that matches nothing once lo is met ends it.
func repeat(atom rnode, lo, hi int, greedy bool, firstCap, count int) rnode {
	var rep func(st *rstate, pos, dir, lo, hi int, k func(int) bool) bool
	rep = func(st *rstate, pos, dir, lo, hi int, k func(int) bool) bool {
		if !st.step() {
			return false
		}
		if hi == 0 {
			return k(pos)
		}
		saved := make([]int, 2*count)
		copy(saved, st.caps[2*firstCap:2*(firstCap+count)])
		try := func() bool {
			for i := 2 * firstCap; i < 2*(firstCap+count); i++ {
				st.caps[i] = -1
			}
			ok := atom(st, pos, dir, func(next int) bool {
				if lo == 0 && next == pos {
					return false
				}
				nlo, nhi := lo, hi
				if nlo > 0 {
					nlo--
				}
				if nhi > 0 {
					nhi--
				}
				return rep(st, next, dir, nlo, nhi, k)
			})
			if !ok {
				copy(st.caps[2*firstCap:2*(firstCap+count)], saved)
			}
			return ok
		}
		if lo > 0 {
			return try()
		}
		if !greedy {
			if k(pos) {
				return true
			}
			if st.over {
				return false
			}
			return try()
		}
		if try() {
			return true
		}
		if st.over {
			return false
		}
		return k(pos)
	}
	return func(st *rstate, pos, dir int, k func(int) bool) bool {
		return rep(st, pos, dir, lo, hi, k)
	}
}

// lookaround is (?=…), (?!…), (?<=…) and (?<!…): the body matched where it stands, forward or backward,
// consuming nothing; a positive one keeps the captures it set, a negative one none.
func lookaround(body rnode, ahead, negate bool) rnode {
	dir := 1
	if !ahead {
		dir = -1
	}
	return func(st *rstate, pos, _ int, k func(int) bool) bool {
		saved := append([]int(nil), st.caps...)
		matched := body(st, pos, dir, func(int) bool { return true })
		if st.over {
			return false
		}
		if negate {
			copy(st.caps, saved)
			if matched {
				return false
			}
			return k(pos)
		}
		if !matched {
			copy(st.caps, saved)
			return false
		}
		if k(pos) {
			return true
		}
		copy(st.caps, saved)
		return false
	}
}

func (p *rparser) atom() (rnode, error) {
	c := p.peek()
	switch c {
	case '.':
		p.at++
		return unitMatcher(func(u uint16) bool { return u != '\n' && u != '\r' && u != 0x2028 && u != 0x2029 }), nil
	case '[':
		return p.class()
	case '(':
		p.at++
		capture := true
		name := ""
		if p.peek() == '?' {
			switch {
			case p.at+1 < len(p.src) && p.src[p.at+1] == ':':
				capture = false
				p.at += 2
			case p.at+1 < len(p.src) && p.src[p.at+1] == '<':
				p.at += 2
				n, err := p.groupName()
				if err != nil {
					return nil, err
				}
				for _, g := range p.names {
					if g.name == n {
						return nil, regexpError("Duplicate capture group name")
					}
				}
				name = n
			default:
				return nil, regexpError("Invalid group")
			}
		}
		index := 0
		if capture {
			p.groups++
			index = p.groups
			if name != "" {
				p.names = append(p.names, groupName{name, index})
			}
		}
		body, err := p.disjunction()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, regexpError("Unterminated group")
		}
		p.at++
		if !capture {
			return body, nil
		}
		return func(st *rstate, pos, dir int, k func(int) bool) bool {
			return body(st, pos, dir, func(end int) bool {
				oldS, oldE := st.caps[2*index], st.caps[2*index+1]
				if dir > 0 {
					st.caps[2*index], st.caps[2*index+1] = pos, end
				} else {
					st.caps[2*index], st.caps[2*index+1] = end, pos
				}
				if k(end) {
					return true
				}
				st.caps[2*index], st.caps[2*index+1] = oldS, oldE
				return false
			})
		}, nil
	case ')', '|':
		return nil, regexpError("Unexpected character")
	case '\\':
		return p.atomEscape()
	}
	p.at++
	u := uint16(c)
	return unitMatcher(func(x uint16) bool { return x == u }), nil
}

// groupName reads a group's name up to its >.
func (p *rparser) groupName() (string, error) {
	start := p.at
	for p.at < len(p.src) && p.src[p.at] != '>' {
		c := p.src[p.at]
		valid := c == '$' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80 ||
			p.at > start && c >= '0' && c <= '9'
		if !valid {
			return "", regexpError("Invalid capture group name")
		}
		p.at++
	}
	if p.at >= len(p.src) || p.at == start {
		return "", regexpError("Invalid capture group name")
	}
	name := fromUnits(p.src[start:p.at])
	p.at++
	return name, nil
}

// unitMatcher matches one code unit for which ok holds.
func unitMatcher(ok func(uint16) bool) rnode {
	return func(st *rstate, pos, dir int, k func(int) bool) bool {
		if !st.step() {
			return false
		}
		if dir > 0 {
			if pos < len(st.in) && ok(st.in[pos]) {
				return k(pos + 1)
			}
			return false
		}
		if pos > 0 && ok(st.in[pos-1]) {
			return k(pos - 1)
		}
		return false
	}
}

func wordUnit(c uint16) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func digitUnit(c uint16) bool { return c >= '0' && c <= '9' }

// classEscape is \d \D \s \S \w \W as a test of a code unit.
func classEscape(c uint16) func(uint16) bool {
	switch c {
	case 'd':
		return digitUnit
	case 'D':
		return func(x uint16) bool { return !digitUnit(x) }
	case 's':
		return isJSSpace
	case 'S':
		return func(x uint16) bool { return !isJSSpace(x) }
	case 'w':
		return wordUnit
	case 'W':
		return func(x uint16) bool { return !wordUnit(x) }
	}
	return nil
}

func (p *rparser) atomEscape() (rnode, error) {
	p.at++ // the backslash
	if p.at >= len(p.src) {
		return nil, regexpError("\\ at end of pattern")
	}
	c := p.src[p.at]
	if test := classEscape(c); test != nil {
		p.at++
		return unitMatcher(test), nil
	}
	if c >= '1' && c <= '9' {
		start := p.at
		n := 0
		for p.at < len(p.src) && digitUnit(p.src[p.at]) {
			if n < 1<<20 {
				n = n*10 + int(p.src[p.at]-'0')
			}
			p.at++
		}
		if n <= p.total {
			return backreference(func() int { return n }), nil
		}
		p.at = start // no such group: a legacy octal escape, or the digit 8 or 9 itself (Annex B)
	}
	if c == 'k' && p.named {
		p.at++
		if p.peek() != '<' {
			return nil, regexpError("Invalid named reference")
		}
		p.at++
		name, err := p.groupName()
		if err != nil {
			return nil, err
		}
		index := 0
		p.pending = append(p.pending, namedRef{name, func(i int) { index = i }})
		return backreference(func() int { return index }), nil
	}
	if c == 'c' && (p.at+1 >= len(p.src) || !asciiLetter(p.src[p.at+1])) {
		// \c with no letter after it is a backslash, and the c is read next (Annex B)
		return unitMatcher(func(x uint16) bool { return x == '\\' }), nil
	}
	u, err := p.characterEscape(false)
	if err != nil {
		return nil, err
	}
	return unitMatcher(func(x uint16) bool { return x == u }), nil
}

func asciiLetter(c uint16) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// characterEscape reads the escape after a backslash that stands for one code unit; inClass allows
// what only a class allows (\b is a backspace, \c with a digit or _).
func (p *rparser) characterEscape(inClass bool) (uint16, error) {
	c := p.src[p.at]
	p.at++
	switch c {
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'v':
		return '\v', nil
	case 'b':
		if inClass {
			return '\b', nil
		}
	case 'c':
		if p.at < len(p.src) {
			l := p.src[p.at]
			if asciiLetter(l) || inClass && (digitUnit(l) || l == '_') {
				p.at++
				return l % 32, nil
			}
		}
		if inClass {
			p.at-- // \c with nothing after it reads as a backslash in a class too
			return '\\', nil
		}
	case 'x':
		if p.at+1 < len(p.src) && hexUnit(p.src[p.at]) && hexUnit(p.src[p.at+1]) {
			v, _ := strconv.ParseUint(fromUnits(p.src[p.at:p.at+2]), 16, 16)
			p.at += 2
			return uint16(v), nil
		}
		return 'x', nil
	case 'u':
		if p.at+3 < len(p.src) && hexUnit(p.src[p.at]) && hexUnit(p.src[p.at+1]) && hexUnit(p.src[p.at+2]) && hexUnit(p.src[p.at+3]) {
			v, _ := strconv.ParseUint(fromUnits(p.src[p.at:p.at+4]), 16, 16)
			p.at += 4
			return uint16(v), nil
		}
		return 'u', nil
	}
	if c >= '0' && c <= '7' {
		// \0 alone is NUL; with more octal digits a legacy octal escape, up to \377
		v := int(c - '0')
		if p.at < len(p.src) && p.src[p.at] >= '0' && p.src[p.at] <= '7' {
			v = v*8 + int(p.src[p.at]-'0')
			p.at++
			if c <= '3' && p.at < len(p.src) && p.src[p.at] >= '0' && p.src[p.at] <= '7' {
				v = v*8 + int(p.src[p.at]-'0')
				p.at++
			}
		}
		return uint16(v), nil
	}
	return c, nil
}

func hexUnit(c uint16) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// backreference matches what group n captured, or nothing when it captured nothing.
func backreference(group func() int) rnode {
	return func(st *rstate, pos, dir int, k func(int) bool) bool {
		if !st.step() {
			return false
		}
		n := group()
		s, e := st.caps[2*n], st.caps[2*n+1]
		if s < 0 || e < 0 {
			return k(pos)
		}
		length := e - s
		if dir > 0 {
			if pos+length > len(st.in) || !equalUnits(st.in[pos:pos+length], st.in[s:e]) {
				return false
			}
			return k(pos + length)
		}
		if pos-length < 0 || !equalUnits(st.in[pos-length:pos], st.in[s:e]) {
			return false
		}
		return k(pos - length)
	}
}

// class reads [...]: ranges, single units and class escapes, ^ to negate.
func (p *rparser) class() (rnode, error) {
	p.at++ // [
	negate := false
	if p.peek() == '^' {
		negate = true
		p.at++
	}
	var tests []func(uint16) bool
	// classAtom reads one atom: a unit (ok) or a class escape (test)
	classAtom := func() (uint16, func(uint16) bool, error) {
		c := p.src[p.at]
		if c != '\\' {
			p.at++
			return c, nil, nil
		}
		p.at++
		if p.at >= len(p.src) {
			return 0, nil, regexpError("\\ at end of pattern")
		}
		e := p.src[p.at]
		if test := classEscape(e); test != nil {
			p.at++
			return 0, test, nil
		}
		if e == '-' {
			p.at++
			return '-', nil, nil
		}
		if e == '8' || e == '9' {
			p.at++
			return e, nil, nil
		}
		if e >= '1' && e <= '7' {
			u, err := p.characterEscape(true)
			return u, nil, err
		}
		if e == 'k' {
			p.at++
			return 'k', nil, nil
		}
		u, err := p.characterEscape(true)
		return u, nil, err
	}
	for {
		if p.at >= len(p.src) {
			return nil, regexpError("Unterminated character class")
		}
		if p.src[p.at] == ']' {
			p.at++
			break
		}
		lo, loTest, err := classAtom()
		if err != nil {
			return nil, err
		}
		if p.peek() == '-' && p.at+1 < len(p.src) && p.src[p.at+1] != ']' {
			p.at++
			hi, hiTest, err := classAtom()
			if err != nil {
				return nil, err
			}
			if loTest != nil || hiTest != nil {
				// a range with a class escape at an end is the escape, '-' and the other end (Annex B)
				for _, t := range []func(uint16) bool{loTest, hiTest} {
					if t != nil {
						tests = append(tests, t)
					}
				}
				if loTest == nil {
					l := lo
					tests = append(tests, func(x uint16) bool { return x == l })
				}
				if hiTest == nil {
					h := hi
					tests = append(tests, func(x uint16) bool { return x == h })
				}
				tests = append(tests, func(x uint16) bool { return x == '-' })
				continue
			}
			if hi < lo {
				return nil, regexpError("Range out of order in character class")
			}
			l, h := lo, hi
			tests = append(tests, func(x uint16) bool { return x >= l && x <= h })
			continue
		}
		if loTest != nil {
			tests = append(tests, loTest)
			continue
		}
		l := lo
		tests = append(tests, func(x uint16) bool { return x == l })
	}
	return unitMatcher(func(x uint16) bool {
		in := false
		for _, t := range tests {
			if t(x) {
				in = true
				break
			}
		}
		return in != negate
	}), nil
}

// ---- the members a list of a match carries beside its items (index, input, groups) ----

func (m *machine) setExtra(list []any, key string, v any) {
	if m.extras == nil {
		m.extras = map[uintptr]*Object{}
	}
	id := listID(list)
	extra := m.extras[id]
	if extra == nil {
		extra = newObject(3)
		m.extras[id] = extra
	}
	extra.set(key, v)
}

// listID is a list's identity: the address of its items.
func listID(list []any) uintptr { return reflect.ValueOf(list).Pointer() }

func (m *machine) extra(list []any) *Object {
	if m.extras == nil || cap(list) == 0 {
		return nil
	}
	return m.extras[listID(list)]
}
