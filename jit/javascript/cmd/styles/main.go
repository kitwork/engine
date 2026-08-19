package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	kitcss "github.com/kitwork/engine/jit/css"
)

func main() {
	output := flag.String("output", "", "generated stylesheet path")
	canonicalDir := flag.String("canonical-dir", "", "directory for an immutable content-addressed stylesheet")
	flag.Parse()
	if (*output == "") == (*canonicalDir == "") || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: styles (-output kitjs.examples.css | -canonical-dir examples) page.html [page.html ...]")
		os.Exit(2)
	}

	html := make([]string, 0, flag.NArg())
	for _, path := range flag.Args() {
		source, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kitjs styles: read %q: %v\n", path, err)
			os.Exit(1)
		}
		html = append(html, string(source))
	}
	stylesheet := kitcss.GenerateSiteCSS(nil, html...)
	if stylesheet == "" {
		fmt.Fprintln(os.Stderr, errors.New("kitjs styles: no Tailwind utility candidates found"))
		os.Exit(1)
	}
	content := []byte(stylesheet)
	path := *output
	immutable := *canonicalDir != ""
	if immutable {
		if err := os.MkdirAll(*canonicalDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "kitjs styles: create %q: %v\n", *canonicalDir, err)
			os.Exit(1)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		path = filepath.Join(*canonicalDir, "kitjs.examples."+hash+".css")
	}
	if err := writeStylesheet(path, content, immutable); err != nil {
		fmt.Fprintf(os.Stderr, "kitjs styles: write %q: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("KitJS example CSS %s %d bytes\n", path, len(content))
}

func writeStylesheet(path string, content []byte, immutable bool) error {
	if !immutable {
		return os.WriteFile(path, content, 0o644)
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	written, err := io.Copy(file, bytes.NewReader(content))
	if err != nil || written != int64(len(content)) {
		_ = file.Close()
		if err != nil {
			return err
		}
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, content) {
			return errors.New("canonical stylesheet already exists with different bytes")
		}
	}
	return nil
}
