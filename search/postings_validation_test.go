package search

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestPostingAdvanceVarintWidthsAndVersions(t *testing.T) {
	for _, version := range []uint32{segmentVersionV1, segmentVersionV2, segmentVersion} {
		for _, positions := range []bool{false, true} {
			if positions && version != segmentVersion {
				continue
			}
			t.Run(fmt.Sprintf("v%d/positions=%t", version, positions), func(t *testing.T) {
				postings := make([]posting, 385)
				gaps := []uint32{1, 127, 128, 16383, 16384, 1 << 21}
				var document uint32
				for i := range postings {
					document += gaps[i%len(gaps)]
					frequency := uint32(1 + i%130)
					item := posting{document: document, frequency: frequency, norm: frequency + 1}
					if positions {
						for j := uint32(0); j < frequency; j++ {
							item.positions = append(item.positions, j*128)
						}
					}
					postings[i] = item
				}
				var encoded bytes.Buffer
				info, err := writePostingList(&encoded, postings, version, positions)
				if err != nil {
					t.Fatal(err)
				}
				create := func() *postingIterator {
					t.Helper()
					iterator, err := newPostingIterator(context.Background(), bytes.NewReader(encoded.Bytes()), version,
						termRecord{documentFreq: uint32(len(postings)), postingsLength: info.bytes, maximumTF: info.maximumTF, minimumNorm: info.minimumNorm},
						document+1, sectionDescriptor{length: info.bytes}, positions)
					if err != nil {
						t.Fatal(err)
					}
					return iterator
				}
				iterator := create()
				for _, want := range postings {
					more, err := iterator.Next()
					if err != nil || !more {
						t.Fatalf("next: %t %v", more, err)
					}
					got, frequency, _ := iterator.Current()
					if got != want.document || frequency != want.frequency || (positions && !slices.Equal(iterator.CurrentPositions(), want.positions)) {
						t.Fatalf("posting %d mismatch", want.document)
					}
				}
				if more, err := iterator.Next(); more || err != nil {
					t.Fatalf("end: %t %v", more, err)
				}
				iterator = create()
				for i := 0; i < len(postings); i += 17 {
					want := postings[i]
					for _, target := range []uint32{want.document - 1, want.document} {
						more, err := iterator.Advance(target)
						got, frequency, _ := iterator.Current()
						index := sort.Search(len(postings), func(i int) bool { return postings[i].document >= target })
						if err != nil || !more || got != postings[index].document || frequency != postings[index].frequency {
							t.Fatalf("advance %d = %d/%d %t %v", target, got, frequency, more, err)
						}
					}
				}
				if more, err := iterator.Advance(math.MaxUint32); more || err != nil {
					t.Fatalf("advance end: %t %v", more, err)
				}
			})
		}
	}
}

func TestPostingPayloadRejectsEscapedDocumentBeforeNormLookup(t *testing.T) {
	data := []byte{0, 1, 2, 1}
	header := postingBlockHeader{count: 2, firstDocument: 0, lastDocument: 1, maximumTF: 1, minimumNorm: 1, payloadLength: uint32(len(data)), payloadCRC: crc32.Checksum(data, crcTable)}
	iterator := postingIterator{ctx: context.Background(), file: bytes.NewReader(data), version: segmentVersion, end: uint64(len(data)), documentCount: 2, norms: []uint32{1, 1}}
	if _, err := iterator.decodeBlock(header); !errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("expected corrupt document bounds, got %v", err)
	}
}

func TestPostingPayloadRejectsImpossibleFrequencyBeforeAllocation(t *testing.T) {
	data := binary.AppendUvarint([]byte{0}, 1<<18)
	data = append(data, 0)
	header := postingBlockHeader{count: 1, flags: postingBlockFlagPositions, maximumTF: 1 << 18, minimumNorm: 1 << 18, payloadLength: uint32(len(data)), payloadCRC: crc32.Checksum(data, crcTable)}
	iterator := postingIterator{ctx: context.Background(), file: bytes.NewReader(data), version: segmentVersion, end: uint64(len(data)), documentCount: 1, wantPositions: true}
	if _, err := iterator.decodeBlock(header); !errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("expected corrupt position count, got %v", err)
	}
	if len(iterator.positions) != 0 && cap(iterator.positions[0]) != 0 {
		t.Fatalf("allocated %d positions from an impossible frequency", cap(iterator.positions[0]))
	}
}

// The oracle uses only the stdlib decoder and scalar range checks, not the
// posting iterator. CRC is recomputed so fuzzing reaches payloads.
func FuzzPostingPayloadAgainstScalar(f *testing.F) {
	f.Add([]byte{0, 1, 9, 2}, uint8(2), uint32(10), uint32(19), uint32(2), false)
	f.Add([]byte{0, 1, 128, 1, 9, 2, 0, 128, 1}, uint8(2), uint32(10), uint32(19), uint32(2), true)
	f.Add([]byte{128, 0, 1}, uint8(1), uint32(0), uint32(0), uint32(1), false)
	f.Add([]byte{0, 128}, uint8(1), uint32(0), uint32(0), uint32(1), false)
	f.Add([]byte{0, 255, 255, 255, 255, 255, 255, 255, 255, 255, 2}, uint8(1), uint32(0), uint32(0), uint32(1), false)
	f.Add([]byte{0, 1, 2, 1}, uint8(2), uint32(0), uint32(1), uint32(1), false)
	f.Add([]byte{0, 128, 128, 16, 0}, uint8(1), uint32(0), uint32(0), uint32(1<<18), true)
	f.Fuzz(func(t *testing.T, data []byte, count uint8, first, last, maximum uint32, positions bool) {
		if len(data) > 4096 || count == 0 || count > postingBlockDocuments {
			return
		}
		header := postingBlockHeader{count: uint16(count), firstDocument: first, lastDocument: last, maximumTF: maximum, payloadLength: uint32(len(data)), payloadCRC: crc32.Checksum(data, crcTable)}
		if positions {
			header.flags = postingBlockFlagPositions
		}
		iterator := postingIterator{ctx: context.Background(), file: bytes.NewReader(data), version: segmentVersion, end: uint64(len(data)), minimumNorm: math.MaxUint32, wantPositions: positions}
		_, err := iterator.decodeBlock(header)
		documents, frequencies, valid := scalarPostingPayload(data, header)
		if (err == nil) != valid {
			t.Fatalf("decoder agreement: err=%v valid=%t data=%x header=%+v", err, valid, data, header)
		}
		if valid && (!slices.Equal(iterator.documents[:count], documents) || !slices.Equal(iterator.frequencies[:count], frequencies)) {
			t.Fatal("decoded payload changed")
		}
	})
}

func scalarPostingPayload(data []byte, header postingBlockHeader) ([]uint32, []uint32, bool) {
	var documents, frequencies []uint32
	previous, maximum := header.firstDocument, uint32(0)
	for i := 0; i < int(header.count); i++ {
		gap, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, nil, false
		}
		data = data[n:]
		frequency, n := binary.Uvarint(data)
		if n <= 0 || frequency == 0 || frequency > math.MaxUint32 {
			return nil, nil, false
		}
		data = data[n:]
		if i == 0 && gap != 0 {
			return nil, nil, false
		}
		if i > 0 {
			if gap == 0 || gap > uint64(math.MaxUint32-previous) {
				return nil, nil, false
			}
			previous += uint32(gap)
		}
		if header.flags&postingBlockFlagPositions != 0 {
			var position uint32
			for j := uint64(0); j < frequency; j++ {
				gap, n := binary.Uvarint(data)
				if n <= 0 {
					return nil, nil, false
				}
				data = data[n:]
				if gap > uint64(math.MaxUint32-position) || (j > 0 && gap == 0) {
					return nil, nil, false
				}
				position += uint32(gap)
			}
		}
		documents = append(documents, previous)
		frequencies = append(frequencies, uint32(frequency))
		maximum = max(maximum, uint32(frequency))
	}
	return documents, frequencies, len(data) == 0 && previous == header.lastDocument && maximum == header.maximumTF
}

func TestMultiFieldTraversalMatchesDocumentWiseOracle(t *testing.T) {
	schema := productSchema(t)
	random := rand.New(rand.NewSource(33793))
	documents := make([]Document, 2048)
	for i := range documents {
		fields := map[string]string{}
		for _, field := range []string{"title", "body"} {
			for _, term := range []string{"alpha", "beta", "gamma"} {
				if random.Intn(7) == 0 || (term == "alpha" && i < 300) || (term == "beta" && i > 1800) {
					fields[field] += strings.Repeat(term+" ", 1+random.Intn(4))
				}
			}
		}
		documents[i] = Document{ID: fmt.Sprintf("merchant%d/%04d", i%2, i), Fields: fields}
	}
	segment := buildTestSegment(t, schema, documents)
	defer segment.Close()
	ctx := context.Background()
	for _, terms := range []string{"alpha beta", "alpha gamma", "beta gamma", "alpha beta gamma"} {
		for _, prefix := range []string{"", "merchant1/"} {
			query := MatchQuery{Fields: []string{"title", "body"}, Text: terms, IdentifierPrefix: prefix}
			prepared, err := prepareMultiMatchQuery(ctx, schema, query, SearchOptions{Limit: 17})
			if err != nil {
				t.Fatal(err)
			}
			records, frequencies, _, err := segment.prepareMultiTermRecords(ctx, prepared, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			averages, err := segment.multiFieldAverageLengths(prepared, uint64(len(documents)), nil)
			if err != nil {
				t.Fatal(err)
			}
			cursors := make([]multiTermCursor, len(records))
			for i, record := range records {
				cursors[i], err = newMultiTermCursor(ctx, segment, prepared, record, inverseDocumentFrequency(float64(len(documents)), float64(frequencies[i])), averages)
				if err != nil {
					t.Fatal(err)
				}
			}
			sort.Slice(cursors, func(i, j int) bool {
				return cursors[i].records.documentFrequency < cursors[j].records.documentFrequency
			})
			var want []Hit
			for document := uint32(0); document < uint32(len(documents)); document++ {
				matched, score := true, 0.0
				for i := range cursors {
					more, err := cursors[i].union.Advance(document)
					if err != nil {
						t.Fatal(err)
					}
					if !more || cursors[i].union.document != document {
						matched = false
						break
					}
					part, err := cursors[i].score(document, prepared.options)
					if err != nil {
						t.Fatal(err)
					}
					score += part
				}
				if matched && strings.HasPrefix(documents[document].ID, prefix) {
					want = append(want, Hit{ID: documents[document].ID, Score: score, InternalID: document, Ordinal: uint64(document)})
				}
			}
			sort.Slice(want, func(i, j int) bool {
				if want[i].Score != want[j].Score {
					return want[i].Score > want[j].Score
				}
				return want[i].InternalID < want[j].InternalID
			})
			first, err := segment.Search(ctx, query, prepared.options)
			if err != nil {
				t.Fatal(err)
			}
			assertOrderedHitsEqual(t, first, want[:min(len(want), 17)])
			if len(first) == 17 {
				last := first[len(first)-1]
				options := prepared.options
				options.After = &SearchAfter{Score: last.Score, Ordinal: uint64(last.InternalID)}
				next, err := segment.Search(ctx, query, options)
				if err != nil {
					t.Fatal(err)
				}
				assertOrderedHitsEqual(t, next, want[17:min(len(want), 34)])
			}
		}
	}
}
