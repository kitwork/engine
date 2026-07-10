package collection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Collection is a tenant-scoped, directory-backed set of Markdown documents.
type Collection struct {
	store *Store
	path  string
	dir   string

	cacheEnabled   bool
	cacheTTL       time.Duration
	persistEnabled bool
	persistTTL     time.Duration
}

func (c *Collection) Path() string { return c.path }

func (c *Collection) SetCache(enabled bool, ttl time.Duration) {
	c.cacheEnabled = enabled
	c.cacheTTL = ttl
}

func (c *Collection) SetPersist(enabled bool, ttl time.Duration) {
	c.persistEnabled = enabled
	c.persistTTL = ttl
}

// List discovers Markdown files without reading frontmatter or body content.
func (c *Collection) List() ([]File, error) {
	return scanMarkdownFiles(c.dir)
}

// Index reads frontmatter only and caches the result by the directory file signature.
func (c *Collection) Index() ([]IndexEntry, error) {
	files, err := c.List()
	if err != nil {
		return nil, err
	}
	signature := directorySignature(files)
	key := c.dir
	if c.cacheEnabled {
		if index, ok := c.store.getIndex(key, signature, c.cacheTTL); ok {
			return index, nil
		}
	}
	persistKey := snapshotKey("index", c.path)
	if c.persistEnabled && c.store.disk != nil {
		if body, ok := c.store.disk.Load(persistKey); ok {
			if index, valid := decodeIndexSnapshot(body, signature); valid {
				if c.cacheEnabled {
					c.store.setIndex(key, signature, index)
				}
				return index, nil
			}
		}
	}

	value, err := c.store.run("index|"+key+"|"+signature, func() (any, error) {
		index := make([]IndexEntry, 0, len(files))
		for _, file := range files {
			meta, err := readFrontMatter(file.path)
			if err != nil {
				return nil, fmt.Errorf("collection: index %s: %w", file.Name, err)
			}
			index = append(index, IndexEntry{File: file, Meta: meta})
		}
		if c.cacheEnabled {
			c.store.setIndex(key, signature, index)
		}
		if c.persistEnabled && c.store.disk != nil {
			body, err := json.Marshal(indexSnapshot{Signature: signature, Index: index})
			if err == nil {
				_ = c.store.disk.Save(persistKey, body, c.persistTTL)
			}
		}
		return index, nil
	})
	if err != nil {
		return nil, err
	}
	return value.([]IndexEntry), nil
}

// Read parses and renders exactly one document.
func (c *Collection) Read(slug string) (*Document, error) {
	file, err := c.resolveDocument(slug)
	if err != nil {
		return nil, err
	}
	key := file.path
	if c.cacheEnabled {
		if document, ok := c.store.getDocument(key, file.signature, c.cacheTTL); ok {
			return document, nil
		}
	}
	persistKey := snapshotKey("document", c.path+"/"+file.Slug)
	if c.persistEnabled && c.store.disk != nil {
		if body, ok := c.store.disk.Load(persistKey); ok {
			if document, valid := decodeDocumentSnapshot(body, file.signature); valid {
				if c.cacheEnabled {
					c.store.setDocument(key, file.signature, document)
				}
				return document, nil
			}
		}
	}

	value, err := c.store.run("document|"+key+"|"+file.signature, func() (any, error) {
		source, err := os.ReadFile(file.path)
		if err != nil {
			return nil, err
		}
		meta, body, err := splitFrontMatter(source)
		if err != nil {
			return nil, fmt.Errorf("collection: read %s: %w", file.Name, err)
		}
		title, _ := meta["title"].(string)
		rendered, toc := renderMarkdown(body, title)
		document := &Document{
			File: file,
			Meta: meta,
			Body: string(body),
			HTML: rendered,
			TOC:  toc,
		}
		if c.cacheEnabled {
			c.store.setDocument(key, file.signature, document)
		}
		if c.persistEnabled && c.store.disk != nil {
			snapshot := documentSnapshot{Signature: file.signature, Document: document}
			if encoded, err := json.Marshal(snapshot); err == nil {
				_ = c.store.disk.Save(persistKey, encoded, c.persistTTL)
			}
		}
		return document, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*Document), nil
}

// Prewarm prepares the index or every document.
func (c *Collection) Prewarm(mode string) (int, error) {
	index, err := c.Index()
	if err != nil {
		return 0, err
	}
	if strings.ToLower(strings.TrimSpace(mode)) != "all" {
		return len(index), nil
	}
	count := 0
	for _, entry := range index {
		if _, err := c.Read(entry.File.Slug); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (c *Collection) resolveDocument(slug string) (File, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" || strings.ContainsAny(slug, `/\`) || slug == "." || slug == ".." {
		return File{}, fmt.Errorf("collection: invalid document slug")
	}
	extension := strings.ToLower(filepath.Ext(slug))
	candidates := []string{slug}
	if extension == "" {
		candidates = []string{slug + ".md", slug + ".markdown"}
	} else if extension != ".md" && extension != ".markdown" {
		return File{}, fmt.Errorf("collection: unsupported document type")
	}
	for _, name := range candidates {
		full := filepath.Join(c.dir, name)
		relative, err := filepath.Rel(c.dir, full)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return File{}, fmt.Errorf("collection: document path denied")
		}
		info, err := os.Stat(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return File{}, err
		}
		if info.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		return File{
			Name:      name,
			Slug:      strings.TrimSuffix(name, ext),
			Extension: ext,
			Size:      info.Size(),
			Modified:  info.ModTime().UTC().Format(time.RFC3339),
			signature: fileSignature(info),
			path:      full,
		}, nil
	}
	return File{}, fmt.Errorf("collection: document %q not found", slug)
}
