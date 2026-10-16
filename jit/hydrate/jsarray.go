package hydrate

import (
	"math"
)

// serverListLimit is the most items the server builds for one list (2^20): Array(1e9) is a length in
// the browser and a billion slots here, so the server refuses what would be longer.
const serverListLimit = 1 << 20

var errTooMany = rangeError("Invalid array length")

// mutating are the Array.prototype methods that change their list: the server makes a new one and
// writes it back where the list was read from (a name, $.name, a member of an object the expression
// made), for a Go slice cannot grow in place under every name that holds it.
var mutating = map[string]bool{
	"copyWithin": true, "fill": true, "pop": true, "push": true, "reverse": true, "shift": true,
	"sort": true, "splice": true, "unshift": true,
}

// arrayLike is O as a list, for an Array.prototype method called on what is no list ([].join.call(s)):
// a string's code units, an object's items up to its length (a hole where it has none).
func (m *machine) arrayLike(f *frame, o any) ([]any, error) {
	switch t := o.(type) {
	case []any:
		return t, nil
	case string:
		u := units(t)
		out := newList(len(u))
		for i, c := range u {
			out[i] = unitString(c)
		}
		return out, nil
	case nil, undefinedType:
		return nil, typeError("Array.prototype method called on null or undefined")
	}
	if !isObject(o) {
		return newList(0), nil
	}
	lengthValue, err := m.get(f, o, "length")
	if err != nil {
		return nil, err
	}
	n, err := m.toLength(f, lengthValue)
	if err != nil {
		return nil, err
	}
	if n > serverListLimit {
		return nil, errTooMany
	}
	out := newList(int(n))
	for i := range out {
		key := primitiveText(float64(i))
		if !m.hasOwn(o, key) {
			out[i] = hole
			continue
		}
		v, err := m.get(f, o, key)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// callback is the function an iterating method was given, or the TypeError V8 throws.
func (m *machine) callback(f *frame, v any) (any, error) {
	if !callable(v) {
		text, _ := m.describe(f, v)
		return nil, typeError(text + " is not a function")
	}
	return v, nil
}

// arrayMethod is a method of Array.prototype, as V8 runs it.
func (m *machine) arrayMethod(f *frame, name string, this any, args []any) (any, error) {
	list, isList := this.([]any)
	if !isList {
		if mutating[name] {
			return nil, typeError("Array.prototype." + name + " changes a list: called on what is no list")
		}
		view, err := m.arrayLike(f, this)
		if err != nil {
			return nil, err
		}
		list = view
	}
	n := len(list)
	switch name {
	case "at":
		i, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if i < 0 {
			i += float64(n)
		}
		if i < 0 || i >= float64(n) {
			return Undefined, nil
		}
		return item(list[int(i)]), nil
	case "concat":
		out := newList(0)
		out = append(out, list...)
		for _, a := range args {
			if items, ok := a.([]any); ok {
				out = append(out, items...)
			} else {
				out = append(out, a)
			}
			if len(out) > serverListLimit {
				return nil, errTooMany
			}
		}
		return out, nil
	case "copyWithin":
		out := append(newList(0), list...)
		target, err := m.relativeIndex(f, arg(args, 0), n, 0)
		if err != nil {
			return nil, err
		}
		start, err := m.relativeIndex(f, arg(args, 1), n, 0)
		if err != nil {
			return nil, err
		}
		end, err := m.relativeIndex(f, arg(args, 2), n, n)
		if err != nil {
			return nil, err
		}
		count := min(end-start, n-target)
		if count > 0 {
			copy(out[target:target+count], list[start:start+count])
		}
		m.mutated = out
		return out, nil
	case "entries", "keys", "values":
		return arrayIterator(name, list), nil
	case "every", "some", "filter", "find", "findIndex", "findLast", "findLastIndex", "forEach", "map", "flatMap":
		fn, err := m.callback(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		thisArg := arg(args, 1)
		var out []any
		if name == "map" {
			out = newList(n)
			for i := range out {
				out[i] = hole
			}
		} else if name == "filter" || name == "flatMap" {
			out = newList(0)
		}
		indexes := make([]int, n)
		for i := range indexes {
			indexes[i] = i
			if name == "findLast" || name == "findLastIndex" {
				indexes[i] = n - 1 - i
			}
		}
		for _, i := range indexes {
			if i >= len(list) {
				continue
			}
			v := list[i]
			skipsHoles := name != "find" && name != "findIndex" && name != "findLast" && name != "findLastIndex"
			if isHole(v) && skipsHoles {
				continue
			}
			v = item(v)
			result, err := m.callValue(f, fn, thisArg, []any{v, float64(i), this})
			if err != nil {
				return nil, err
			}
			switch name {
			case "every":
				if !truthy(result) {
					return false, nil
				}
			case "some":
				if truthy(result) {
					return true, nil
				}
			case "filter":
				if truthy(result) {
					out = append(out, v)
				}
			case "find", "findLast":
				if truthy(result) {
					return v, nil
				}
			case "findIndex", "findLastIndex":
				if truthy(result) {
					return float64(i), nil
				}
			case "map":
				out[i] = result
			case "flatMap":
				if inner, ok := result.([]any); ok {
					for _, x := range inner {
						if !isHole(x) {
							out = append(out, x)
						}
					}
				} else {
					out = append(out, result)
				}
				if len(out) > serverListLimit {
					return nil, errTooMany
				}
			}
		}
		switch name {
		case "every":
			return true, nil
		case "some":
			return false, nil
		case "find", "findLast", "forEach":
			return Undefined, nil
		case "findIndex", "findLastIndex":
			return float64(-1), nil
		}
		return out, nil
	case "fill":
		out := append(newList(0), list...)
		start, err := m.relativeIndex(f, arg(args, 1), n, 0)
		if err != nil {
			return nil, err
		}
		end, err := m.relativeIndex(f, arg(args, 2), n, n)
		if err != nil {
			return nil, err
		}
		for i := start; i < end; i++ {
			out[i] = arg(args, 0)
		}
		m.mutated = out
		return out, nil
	case "flat":
		depth := 1.0
		if !isUndefined(arg(args, 0)) {
			d, err := m.toIntegerOrInfinity(f, arg(args, 0))
			if err != nil {
				return nil, err
			}
			depth = math.Max(d, 0)
		}
		out := newList(0)
		var flatten func(items []any, depth float64, level int) error
		flatten = func(items []any, depth float64, level int) error {
			if level > 1000 {
				return rangeError("Maximum call stack size exceeded")
			}
			for _, x := range items {
				if isHole(x) {
					continue
				}
				if inner, ok := x.([]any); ok && depth > 0 {
					if err := flatten(inner, depth-1, level+1); err != nil {
						return err
					}
					continue
				}
				out = append(out, x)
				if len(out) > serverListLimit {
					return errTooMany
				}
			}
			return nil
		}
		if err := flatten(list, depth, 0); err != nil {
			return nil, err
		}
		return out, nil
	case "includes", "indexOf":
		if n == 0 {
			if name == "includes" {
				return false, nil
			}
			return float64(-1), nil
		}
		from, err := m.toIntegerOrInfinity(f, arg(args, 1))
		if err != nil {
			return nil, err
		}
		if from < 0 {
			from = math.Max(float64(n)+from, 0)
		}
		target := arg(args, 0)
		for i := int(math.Min(from, float64(n))); i < n; i++ {
			v := list[i]
			if name == "includes" {
				if sameValueZero(item(v), target) {
					return true, nil
				}
				continue
			}
			if !isHole(v) && strictEquals(item(v), target) {
				return float64(i), nil
			}
		}
		if name == "includes" {
			return false, nil
		}
		return float64(-1), nil
	case "lastIndexOf":
		if n == 0 {
			return float64(-1), nil
		}
		from := float64(n - 1)
		if len(args) > 1 {
			var err error
			from, err = m.toIntegerOrInfinity(f, args[1])
			if err != nil {
				return nil, err
			}
			if from < 0 {
				from += float64(n)
			} else {
				from = math.Min(from, float64(n-1))
			}
		}
		for i := int(from); i >= 0; i-- {
			if !isHole(list[i]) && strictEquals(item(list[i]), arg(args, 0)) {
				return float64(i), nil
			}
		}
		return float64(-1), nil
	case "join", "toString":
		if name == "toString" {
			return m.joinList(f, list, ",")
		}
		sep := ","
		if !isUndefined(arg(args, 0)) {
			var err error
			sep, err = m.toString(f, arg(args, 0))
			if err != nil {
				return nil, err
			}
		}
		return m.joinList(f, list, sep)
	case "toLocaleString":
		// each item's toLocaleString: a number as the visitor's language writes it, which the browser
		// answers
		return Undefined, nil
	case "pop":
		if n == 0 {
			m.mutated = newList(0)
			return Undefined, nil
		}
		m.mutated = append(newList(0), list[:n-1]...)
		return item(list[n-1]), nil
	case "shift":
		if n == 0 {
			m.mutated = newList(0)
			return Undefined, nil
		}
		m.mutated = append(newList(0), list[1:]...)
		return item(list[0]), nil
	case "push", "unshift":
		if n+len(args) > serverListLimit {
			return nil, errTooMany
		}
		out := newList(0)
		if name == "push" {
			out = append(append(out, list...), args...)
		} else {
			out = append(append(out, args...), list...)
		}
		m.mutated = out
		return float64(len(out)), nil
	case "reduce", "reduceRight":
		fn, err := m.callback(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		order := make([]int, 0, n)
		for i := 0; i < n; i++ {
			j := i
			if name == "reduceRight" {
				j = n - 1 - i
			}
			if !isHole(list[j]) {
				order = append(order, j)
			}
		}
		var acc any
		if len(args) >= 2 {
			acc = args[1]
		} else {
			if len(order) == 0 {
				return nil, typeError("Reduce of empty array with no initial value")
			}
			acc = item(list[order[0]])
			order = order[1:]
		}
		for _, i := range order {
			acc, err = m.callValue(f, fn, Undefined, []any{acc, item(list[i]), float64(i), this})
			if err != nil {
				return nil, err
			}
		}
		return acc, nil
	case "reverse", "toReversed":
		out := newList(n)
		for i, v := range list {
			if name == "toReversed" {
				v = item(v)
			}
			out[n-1-i] = v
		}
		if name == "reverse" {
			m.mutated = out
		}
		return out, nil
	case "slice":
		start, err := m.relativeIndex(f, arg(args, 0), n, 0)
		if err != nil {
			return nil, err
		}
		end, err := m.relativeIndex(f, arg(args, 1), n, n)
		if err != nil {
			return nil, err
		}
		if start >= end {
			return newList(0), nil
		}
		return append(newList(0), list[start:end]...), nil
	case "sort", "toSorted":
		compare := arg(args, 0)
		if !isUndefined(compare) && !callable(compare) {
			return nil, typeError("The comparison function must be either a function or undefined")
		}
		out, err := m.sortList(f, list, compare, name == "toSorted")
		if err != nil {
			return nil, err
		}
		if name == "sort" {
			m.mutated = out
		}
		return out, nil
	case "splice", "toSpliced":
		start, err := m.relativeIndex(f, arg(args, 0), n, 0)
		if err != nil {
			return nil, err
		}
		count := 0
		switch {
		case len(args) == 0:
			count = 0
		case len(args) == 1:
			count = n - start
		default:
			c, err := m.toIntegerOrInfinity(f, args[1])
			if err != nil {
				return nil, err
			}
			count = int(math.Min(math.Max(c, 0), float64(n-start)))
		}
		var inserted []any
		if len(args) > 2 {
			inserted = args[2:]
		}
		if n-count+len(inserted) > serverListLimit {
			return nil, errTooMany
		}
		out := append(append(append(newList(0), list[:start]...), inserted...), list[start+count:]...)
		if name == "toSpliced" {
			for i, v := range out {
				out[i] = item(v)
			}
			return out, nil
		}
		m.mutated = out
		return append(newList(0), list[start:start+count]...), nil
	case "with":
		i, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if i < 0 {
			i += float64(n)
		}
		if i < 0 || i >= float64(n) {
			return nil, rangeError("Invalid index : " + primitiveText(i))
		}
		out := newList(n)
		for j, v := range list {
			out[j] = item(v)
		}
		out[int(i)] = arg(args, 1)
		return out, nil
	}
	return nil, typeError("Array.prototype." + name + " is not a function")
}

// joinList is Array.prototype.join: each item's text, null, undefined and a hole as nothing, a list
// already being joined as nothing, as V8 writes a list that holds itself.
func (m *machine) joinList(f *frame, list []any, sep string) (string, error) {
	for _, outer := range m.joining {
		if sameRef(outer, list) {
			return "", nil
		}
	}
	m.joining = append(m.joining, list)
	defer func() { m.joining = m.joining[:len(m.joining)-1] }()
	parts := make([]string, len(list))
	total := 0
	for i, v := range list {
		if isHole(v) || nullish(v) {
			continue
		}
		text, err := m.toString(f, v)
		if err != nil {
			return "", err
		}
		parts[i] = text
		total += len(text) + len(sep)
		if total > serverStringLimit {
			return "", errTooLong
		}
	}
	return join16(parts, sep), nil
}

// sortList is sort and toSorted as V8 runs them: undefined after every other item and holes after
// those (toSorted reads a hole as undefined), the rest by the comparison — the function given, or the
// items' texts by code units — in V8's TimSort, which for a list under 64 items is one run found and
// a binary insertion sort, so even a comparison that is not consistent ((a, b) => a + b) orders as the
// browser orders.
func (m *machine) sortList(f *frame, list []any, compare any, copying bool) ([]any, error) {
	var work []any
	undefineds, holes := 0, 0
	for _, v := range list {
		switch {
		case isHole(v) && !copying:
			holes++
		case isHole(v) || isUndefined(v):
			undefineds++
		default:
			work = append(work, v)
		}
	}
	var failed error
	cmp := func(a, b any) float64 {
		if failed != nil {
			return 0
		}
		if !isUndefined(compare) {
			out, err := m.callValue(f, compare, Undefined, []any{a, b})
			if err != nil {
				failed = err
				return 0
			}
			n, err := m.toNumber(f, out)
			if err != nil {
				failed = err
				return 0
			}
			if n != n {
				return 0
			}
			return n
		}
		x, err := m.toString(f, a)
		if err != nil {
			failed = err
			return 0
		}
		y, err := m.toString(f, b)
		if err != nil {
			failed = err
			return 0
		}
		return float64(compare16(x, y))
	}
	if err := timSort(work, cmp, &failed); err != nil {
		return nil, err
	}
	out := newList(0)
	out = append(out, work...)
	for i := 0; i < undefineds; i++ {
		out = append(out, Undefined)
	}
	for i := 0; i < holes; i++ {
		out = append(out, hole)
	}
	return out, nil
}

// timSort sorts work in place as V8's ArrayTimSort does (third_party/v8/builtins/array-sort.tq),
// comparing with cmp: the runs it finds and makes, the binary insertion sort that stretches a short run
// to the least run length, the stack of runs merged to keep its lengths shrinking, and the merges that
// gallop once one run keeps winning. Every comparison it makes is the browser's, in the browser's order,
// so a comparison that is not consistent ((a, b) => (a % 3) - (b % 2)) orders a list of any length as
// the browser does, and a comparison that counts its calls counts the same (16/10/2026: a list of 64 or
// more items was sorted with sort.SliceStable, which orders alike only for a consistent comparison).
// Once cmp fails it answers 0 and the sort runs out without calling it again.
func timSort(work []any, cmp func(a, b any) float64, failed *error) error {
	n := len(work)
	if n < 2 {
		return nil
	}
	s := &timSorter{work: work, cmp: cmp, minGallop: timMinGallop}
	low, remaining := 0, n
	minRun := timMinRunLength(n)
	for remaining != 0 {
		run := s.countAndMakeRun(low, low+remaining)
		if run < minRun {
			forced := min(minRun, remaining)
			s.binaryInsertionSort(low, low+run, low+forced)
			run = forced
		}
		s.runs = append(s.runs, timRun{low, run})
		s.mergeCollapse()
		low += run
		remaining -= run
	}
	s.mergeForceCollapse()
	return *failed
}

// timMinGallop is how many times in a row one run wins before a merge starts to gallop
// (kMinGallopWins).
const timMinGallop = 7

type timRun struct{ base, length int }

type timSorter struct {
	work      []any
	cmp       func(a, b any) float64
	runs      []timRun
	minGallop int
}

// timMinRunLength is ComputeMinRunLength: n itself under 64, else between 32 and 64 so that n divided
// by it is a power of two or just under one.
func timMinRunLength(n int) int {
	r := 0
	for n >= 64 {
		r |= n & 1
		n >>= 1
	}
	return n + r
}

// countAndMakeRun is the length of the run that starts at low (before high): ascending, or strictly
// descending and then reversed in place.
func (s *timSorter) countAndMakeRun(low, high int) int {
	if low+1 == high {
		return 1
	}
	run := 2
	descending := s.cmp(s.work[low+1], s.work[low]) < 0
	previous := s.work[low+1]
	for i := low + 2; i < high; i++ {
		order := s.cmp(s.work[i], previous)
		if descending && order >= 0 || !descending && order < 0 {
			break
		}
		previous = s.work[i]
		run++
	}
	if descending {
		for i, j := low, low+run-1; i < j; i, j = i+1, j-1 {
			s.work[i], s.work[j] = s.work[j], s.work[i]
		}
	}
	return run
}

// binaryInsertionSort sorts [low, high), of which [low, start) is sorted already, each next item put
// after the items it is not less than.
func (s *timSorter) binaryInsertionSort(low, start, high int) {
	if low == start {
		start++
	}
	for ; start < high; start++ {
		left, right := low, start
		pivot := s.work[start]
		for left < right {
			mid := left + (right-left)>>1
			if s.cmp(pivot, s.work[mid]) < 0 {
				right = mid
			} else {
				left = mid + 1
			}
		}
		copy(s.work[left+1:start+1], s.work[left:start])
		s.work[left] = pivot
	}
}

// runInvariant reports whether run n-2 is longer than runs n-1 and n together (true below 2).
func (s *timSorter) runInvariant(n int) bool {
	if n < 2 {
		return true
	}
	return s.runs[n-2].length > s.runs[n-1].length+s.runs[n].length
}

// mergeCollapse merges runs on the stack until each is longer than the two above it together, and
// each longer than the one above it.
func (s *timSorter) mergeCollapse() {
	for len(s.runs) > 1 {
		n := len(s.runs) - 2
		if !s.runInvariant(n+1) || !s.runInvariant(n) {
			if s.runs[n-1].length < s.runs[n+1].length {
				n--
			}
			s.mergeAt(n)
		} else if s.runs[n].length <= s.runs[n+1].length {
			s.mergeAt(n)
		} else {
			break
		}
	}
}

// mergeForceCollapse merges every run left on the stack, at the end.
func (s *timSorter) mergeForceCollapse() {
	for len(s.runs) > 1 {
		n := len(s.runs) - 2
		if n > 0 && s.runs[n-1].length < s.runs[n+1].length {
			n--
		}
		s.mergeAt(n)
	}
}

// mergeAt merges runs i and i+1 (i is the second or the third from the top).
func (s *timSorter) mergeAt(i int) {
	baseA, lengthA := s.runs[i].base, s.runs[i].length
	baseB, lengthB := s.runs[i+1].base, s.runs[i+1].length
	s.runs[i].length = lengthA + lengthB
	if i == len(s.runs)-3 {
		s.runs[i+1] = s.runs[i+2]
	}
	s.runs = s.runs[:len(s.runs)-1]
	// where b starts in a: what of a comes before is in place already
	k := s.gallopRight(s.work, s.work[baseB], baseA, lengthA, 0)
	baseA += k
	lengthA -= k
	if lengthA == 0 {
		return
	}
	// where a ends in b: what of b comes after is in place already
	lengthB = s.gallopLeft(s.work, s.work[baseA+lengthA-1], baseB, lengthB, lengthB-1)
	if lengthB == 0 {
		return
	}
	if lengthA <= lengthB {
		s.mergeLow(baseA, lengthA, baseB, lengthB)
	} else {
		s.mergeHigh(baseA, lengthA, baseB, lengthB)
	}
}

// gallopLeft is where key goes in the sorted [base, base+length) of list, before any item equal to it,
// searched from hint by steps of 1, 3, 7, 15 … and then halving.
func (s *timSorter) gallopLeft(list []any, key any, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if s.cmp(list[base+hint], key) < 0 {
		maxOfs := length - hint
		for offset < maxOfs {
			if s.cmp(list[base+hint+offset], key) >= 0 {
				break
			}
			lastOfs = offset
			offset = offset<<1 + 1
		}
		offset = min(offset, maxOfs)
		lastOfs += hint
		offset += hint
	} else {
		maxOfs := hint + 1
		for offset < maxOfs {
			if s.cmp(list[base+hint-offset], key) < 0 {
				break
			}
			lastOfs = offset
			offset = offset<<1 + 1
		}
		offset = min(offset, maxOfs)
		lastOfs, offset = hint-offset, hint-lastOfs
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + (offset-lastOfs)>>1
		if s.cmp(list[base+m], key) < 0 {
			lastOfs = m + 1
		} else {
			offset = m
		}
	}
	return offset
}

// gallopRight is gallopLeft after any item equal to key.
func (s *timSorter) gallopRight(list []any, key any, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if s.cmp(key, list[base+hint]) < 0 {
		maxOfs := hint + 1
		for offset < maxOfs {
			if s.cmp(key, list[base+hint-offset]) >= 0 {
				break
			}
			lastOfs = offset
			offset = offset<<1 + 1
		}
		offset = min(offset, maxOfs)
		lastOfs, offset = hint-offset, hint-lastOfs
	} else {
		maxOfs := length - hint
		for offset < maxOfs {
			if s.cmp(key, list[base+hint+offset]) < 0 {
				break
			}
			lastOfs = offset
			offset = offset<<1 + 1
		}
		offset = min(offset, maxOfs)
		lastOfs += hint
		offset += hint
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + (offset-lastOfs)>>1
		if s.cmp(key, list[base+m]) < 0 {
			offset = m
		} else {
			lastOfs = m + 1
		}
	}
	return offset
}

// mergeLow merges run a (the shorter, copied aside) with run b after it, from the front.
func (s *timSorter) mergeLow(baseA, lengthA, baseB, lengthB int) {
	w := s.work
	temp := append([]any(nil), w[baseA:baseA+lengthA]...)
	dest, cursorTemp, cursorB := baseA, 0, baseB
	w[dest] = w[cursorB]
	dest++
	cursorB++
	lengthB--
	copyB := func() {
		// the last item of a goes after everything left of b
		copy(w[dest:dest+lengthB], w[cursorB:cursorB+lengthB])
		w[dest+lengthB] = temp[cursorTemp]
	}
	succeed := func() {
		if lengthA > 0 {
			copy(w[dest:dest+lengthA], temp[cursorTemp:cursorTemp+lengthA])
		}
	}
	if lengthB == 0 {
		succeed()
		return
	}
	if lengthA == 1 {
		copyB()
		return
	}
	minGallop := s.minGallop
	for {
		winsA, winsB := 0, 0
		for {
			if s.cmp(w[cursorB], temp[cursorTemp]) < 0 {
				w[dest] = w[cursorB]
				dest++
				cursorB++
				winsB++
				lengthB--
				winsA = 0
				if lengthB == 0 {
					succeed()
					return
				}
				if winsB >= minGallop {
					break
				}
			} else {
				w[dest] = temp[cursorTemp]
				dest++
				cursorTemp++
				winsA++
				lengthA--
				winsB = 0
				if lengthA == 1 {
					copyB()
					return
				}
				if winsA >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for winsA >= timMinGallop || winsB >= timMinGallop || first {
			first = false
			minGallop = max(1, minGallop-1)
			s.minGallop = minGallop
			winsA = s.gallopRight(temp, w[cursorB], cursorTemp, lengthA, 0)
			if winsA > 0 {
				copy(w[dest:dest+winsA], temp[cursorTemp:cursorTemp+winsA])
				dest += winsA
				cursorTemp += winsA
				lengthA -= winsA
				if lengthA == 1 {
					copyB()
					return
				}
				if lengthA == 0 {
					succeed()
					return
				}
			}
			w[dest] = w[cursorB]
			dest++
			cursorB++
			lengthB--
			if lengthB == 0 {
				succeed()
				return
			}
			winsB = s.gallopLeft(w, temp[cursorTemp], cursorB, lengthB, 0)
			if winsB > 0 {
				copy(w[dest:dest+winsB], w[cursorB:cursorB+winsB])
				dest += winsB
				cursorB += winsB
				lengthB -= winsB
				if lengthB == 0 {
					succeed()
					return
				}
			}
			w[dest] = temp[cursorTemp]
			dest++
			cursorTemp++
			lengthA--
			if lengthA == 1 {
				copyB()
				return
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}

// mergeHigh merges run a with run b after it (the shorter, copied aside), from the back.
func (s *timSorter) mergeHigh(baseA, lengthA, baseB, lengthB int) {
	w := s.work
	temp := append([]any(nil), w[baseB:baseB+lengthB]...)
	dest, cursorTemp, cursorA := baseB+lengthB-1, lengthB-1, baseA+lengthA-1
	w[dest] = w[cursorA]
	dest--
	cursorA--
	lengthA--
	copyA := func() {
		// the first item of b goes before everything left of a
		dest -= lengthA
		cursorA -= lengthA
		copy(w[dest+1:dest+1+lengthA], w[cursorA+1:cursorA+1+lengthA])
		w[dest] = temp[cursorTemp]
	}
	succeed := func() {
		if lengthB > 0 {
			copy(w[dest-(lengthB-1):dest+1], temp[:lengthB])
		}
	}
	if lengthA == 0 {
		succeed()
		return
	}
	if lengthB == 1 {
		copyA()
		return
	}
	minGallop := s.minGallop
	for {
		winsA, winsB := 0, 0
		for {
			if s.cmp(temp[cursorTemp], w[cursorA]) < 0 {
				w[dest] = w[cursorA]
				dest--
				cursorA--
				winsA++
				lengthA--
				winsB = 0
				if lengthA == 0 {
					succeed()
					return
				}
				if winsA >= minGallop {
					break
				}
			} else {
				w[dest] = temp[cursorTemp]
				dest--
				cursorTemp--
				winsB++
				lengthB--
				winsA = 0
				if lengthB == 1 {
					copyA()
					return
				}
				if winsB >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for winsA >= timMinGallop || winsB >= timMinGallop || first {
			first = false
			minGallop = max(1, minGallop-1)
			s.minGallop = minGallop
			k := s.gallopRight(w, temp[cursorTemp], baseA, lengthA, lengthA-1)
			winsA = lengthA - k
			if winsA > 0 {
				dest -= winsA
				cursorA -= winsA
				copy(w[dest+1:dest+1+winsA], w[cursorA+1:cursorA+1+winsA])
				lengthA -= winsA
				if lengthA == 0 {
					succeed()
					return
				}
			}
			w[dest] = temp[cursorTemp]
			dest--
			cursorTemp--
			lengthB--
			if lengthB == 1 {
				copyA()
				return
			}
			k = s.gallopLeft(temp, w[cursorA], 0, lengthB, lengthB-1)
			winsB = lengthB - k
			if winsB > 0 {
				dest -= winsB
				cursorTemp -= winsB
				copy(w[dest+1:dest+1+winsB], temp[cursorTemp+1:cursorTemp+1+winsB])
				lengthB -= winsB
				if lengthB == 1 {
					copyA()
					return
				}
				if lengthB == 0 {
					succeed()
					return
				}
			}
			w[dest] = w[cursorA]
			dest--
			cursorA--
			lengthA--
			if lengthA == 0 {
				succeed()
				return
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}

// arrayConstruct is Array(...): Array(n) a list of n holes, Array(x) for anything else [x], Array(a, b)
// [a, b].
func (m *machine) arrayConstruct(f *frame, args []any) (any, error) {
	if len(args) == 1 {
		if n, ok := args[0].(float64); ok {
			if n != float64(toUint32(n)) {
				return nil, errTooMany
			}
			if n > serverListLimit {
				return nil, errTooMany
			}
			out := newList(int(n))
			for i := range out {
				out[i] = hole
			}
			return out, nil
		}
	}
	return append(newList(0), args...), nil
}

// arrayStatic is Array.isArray, Array.of, Array.from and Array.fromAsync.
func (m *machine) arrayStatic(f *frame, name string, args []any) (any, error) {
	switch name {
	case "isArray":
		_, ok := arg(args, 0).([]any)
		return ok, nil
	case "of":
		return append(newList(0), args...), nil
	case "fromAsync":
		return &promise{}, nil
	case "from":
		source := arg(args, 0)
		mapper := arg(args, 1)
		if !isUndefined(mapper) && !callable(mapper) {
			text, _ := m.describe(f, mapper)
			return nil, typeError(text + " is not a function")
		}
		if nullish(source) {
			return nil, typeError(primitiveText(source) + " is not iterable (cannot read property Symbol(Symbol.iterator))")
		}
		var items []any
		switch t := source.(type) {
		case string:
			// a string's iterator gives its characters, a pair of halves as one
			u := units(t)
			for i := 0; i < len(u); i++ {
				if u[i] >= 0xD800 && u[i] <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
					items = append(items, fromUnits(u[i:i+2]))
					i++
					continue
				}
				items = append(items, unitString(u[i]))
			}
		case []any:
			items = make([]any, len(t))
			for i, v := range t {
				items[i] = item(v)
			}
		case *iterator:
			for {
				v, done, err := t.step(m, f)
				if err != nil {
					return nil, err
				}
				if done {
					break
				}
				items = append(items, v)
				if len(items) > serverListLimit {
					return nil, errTooMany
				}
			}
		default:
			view, err := m.arrayLike(f, source)
			if err != nil {
				return nil, err
			}
			items = make([]any, len(view))
			for i, v := range view {
				items[i] = item(v)
			}
		}
		out := newList(len(items))
		for i, v := range items {
			if !isUndefined(mapper) {
				mapped, err := m.callValue(f, mapper, arg(args, 2), []any{v, float64(i)})
				if err != nil {
					return nil, err
				}
				v = mapped
			}
			out[i] = v
		}
		return out, nil
	}
	return nil, typeError("Array." + name + " is not a function")
}
