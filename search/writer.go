package search

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	defaultFlushThreshold = int64(64 << 20)
	defaultMaxDocument    = 8 << 20
	defaultMaxIdentifier  = 4 << 10
	defaultMaxTerm        = 256
	defaultMaxFieldTokens = 1 << 20
)

// BuildOptions bound the inputs accepted by one immutable segment builder.
// FlushThresholdBytes is a soft accounting threshold, not a Go heap limit.
type BuildOptions struct {
	FlushThresholdBytes int64
	MaxDocuments        uint32
	MaxDocumentBytes    int
	MaxIdentifierBytes  int
	MaxTermBytes        int
	MaxTokensPerField   int
	SkipLongTerms       bool
}

func normalizeBuildOptions(options BuildOptions) (BuildOptions, error) {
	if options.FlushThresholdBytes == 0 {
		options.FlushThresholdBytes = defaultFlushThreshold
	}
	if options.MaxDocuments == 0 {
		options.MaxDocuments = math.MaxUint32
	}
	if options.MaxDocumentBytes == 0 {
		options.MaxDocumentBytes = defaultMaxDocument
	}
	if options.MaxIdentifierBytes == 0 {
		options.MaxIdentifierBytes = defaultMaxIdentifier
	}
	if options.MaxTermBytes == 0 {
		options.MaxTermBytes = defaultMaxTerm
	}
	if options.MaxTokensPerField == 0 {
		options.MaxTokensPerField = defaultMaxFieldTokens
	}
	if options.FlushThresholdBytes < 1 || options.MaxDocumentBytes < 1 ||
		options.MaxIdentifierBytes < 1 || options.MaxTermBytes < 1 || options.MaxTokensPerField < 1 {
		return BuildOptions{}, fmt.Errorf("search: build limits must be positive")
	}
	if options.MaxTermBytes > math.MaxUint16 || uint64(options.MaxTokensPerField) > math.MaxUint32 {
		return BuildOptions{}, fmt.Errorf("search: term or token limit exceeds segment format range")
	}
	return options, nil
}

type termPostings struct {
	items []posting
}

// Builder accumulates one bounded immutable segment. It is not safe for
// concurrent use. Callers should write the segment and retry on ErrSegmentFull.
type Builder struct {
	schema         Schema
	version        uint32
	options        BuildOptions
	identifiers    []string
	identifierSet  map[string]struct{}
	norms          [][]uint32
	totalTokens    []uint64
	terms          map[termKey]*termPostings
	estimatedBytes int64
	sealed         bool
}

// NewBuilder creates an empty segment builder.
func NewBuilder(schema Schema, options BuildOptions) (*Builder, error) {
	if !schema.valid() {
		return nil, fmt.Errorf("search: invalid schema")
	}
	normalized, err := normalizeBuildOptions(options)
	if err != nil {
		return nil, err
	}
	return &Builder{
		schema:        schema,
		version:       segmentVersion,
		options:       normalized,
		identifierSet: make(map[string]struct{}),
		norms:         make([][]uint32, len(schema.fields)),
		totalTokens:   make([]uint64, len(schema.fields)),
		terms:         make(map[termKey]*termPostings),
	}, nil
}

// DocumentCount returns the number of accepted documents.
func (builder *Builder) DocumentCount() uint32 {
	return uint32(len(builder.identifiers))
}

// EstimatedBytes returns the builder's conservative flush accounting value.
func (builder *Builder) EstimatedBytes() int64 {
	return builder.estimatedBytes
}

// Add analyzes and atomically adds one document to the current segment.
func (builder *Builder) Add(document Document) error {
	return builder.AddContext(context.Background(), document)
}

// AddContext analyzes and atomically adds one document with cancellation.
func (builder *Builder) AddContext(ctx context.Context, document Document) error {
	if ctx == nil {
		return fmt.Errorf("search: builder context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if builder.sealed {
		return ErrBuilderSealed
	}
	if uint64(len(builder.identifiers)) >= uint64(builder.options.MaxDocuments) {
		return ErrSegmentFull
	}
	if document.ID == "" || !utf8.ValidString(document.ID) || len(document.ID) > builder.options.MaxIdentifierBytes {
		return fmt.Errorf("search: document identifier is invalid or exceeds %d bytes", builder.options.MaxIdentifierBytes)
	}
	if _, duplicate := builder.identifierSet[document.ID]; duplicate {
		return fmt.Errorf("%w: %q", ErrPendingDocument, document.ID)
	}

	documentBytes := int64(len(document.ID))
	if documentBytes > int64(builder.options.MaxDocumentBytes) {
		return fmt.Errorf("search: document %q exceeds %d bytes", document.ID, builder.options.MaxDocumentBytes)
	}
	for name, text := range document.Fields {
		if _, _, exists := builder.schema.field(name); !exists {
			return fmt.Errorf("search: document %q contains unknown field %q", document.ID, name)
		}
		documentBytes += int64(len(name)) + int64(len(text))
		if documentBytes > int64(builder.options.MaxDocumentBytes) {
			return fmt.Errorf("search: document %q exceeds %d bytes", document.ID, builder.options.MaxDocumentBytes)
		}
	}

	fieldTerms := make([]map[string]*posting, len(builder.schema.fields))
	fieldLengths := make([]uint32, len(builder.schema.fields))
	additionalBytes := documentBytes + int64(len(builder.schema.fields))*4
	for fieldID, field := range builder.schema.fields {
		text := document.Fields[field.Name]
		frequencies := make(map[string]*posting)
		var tokenErr error
		err := field.Analyzer.Analyze(ctx, text, func(token Token) bool {
			if token.Term == "" || !utf8.ValidString(token.Term) {
				tokenErr = fmt.Errorf("search: analyzer %q emitted an invalid term", field.Analyzer.Identifier())
				return false
			}
			if len(token.Term) > builder.options.MaxTermBytes {
				if builder.options.SkipLongTerms {
					return true
				}
				tokenErr = fmt.Errorf("search: analyzer %q emitted a term exceeding %d bytes", field.Analyzer.Identifier(), builder.options.MaxTermBytes)
				return false
			}
			if fieldLengths[fieldID] >= uint32(builder.options.MaxTokensPerField) {
				tokenErr = fmt.Errorf("search: field %q exceeds %d tokens", field.Name, builder.options.MaxTokensPerField)
				return false
			}
			fieldLengths[fieldID]++
			entry := frequencies[token.Term]
			if entry == nil {
				term := strings.Clone(token.Term)
				entry = &posting{}
				frequencies[term] = entry
				additionalBytes += int64(len(term) + 56)
			}
			entry.frequency++
			entry.positions = append(entry.positions, token.Position)
			additionalBytes += 8
			return true
		})
		if err != nil {
			return fmt.Errorf("search: analyze field %q: %w", field.Name, err)
		}
		if tokenErr != nil {
			return tokenErr
		}
		fieldTerms[fieldID] = frequencies
	}

	if additionalBytes > math.MaxInt64-builder.estimatedBytes ||
		(len(builder.identifiers) > 0 && additionalBytes > builder.options.FlushThresholdBytes-builder.estimatedBytes) {
		return ErrSegmentFull
	}
	documentID := uint32(len(builder.identifiers))
	builder.identifiers = append(builder.identifiers, strings.Clone(document.ID))
	builder.identifierSet[builder.identifiers[len(builder.identifiers)-1]] = struct{}{}
	for fieldID, frequencies := range fieldTerms {
		length := fieldLengths[fieldID]
		builder.norms[fieldID] = append(builder.norms[fieldID], length)
		builder.totalTokens[fieldID] += uint64(length)
		for term, frequency := range frequencies {
			key := termKey{field: uint16(fieldID), term: term}
			list := builder.terms[key]
			if list == nil {
				list = &termPostings{}
				builder.terms[key] = list
			}
			list.items = append(list.items, posting{
				document: documentID, frequency: frequency.frequency, norm: length,
				positions: append([]uint32(nil), frequency.positions...),
			})
		}
	}
	builder.estimatedBytes += additionalBytes
	return nil
}

// SegmentInfo describes a persisted segment.
type SegmentInfo struct {
	Path      string
	Documents uint32
	Fields    uint16
	Bytes     int64
}

// Write atomically publishes the builder as a new immutable segment path.
func (builder *Builder) Write(ctx context.Context, path string) (SegmentInfo, error) {
	if builder.sealed {
		return SegmentInfo{}, ErrBuilderSealed
	}
	if ctx == nil {
		return SegmentInfo{}, fmt.Errorf("search: write context is nil")
	}
	if err := ctx.Err(); err != nil {
		return SegmentInfo{}, err
	}
	if path == "" {
		return SegmentInfo{}, fmt.Errorf("search: segment path is empty")
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		return SegmentInfo{}, fmt.Errorf("search: segment path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return SegmentInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return SegmentInfo{}, err
	}
	temporary, err := temporarySegmentPath(path)
	if err != nil {
		return SegmentInfo{}, err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return SegmentInfo{}, err
	}
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(make([]byte, segmentHeaderSize)); err != nil {
		return SegmentInfo{}, err
	}

	var sections [sectionCount]sectionDescriptor
	sections[sectionFieldStats], err = writeSection(file, func(destination io.Writer) error {
		var encoded [8]byte
		for _, total := range builder.totalTokens {
			binary.LittleEndian.PutUint64(encoded[:], total)
			if _, err := destination.Write(encoded[:]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	keys := make([]termKey, 0, len(builder.terms))
	for key := range builder.terms {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].field != keys[j].field {
			return keys[i].field < keys[j].field
		}
		return keys[i].term < keys[j].term
	})
	records := make([]termRecord, 0, len(keys))
	sections[sectionPostings], err = writeSection(file, func(destination io.Writer) error {
		sectionStart, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr != nil {
			return seekErr
		}
		var relative uint64
		for index, key := range keys {
			if index&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			items := builder.terms[key].items
			listInfo, err := writePostingList(destination, items, builder.version, builder.version >= segmentVersion)
			if err != nil {
				return err
			}
			records = append(records, termRecord{
				key: key, documentFreq: uint32(len(items)),
				postingsOffset: uint64(sectionStart) + relative, postingsLength: listInfo.bytes,
				maximumTF: listInfo.maximumTF, minimumNorm: listInfo.minimumNorm,
			})
			relative += listInfo.bytes
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	indexes := make([]dictionaryBlockIndex, 0, (len(records)+dictionaryBlockMaxEntries-1)/dictionaryBlockMaxEntries)
	sections[sectionDictionaryBlocks], err = writeSection(file, func(destination io.Writer) error {
		sectionStart, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr != nil {
			return seekErr
		}
		var relative uint64
		for start := 0; start < len(records); {
			end := min(start+dictionaryBlockMaxEntries, len(records))
			for end > start+1 && records[end-1].key.field != records[start].key.field {
				end--
			}
			block, err := encodeDictionaryBlock(records[start:end], builder.version)
			if err != nil {
				return err
			}
			if _, err := destination.Write(block); err != nil {
				return err
			}
			indexes = append(indexes, dictionaryBlockIndex{
				field: records[start].key.field, firstTerm: records[start].key.term,
				blockOffset: uint64(sectionStart) + relative, blockLength: uint32(len(block)),
				blockCRC: dictionaryBlockChecksum(block),
			})
			relative += uint64(len(block))
			start = end
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	sections[sectionDictionaryIndex], err = writeSection(file, func(destination io.Writer) error {
		encoded, err := encodeDictionaryIndex(indexes)
		if err != nil {
			return err
		}
		_, err = destination.Write(encoded)
		return err
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	sections[sectionNorms], err = writeSection(file, func(destination io.Writer) error {
		buffer := make([]byte, 0, 32<<10)
		for fieldID, norms := range builder.norms {
			if len(norms) != len(builder.identifiers) {
				return fmt.Errorf("search: field %d norm count is inconsistent", fieldID)
			}
			for documentID, norm := range norms {
				buffer = binary.LittleEndian.AppendUint32(buffer, norm)
				if len(buffer) >= 32<<10 {
					if _, err := destination.Write(buffer); err != nil {
						return err
					}
					buffer = buffer[:0]
				}
				if documentID&8191 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
			}
		}
		if len(buffer) > 0 {
			_, err := destination.Write(buffer)
			return err
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	sections[sectionStoredOffsets], err = writeSection(file, func(destination io.Writer) error {
		var encoded [8]byte
		var offset uint64
		binary.LittleEndian.PutUint64(encoded[:], offset)
		if _, err := destination.Write(encoded[:]); err != nil {
			return err
		}
		for _, identifier := range builder.identifiers {
			offset += uint64(len(identifier))
			binary.LittleEndian.PutUint64(encoded[:], offset)
			if _, err := destination.Write(encoded[:]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	sections[sectionStoredData], err = writeSection(file, func(destination io.Writer) error {
		for index, identifier := range builder.identifiers {
			if index&8191 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(destination, identifier); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	fileSize, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return SegmentInfo{}, err
	}
	header := marshalSegmentHeader(segmentHeader{
		version: builder.version, fileSize: uint64(fileSize), schemaHash: builder.schema.fingerprint,
		documentN: uint32(len(builder.identifiers)), fieldN: uint16(len(builder.schema.fields)), sections: sections,
	})
	if _, err := file.WriteAt(header[:], 0); err != nil {
		return SegmentInfo{}, err
	}
	if err := file.Sync(); err != nil {
		return SegmentInfo{}, err
	}
	if err := file.Close(); err != nil {
		return SegmentInfo{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return SegmentInfo{}, fmt.Errorf("search: segment path appeared while writing: %s", path)
	} else if !os.IsNotExist(err) {
		return SegmentInfo{}, err
	}
	if err := publishFile(temporary, path); err != nil {
		return SegmentInfo{}, err
	}
	keepTemporary = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return SegmentInfo{}, err
	}
	builder.sealed = true
	return SegmentInfo{Path: path, Documents: uint32(len(builder.identifiers)), Fields: uint16(len(builder.schema.fields)), Bytes: fileSize}, nil
}

type sectionWriter struct {
	destination io.Writer
	checksum    hash.Hash32
	length      uint64
}

func (writer *sectionWriter) Write(data []byte) (int, error) {
	written, err := writer.destination.Write(data)
	if written > 0 {
		_, _ = writer.checksum.Write(data[:written])
		writer.length += uint64(written)
	}
	return written, err
}

func writeSection(file *os.File, write func(io.Writer) error) (sectionDescriptor, error) {
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return sectionDescriptor{}, err
	}
	stream := &sectionWriter{destination: file, checksum: crc32.New(crcTable)}
	if err := write(stream); err != nil {
		return sectionDescriptor{}, err
	}
	return sectionDescriptor{offset: uint64(offset), length: stream.length, checksum: stream.checksum.Sum32()}, nil
}

func temporarySegmentPath(path string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return path + ".tmp-" + hex.EncodeToString(random[:]), nil
}
