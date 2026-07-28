// Package file provides tenant boundary-safe file utilities, MIME detection, and Data URI encoding/decoding.
package file

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/kitwork/engine/utilities/safepath"
)

// ResolvePath checks that targetRelPath stays within baseDir boundary.
func ResolvePath(baseDir, targetRelPath string) (string, error) {
	return safepath.Resolve(baseDir, targetRelPath)
}

// DetectMIME detects the MIME content type from a file extension.
func DetectMIME(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

// Base64DataURI converts raw bytes and a file extension into a Data URI.
func Base64DataURI(content []byte, ext string) string {
	b64 := base64.StdEncoding.EncodeToString(content)
	mime := DetectMIME(ext)
	return fmt.Sprintf("data:%s;base64,%s", mime, b64)
}

// DecodeDataURI extracts and decodes bytes from a Base64 Data URI ("data:...;base64,...").
func DecodeDataURI(strVal string) ([]byte, bool) {
	if strings.HasPrefix(strVal, "data:") && strings.Contains(strVal, ";base64,") {
		parts := strings.SplitN(strVal, ";base64,", 2)
		if len(parts) == 2 {
			decoded, err := base64.StdEncoding.DecodeString(parts[1])
			if err == nil {
				return decoded, true
			}
		}
	}
	return nil, false
}
