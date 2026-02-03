package render

import "strings"

type nodeType int

const (
	nodeRoot nodeType = iota
	nodeText
	nodeVar
	nodeIf
	nodeRange
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

			case "range":
				n := &node{typ: nodeRange}

				if assignIdx := indexOf(parts, ":="); assignIdx > -1 {
					// range i, v := list
					vars := strings.ReplaceAll(strings.Join(parts[1:assignIdx], " "), ",", " ")
					varParts := strings.Fields(vars)
					if len(varParts) > 0 {
						n.keyVar = varParts[0]
					}
					if len(varParts) > 1 {
						n.valVar = varParts[1]
					}
					n.val = parts[assignIdx+1] // Target List
				} else {
					// range list
					n.val = parts[1]
				}

				addChild(current, n)
				stack = append(stack, n)

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
