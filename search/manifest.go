package search

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	manifestLegacyVersion   = uint32(1)
	manifestVersion         = uint32(2)
	manifestHeaderSize      = 96
	manifestHeaderCRCOffset = 76
	manifestV1EntrySize     = 16
	manifestV2EntrySize     = 48
	manifestMaximumBytes    = 64 << 20
	manifestMaximumSegments = 256
	manifestMaximumName     = 255
	manifestNameDigits      = 20
	manifestNamePrefix      = "manifest-"
	manifestNameSuffix      = ".km"
)

var manifestMagic = [8]byte{'K', 'W', 'M', 'A', 'N', '0', '0', '1'}

type manifestSegment struct {
	name            string
	documents       uint32
	bytes           uint64
	identifierName  string
	identifierBytes uint64
	deletionName    string
	deletionBytes   uint64
	deleted         uint32
}

type indexManifest struct {
	generation uint64
	schemaHash [32]byte
	segments   []manifestSegment
	path       string
}

func manifestFilename(generation uint64) string {
	return fmt.Sprintf("%s%0*d%s", manifestNamePrefix, manifestNameDigits, generation, manifestNameSuffix)
}

func parseManifestFilename(name string) (uint64, bool) {
	wantLength := len(manifestNamePrefix) + manifestNameDigits + len(manifestNameSuffix)
	if len(name) != wantLength || !strings.HasPrefix(name, manifestNamePrefix) || !strings.HasSuffix(name, manifestNameSuffix) {
		return 0, false
	}
	digits := name[len(manifestNamePrefix) : len(name)-len(manifestNameSuffix)]
	generation, err := strconv.ParseUint(digits, 10, 64)
	return generation, err == nil && generation != 0
}

func validArtifactFilename(name, suffix string) bool {
	if name == "" || len(name) > manifestMaximumName || filepath.Base(name) != name || !strings.HasSuffix(name, suffix) {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validSegmentFilename(name string) bool {
	return validArtifactFilename(name, ".ks")
}

func marshalManifest(manifest indexManifest) ([]byte, error) {
	if manifest.generation == 0 {
		return nil, fmt.Errorf("search: manifest generation must be positive")
	}
	if len(manifest.segments) > manifestMaximumSegments {
		return nil, ErrTooManySegments
	}

	body := make([]byte, 0, len(manifest.segments)*96)
	seen := make(map[string]struct{}, len(manifest.segments))
	for index, segment := range manifest.segments {
		if !validSegmentFilename(segment.name) || segment.documents == 0 || segment.bytes < segmentHeaderSize {
			return nil, fmt.Errorf("search: manifest segment %d is invalid", index)
		}
		if segment.identifierName != "" {
			if !validArtifactFilename(segment.identifierName, ".ki") || segment.identifierBytes < identifierIndexHeaderSize {
				return nil, fmt.Errorf("search: manifest segment %d identifier index is invalid", index)
			}
		} else if segment.identifierBytes != 0 {
			return nil, fmt.Errorf("search: manifest segment %d has identifier bytes without a file", index)
		}
		if segment.deleted > segment.documents {
			return nil, fmt.Errorf("search: manifest segment %d deletes too many documents", index)
		}
		if segment.deleted == 0 {
			if segment.deletionName != "" || segment.deletionBytes != 0 {
				return nil, fmt.Errorf("search: manifest segment %d has an empty deletion file", index)
			}
		} else if !validArtifactFilename(segment.deletionName, ".kd") || segment.deletionBytes < deletionHeaderSize {
			return nil, fmt.Errorf("search: manifest segment %d deletion file is invalid", index)
		}
		for _, name := range []string{segment.name, segment.identifierName, segment.deletionName} {
			if name == "" {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				return nil, fmt.Errorf("search: manifest repeats artifact %q", name)
			}
			seen[name] = struct{}{}
		}
		base := len(body)
		body = append(body, make([]byte, manifestV2EntrySize)...)
		binary.LittleEndian.PutUint16(body[base:base+2], uint16(len(segment.name)))
		binary.LittleEndian.PutUint16(body[base+2:base+4], uint16(len(segment.identifierName)))
		binary.LittleEndian.PutUint16(body[base+4:base+6], uint16(len(segment.deletionName)))
		binary.LittleEndian.PutUint32(body[base+8:base+12], segment.documents)
		binary.LittleEndian.PutUint32(body[base+12:base+16], segment.deleted)
		binary.LittleEndian.PutUint64(body[base+16:base+24], segment.bytes)
		binary.LittleEndian.PutUint64(body[base+24:base+32], segment.identifierBytes)
		binary.LittleEndian.PutUint64(body[base+32:base+40], segment.deletionBytes)
		body = append(body, segment.name...)
		body = append(body, segment.identifierName...)
		body = append(body, segment.deletionName...)
		if manifestHeaderSize+len(body) > manifestMaximumBytes {
			return nil, fmt.Errorf("search: manifest exceeds %d bytes", manifestMaximumBytes)
		}
	}

	data := make([]byte, manifestHeaderSize, manifestHeaderSize+len(body))
	copy(data[:8], manifestMagic[:])
	binary.LittleEndian.PutUint32(data[8:12], manifestVersion)
	binary.LittleEndian.PutUint32(data[12:16], manifestHeaderSize)
	binary.LittleEndian.PutUint64(data[16:24], uint64(manifestHeaderSize+len(body)))
	copy(data[24:56], manifest.schemaHash[:])
	binary.LittleEndian.PutUint64(data[56:64], manifest.generation)
	binary.LittleEndian.PutUint32(data[64:68], uint32(len(manifest.segments)))
	binary.LittleEndian.PutUint32(data[72:76], crc32.Checksum(body, crcTable))
	binary.LittleEndian.PutUint32(data[manifestHeaderCRCOffset:manifestHeaderCRCOffset+4], 0)
	binary.LittleEndian.PutUint32(data[manifestHeaderCRCOffset:manifestHeaderCRCOffset+4], crc32.Checksum(data, crcTable))
	return append(data, body...), nil
}

func parseManifest(data []byte) (indexManifest, error) {
	if len(data) < manifestHeaderSize || len(data) > manifestMaximumBytes {
		return indexManifest{}, corruptIndexf("manifest has invalid size %d", len(data))
	}
	if !bytes.Equal(data[:8], manifestMagic[:]) {
		return indexManifest{}, corruptIndexf("invalid manifest magic")
	}
	version := binary.LittleEndian.Uint32(data[8:12])
	if version != manifestLegacyVersion && version != manifestVersion {
		return indexManifest{}, fmt.Errorf("%w: manifest version %d", ErrUnsupportedVersion, version)
	}
	if size := binary.LittleEndian.Uint32(data[12:16]); size != manifestHeaderSize {
		return indexManifest{}, corruptIndexf("manifest header size is %d; expected %d", size, manifestHeaderSize)
	}
	if size := binary.LittleEndian.Uint64(data[16:24]); size != uint64(len(data)) {
		return indexManifest{}, corruptIndexf("manifest records %d bytes; actual size is %d", size, len(data))
	}
	if binary.LittleEndian.Uint32(data[68:72]) != 0 || !allZero(data[80:manifestHeaderSize]) {
		return indexManifest{}, corruptIndexf("manifest contains unsupported header flags")
	}

	wantHeaderCRC := binary.LittleEndian.Uint32(data[manifestHeaderCRCOffset : manifestHeaderCRCOffset+4])
	header := append([]byte(nil), data[:manifestHeaderSize]...)
	binary.LittleEndian.PutUint32(header[manifestHeaderCRCOffset:manifestHeaderCRCOffset+4], 0)
	if got := crc32.Checksum(header, crcTable); got != wantHeaderCRC {
		return indexManifest{}, corruptIndexf("manifest header checksum is %08x; expected %08x", got, wantHeaderCRC)
	}

	body := data[manifestHeaderSize:]
	wantBodyCRC := binary.LittleEndian.Uint32(data[72:76])
	if got := crc32.Checksum(body, crcTable); got != wantBodyCRC {
		return indexManifest{}, corruptIndexf("manifest body checksum is %08x; expected %08x", got, wantBodyCRC)
	}
	generation := binary.LittleEndian.Uint64(data[56:64])
	if generation == 0 {
		return indexManifest{}, corruptIndexf("manifest generation is zero")
	}
	count := uint64(binary.LittleEndian.Uint32(data[64:68]))
	minimumEntrySize := manifestV2EntrySize
	if version == manifestLegacyVersion {
		minimumEntrySize = manifestV1EntrySize
	}
	if count > manifestMaximumSegments || count > uint64(len(body)/minimumEntrySize) {
		return indexManifest{}, corruptIndexf("manifest segment count %d exceeds its body", count)
	}

	manifest := indexManifest{generation: generation, segments: make([]manifestSegment, 0, int(count))}
	copy(manifest.schemaHash[:], data[24:56])
	seen := make(map[string]struct{}, int(count))
	for index := 0; index < int(count); index++ {
		if len(body) < minimumEntrySize {
			return indexManifest{}, corruptIndexf("manifest segment %d is truncated", index)
		}
		nameLength := int(binary.LittleEndian.Uint16(body[:2]))
		identifierLength := 0
		deletionLength := 0
		var documents uint32
		var deleted uint32
		var segmentBytes uint64
		var identifierBytes uint64
		var deletionBytes uint64
		if version == manifestLegacyVersion {
			if binary.LittleEndian.Uint16(body[2:4]) != 0 {
				return indexManifest{}, corruptIndexf("manifest segment %d contains unsupported flags", index)
			}
			documents = binary.LittleEndian.Uint32(body[4:8])
			segmentBytes = binary.LittleEndian.Uint64(body[8:16])
			body = body[manifestV1EntrySize:]
		} else {
			identifierLength = int(binary.LittleEndian.Uint16(body[2:4]))
			deletionLength = int(binary.LittleEndian.Uint16(body[4:6]))
			if binary.LittleEndian.Uint16(body[6:8]) != 0 || binary.LittleEndian.Uint64(body[40:48]) != 0 {
				return indexManifest{}, corruptIndexf("manifest segment %d contains unsupported flags", index)
			}
			documents = binary.LittleEndian.Uint32(body[8:12])
			deleted = binary.LittleEndian.Uint32(body[12:16])
			segmentBytes = binary.LittleEndian.Uint64(body[16:24])
			identifierBytes = binary.LittleEndian.Uint64(body[24:32])
			deletionBytes = binary.LittleEndian.Uint64(body[32:40])
			body = body[manifestV2EntrySize:]
		}
		totalNameLength := nameLength + identifierLength + deletionLength
		if nameLength == 0 || totalNameLength > len(body) || nameLength > manifestMaximumName ||
			identifierLength > manifestMaximumName || deletionLength > manifestMaximumName {
			return indexManifest{}, corruptIndexf("manifest segment %d has an invalid name length", index)
		}
		name := string(body[:nameLength])
		identifierName := string(body[nameLength : nameLength+identifierLength])
		deletionName := string(body[nameLength+identifierLength : totalNameLength])
		body = body[totalNameLength:]
		if !validSegmentFilename(name) || documents == 0 || segmentBytes < segmentHeaderSize || segmentBytes > math.MaxInt64 || deleted > documents {
			return indexManifest{}, corruptIndexf("manifest segment %d is invalid", index)
		}
		if identifierName != "" {
			if !validArtifactFilename(identifierName, ".ki") || identifierBytes < identifierIndexHeaderSize || identifierBytes > math.MaxInt64 {
				return indexManifest{}, corruptIndexf("manifest segment %d identifier index is invalid", index)
			}
		} else if identifierBytes != 0 {
			return indexManifest{}, corruptIndexf("manifest segment %d has identifier bytes without a file", index)
		}
		if deleted == 0 {
			if deletionName != "" || deletionBytes != 0 {
				return indexManifest{}, corruptIndexf("manifest segment %d has an empty deletion file", index)
			}
		} else if !validArtifactFilename(deletionName, ".kd") || deletionBytes < deletionHeaderSize || deletionBytes > math.MaxInt64 {
			return indexManifest{}, corruptIndexf("manifest segment %d deletion file is invalid", index)
		}
		for _, artifact := range []string{name, identifierName, deletionName} {
			if artifact == "" {
				continue
			}
			if _, duplicate := seen[artifact]; duplicate {
				return indexManifest{}, corruptIndexf("manifest repeats artifact %q", artifact)
			}
			seen[artifact] = struct{}{}
		}
		manifest.segments = append(manifest.segments, manifestSegment{
			name: name, documents: documents, bytes: segmentBytes,
			identifierName: identifierName, identifierBytes: identifierBytes,
			deletionName: deletionName, deletionBytes: deletionBytes, deleted: deleted,
		})
	}
	if len(body) != 0 {
		return indexManifest{}, corruptIndexf("manifest has %d trailing bytes", len(body))
	}
	return manifest, nil
}

func readManifest(path string) (indexManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return indexManifest{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return indexManifest{}, err
	}
	if !stat.Mode().IsRegular() || stat.Size() < manifestHeaderSize || stat.Size() > manifestMaximumBytes {
		return indexManifest{}, corruptIndexf("manifest is not a regular bounded file")
	}
	data := make([]byte, int(stat.Size()))
	read, err := file.ReadAt(data, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return indexManifest{}, err
	}
	if read != len(data) {
		return indexManifest{}, corruptIndexf("manifest read %d bytes; expected %d", read, len(data))
	}
	manifest, err := parseManifest(data)
	if err != nil {
		return indexManifest{}, err
	}
	manifest.path = path
	return manifest, nil
}

func readLatestManifest(directory string) (indexManifest, bool, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return indexManifest{}, false, nil
	}
	if err != nil {
		return indexManifest{}, false, err
	}
	var latestGeneration uint64
	var latestName string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		generation, valid := parseManifestFilename(entry.Name())
		if valid && generation > latestGeneration {
			latestGeneration = generation
			latestName = entry.Name()
		}
	}
	if latestGeneration == 0 {
		return indexManifest{}, false, nil
	}
	manifest, err := readManifest(filepath.Join(directory, latestName))
	if err != nil {
		return indexManifest{}, false, err
	}
	if manifest.generation != latestGeneration {
		return indexManifest{}, false, corruptIndexf("manifest filename generation is %d; body generation is %d", latestGeneration, manifest.generation)
	}
	return manifest, true, nil
}

type manifestWriteResult struct {
	path      string
	published bool
}

func writeManifest(
	ctx context.Context,
	directory string,
	manifest indexManifest,
	directorySync func(string) error,
) (manifestWriteResult, error) {
	if ctx == nil {
		return manifestWriteResult{}, fmt.Errorf("search: manifest context is nil")
	}
	if err := ctx.Err(); err != nil {
		return manifestWriteResult{}, err
	}
	data, err := marshalManifest(manifest)
	if err != nil {
		return manifestWriteResult{}, err
	}
	finalPath := filepath.Join(directory, manifestFilename(manifest.generation))
	if _, err := os.Stat(finalPath); err == nil {
		return manifestWriteResult{}, fmt.Errorf("search: manifest generation %d already exists", manifest.generation)
	} else if !errors.Is(err, os.ErrNotExist) {
		return manifestWriteResult{}, err
	}
	temporaryPath, err := temporarySegmentPath(finalPath)
	if err != nil {
		return manifestWriteResult{}, err
	}
	file, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return manifestWriteResult{}, err
	}
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	written, err := io.Copy(file, bytes.NewReader(data))
	if err != nil {
		return manifestWriteResult{}, err
	}
	if written != int64(len(data)) {
		return manifestWriteResult{}, io.ErrShortWrite
	}
	if err := ctx.Err(); err != nil {
		return manifestWriteResult{}, err
	}
	if err := file.Sync(); err != nil {
		return manifestWriteResult{}, err
	}
	if err := file.Close(); err != nil {
		return manifestWriteResult{}, err
	}
	if err := publishFile(temporaryPath, finalPath); err != nil {
		return manifestWriteResult{}, err
	}
	keepTemporary = false
	result := manifestWriteResult{path: finalPath, published: true}
	// A visible manifest is already authoritative. The caller must adopt it even
	// when persisting the directory entry fails, or cleanup could remove its files.
	if err := directorySync(directory); err != nil {
		return result, err
	}
	return result, nil
}

func corruptIndexf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrCorruptIndex, fmt.Sprintf(format, arguments...))
}
