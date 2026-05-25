package work

import (
	"strings"
)

type nodeRouter struct {
	part     string
	children []*nodeRouter
	isWild   bool
	routers  map[string]*Router // method -> Router
}

type Routes struct {
	root *nodeRouter
}

func NewRoutes() *Routes {
	return &Routes{
		root: &nodeRouter{
			routers: make(map[string]*Router),
		},
	}
}

func (t *Routes) Insert(method string, pattern string, route *Router) {
	parts := parsePattern(pattern)
	node := t.root
	for _, part := range parts {
		var child *nodeRouter
		for _, c := range node.children {
			if c.part == part {
				child = c
				break
			}
		}
		if child == nil {
			isWild := part == "*" || strings.HasPrefix(part, ":")
			child = &nodeRouter{
				part:    part,
				isWild:  isWild,
				routers: make(map[string]*Router),
			}
			node.children = append(node.children, child)
		}
		node = child
	}
	node.routers[strings.ToUpper(method)] = route
}

func (t *Routes) Match(method string, path string) (*Router, map[string]string) {
	searchParts := parsePattern(path)
	params := make(map[string]string)

	var search func(node *nodeRouter, partIndex int) *Router
	search = func(node *nodeRouter, partIndex int) *Router {
		if partIndex == len(searchParts) {
			// Exact match on node
			if rt, ok := node.routers[strings.ToUpper(method)]; ok {
				return rt
			}
			if rt, ok := node.routers["ANY"]; ok {
				return rt
			}

			// Optional parameter check at final segment
			for _, child := range node.children {
				if strings.HasPrefix(child.part, ":") && strings.HasSuffix(child.part, "?") {
					if rt, ok := child.routers[strings.ToUpper(method)]; ok {
						paramName := child.part[1 : len(child.part)-1]
						params[paramName] = ""
						return rt
					}
					if rt, ok := child.routers["ANY"]; ok {
						paramName := child.part[1 : len(child.part)-1]
						params[paramName] = ""
						return rt
					}
				}
			}

			// Wildcard check at final segment (e.g. "/" matches "/*")
			for _, child := range node.children {
				if child.part == "*" {
					if rt, ok := child.routers[strings.ToUpper(method)]; ok {
						return rt
					}
					if rt, ok := child.routers["ANY"]; ok {
						return rt
					}
				}
			}
			return nil
		}

		part := searchParts[partIndex]

		// 1. Exact match child first
		for _, child := range node.children {
			if child.part == part && !child.isWild {
				if matched := search(child, partIndex+1); matched != nil {
					return matched
				}
			}
		}

		// 2. Dynamic parameter child next
		for _, child := range node.children {
			if strings.HasPrefix(child.part, ":") {
				paramName := child.part[1:]
				isOptional := strings.HasSuffix(paramName, "?")
				if isOptional {
					paramName = paramName[:len(paramName)-1]
				}

				if matched := search(child, partIndex+1); matched != nil {
					params[paramName] = part
					return matched
				}
			}
		}

		// 3. Wildcard child last
		for _, child := range node.children {
			if child.part == "*" {
				if rt, ok := child.routers[strings.ToUpper(method)]; ok {
					return rt
				}
				if rt, ok := child.routers["ANY"]; ok {
					return rt
				}
			}
		}

		return nil
	}

	matchedRoute := search(t.root, 0)
	return matchedRoute, params
}

func parsePattern(pattern string) []string {
	parts := strings.Split(pattern, "/")
	result := make([]string, 0)
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
