package render

import "strings"

type nodeType int

const (
	nodeRoot nodeType = iota
	nodeText
	nodeVar
	nodeIf
	nodeRange
	nodeLet // New: Assignment
)

type node struct {
	typ         nodeType
	val         string   // Variable name or Condition
	args        []string // Arguments for comparison
	keyVar      string   // "i" in range i, v := list
	valVar      string   // "v" in range i, v := list
	children    []*node
	alt         []*node // Else block
	parsingElse bool    // Parsing state
}

func specializeTokens(tmpl string) []string {
	var tokens []string
	start := 0
	for {
		open := strings.Index(tmpl[start:], "{{")
		if open == -1 {
			tokens = append(tokens, tmpl[start:])
			break
		}
		if open > 0 {
			tokens = append(tokens, tmpl[start:start+open])
		}

		close := strings.Index(tmpl[start+open:], "}}")
		if close == -1 {
			tokens = append(tokens, tmpl[start+open:])
			break
		}

		tagContent := tmpl[start+open+2 : start+open+close]
		tokens = append(tokens, "{{"+tagContent+"}}")

		start += open + close + 2
	}
	var clean []string
	for _, t := range tokens {
		if t != "" {
			clean = append(clean, t)
		}
	}
	return clean
}

func parse(tokens []string) *node {
	root := &node{typ: nodeRoot}
	stack := []*node{root}

	for _, t := range tokens {
		current := stack[len(stack)-1]

		if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") {
			content := strings.TrimSpace(t[2 : len(t)-2])
			parts := strings.Fields(content)

			if len(parts) == 0 {
				continue
			}

			cmd := parts[0]

			switch cmd {
			case "if":
				n := &node{typ: nodeIf, val: parts[1]}
				if len(parts) > 2 {
					n.args = parts[2:]
				}
				addChild(current, n)
				stack = append(stack, n)

			case "for": // Clean Syntax: 'for' only
				n := &node{typ: nodeRange}

				if inIdx := indexOf(parts, "in"); inIdx > -1 {
					// Pattern: for item in list OR for (i, item) in list
					varsPart := strings.Join(parts[1:inIdx], "")

					// Tuple syntax handling: (i,v)
					if strings.HasPrefix(varsPart, "(") && strings.HasSuffix(varsPart, ")") {
						inner := varsPart[1 : len(varsPart)-1]
						subParts := strings.Split(inner, ",")
						if len(subParts) > 1 {
							n.keyVar = subParts[0]
							n.valVar = subParts[1]
						} else {
							n.valVar = subParts[0]
						}
					} else {
						// Single variable: item in list
						n.valVar = parts[1]
					}
					n.val = parts[inIdx+1] // list
				} else {
					// for list (Implicit '.' point to item)
					n.val = parts[1]
				}

				addChild(current, n)
				stack = append(stack, n)

			case "let": // New Logic: let x = y
				if len(parts) >= 4 && parts[2] == "=" {
					// parts[1] is variable name
					// parts[3] is value expression (simple)
					n := &node{typ: nodeLet, keyVar: parts[1], val: parts[3]}
					addChild(current, n)
				}

			case "else":
				if current.typ == nodeIf {
					current.parsingElse = true
				}

			case "end":
				if len(stack) > 1 {
					stack = stack[:len(stack)-1]
				}

			default:
				// Variable
				n := &node{typ: nodeVar, val: content}
				addChild(current, n)
			}
		} else {
			n := &node{typ: nodeText, val: t}
			addChild(current, n)
		}
	}
	return root
}

func addChild(parent, child *node) {
	if parent.parsingElse {
		parent.alt = append(parent.alt, child)
	} else {
		parent.children = append(parent.children, child)
	}
}

func indexOf(parts []string, target string) int {
	for i, p := range parts {
		if p == target {
			return i
		}
	}
	return -1
}
