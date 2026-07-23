package collection

// File describes one source file without reading its body.
type File struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Extension string `json:"extension"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
	signature string
	path      string
}

// Signature exposes the freshness key (size + mtime in NANOseconds) for derived indexes — Modified
// alone is second-granular, too coarse to notice a same-second same-size edit.
func (f File) Signature() string { return f.signature }

// Heading is one entry in a rendered Markdown table of contents.
type Heading struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Level int    `json:"level"`
}

// IndexEntry is the lightweight representation used by collection.index().
// Meta stays dynamic: collections never impose a content-specific Go struct.
type IndexEntry struct {
	File File           `json:"file"`
	Meta map[string]any `json:"meta"`
}

// Document is the generic envelope returned by collection.read().
type Document struct {
	File File           `json:"file"`
	Meta map[string]any `json:"meta"`
	Body string         `json:"body"`
	HTML string         `json:"html"`
	TOC  []Heading      `json:"toc"`
}
