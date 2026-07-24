package collection

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

func splitFrontMatter(source []byte) (map[string]any, []byte, error) {
	source = bytes.TrimPrefix(source, utf8BOM)
	normalized := strings.ReplaceAll(string(source), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]any{}, []byte(normalized), nil
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, nil, fmt.Errorf("collection: unclosed frontmatter")
	}

	meta := map[string]any{}
	header := strings.Join(lines[1:end], "\n")
	if strings.TrimSpace(header) != "" {
		if err := yaml.Unmarshal([]byte(header), &meta); err != nil {
			return nil, nil, fmt.Errorf("collection: parse frontmatter: %w", err)
		}
	}
	body := strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return meta, []byte(body), nil
}

// readFrontMatter reads only the YAML header, never the Markdown body.
func readFrontMatter(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	}

	first := strings.TrimPrefix(scanner.Text(), "\ufeff")
	if strings.TrimSpace(first) != "---" {
		return map[string]any{}, nil
	}

	var header strings.Builder
	closed := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		header.WriteString(line)
		header.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !closed {
		return nil, fmt.Errorf("collection: unclosed frontmatter")
	}

	meta := map[string]any{}
	if strings.TrimSpace(header.String()) != "" {
		if err := yaml.Unmarshal([]byte(header.String()), &meta); err != nil {
			return nil, fmt.Errorf("collection: parse frontmatter: %w", err)
		}
	}
	return meta, nil
}
