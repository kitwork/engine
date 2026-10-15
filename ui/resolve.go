package ui

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
)

// Site is one site folder (a domain under an identity) written in Kitwork UI.
type Site struct {
	// Root is the site folder: apps/<identity>/<domain>.
	Root string
	// Target is the build target the page is compiled for: "website" today. Target prefixes
	// (website:, mobile:, ios: …) are decided against it at compile time.
	Target string
	// Read loads a source file; nil reads from disk. Tests and the engine's snapshot plug in here.
	Read func(path string) ([]byte, error)
	// Palette is the route's colour chain (router.colors of the site and of every branch down to the
	// route, over Kit Design's defaults): the tokens a colour may name (color="brand", the ink of
	// color="ink-30"). nil is Kit Design's default palette alone.
	Palette *stylesheet.Palette
	// Reload is router.reload: true loads every page whole. By default (false) a link moves to the
	// next page without a reload, and every page has a progressbar: the one page.ui writes, else one
	// at the top.
	Reload bool
	// Contrast is router.contrast of the route: how a pair of colours short of WCAG 2.2 AA is told
	// (contrast.go). "" and "warning" (the default) make it a warning (Page.Warnings), the page
	// rendering as written; "strict" makes it a compile error. Any other word is an error.
	Contrast string
	// Language is router.language of the route (the nearest folder that says one), "" when none does.
	// A language written right to left (ar, he, fa, ur, and their regional forms) makes a physical left
	// or right attribute a warning: left and right never flip (classes.go, checkSides).
	Language string
}

func (s *Site) target() string {
	if s.Target == "" {
		return "website"
	}
	return s.Target
}

func (s *Site) read(path string) ([]byte, error) {
	if s.Read != nil {
		return s.Read(path)
	}
	return os.ReadFile(path)
}

// readHead reads at most limit bytes from the start of a file: what tells an image's size, without
// reading a whole photo.
func (s *Site) readHead(path string, limit int) ([]byte, error) {
	if s.Read != nil {
		data, err := s.Read(path)
		if len(data) > limit {
			data = data[:limit]
		}
		return data, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data := make([]byte, limit)
	n, err := io.ReadFull(f, data)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil
	}
	return data[:n], err
}

func (s *Site) exists(path string) bool {
	if s.Read != nil {
		_, err := s.Read(path)
		return err == nil
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// upward lists folders from dir up to and including the site root, nearest first.
func (s *Site) upward(dir string) []string {
	root := filepath.Clean(s.Root)
	dir = filepath.Clean(dir)
	var out []string
	for {
		out = append(out, dir)
		if dir == root {
			return out
		}
		parent := filepath.Dir(dir)
		if parent == dir || !strings.HasPrefix(parent, root) {
			return out
		}
		dir = parent
	}
}

// find returns the nearest <name>.ui walking up from dir to the site root, or "".
func (s *Site) find(dir, name string) string {
	for _, folder := range s.upward(dir) {
		candidate := filepath.Join(folder, name+".ui")
		if s.exists(candidate) {
			return candidate
		}
	}
	return ""
}

// parseFile reads and parses one .ui file.
func (s *Site) parseFile(path string) ([]*Node, error) {
	source, err := s.read(path)
	if err != nil {
		return nil, err
	}
	rel, relErr := filepath.Rel(s.Root, path)
	if relErr != nil {
		rel = path
	}
	return Parse(filepath.ToSlash(rel), source)
}

// singleRoot returns the one element at the top of a file, ignoring whitespace and comments.
func singleRoot(nodes []*Node, file string) (*Node, error) {
	var root *Node
	for _, n := range nodes {
		if n.IsText() {
			if strings.TrimSpace(n.Text) != "" {
				return nil, &Error{File: file, Line: n.Line, Column: n.Column, Message: "chữ nằm ngoài thẻ gốc của file"}
			}
			continue
		}
		if root != nil {
			return nil, &Error{File: file, Line: n.Line, Column: n.Column, Message: "file chỉ được có một thẻ gốc"}
		}
		root = n
	}
	if root == nil {
		return nil, &Error{File: file, Line: 1, Column: 1, Message: "file rỗng, không có thẻ gốc"}
	}
	return root, nil
}
