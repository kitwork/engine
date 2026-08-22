package search

import "testing"

func FuzzParseSegmentHeader(f *testing.F) {
	var sections [sectionCount]sectionDescriptor
	for index := range sections {
		sections[index].offset = segmentHeaderSize
	}
	valid := marshalSegmentHeader(segmentHeader{fileSize: segmentHeaderSize, fieldN: 1, sections: sections})
	legacy := marshalSegmentHeader(segmentHeader{
		version: segmentVersionV1, fileSize: segmentHeaderSize, fieldN: 1, sections: sections,
	})
	f.Add(valid[:], int64(segmentHeaderSize))
	f.Add(legacy[:], int64(segmentHeaderSize))
	f.Add([]byte("short"), int64(5))
	f.Fuzz(func(t *testing.T, data []byte, size int64) {
		if len(data) != segmentHeaderSize || size < 0 {
			return
		}
		_, _ = parseSegmentHeader(data, size)
	})
}

func FuzzDecodeDictionaryBlock(f *testing.F) {
	valid, err := encodeDictionaryBlock([]termRecord{{
		key: termKey{term: "alpha"}, documentFreq: 1, postingsOffset: 256, postingsLength: 32,
		maximumTF: 1, minimumNorm: 1,
	}}, segmentVersion)
	if err != nil {
		f.Fatal(err)
	}
	legacy, err := encodeDictionaryBlock([]termRecord{{
		key: termKey{term: "alpha"}, documentFreq: 1, postingsOffset: 256, postingsLength: 24,
	}}, segmentVersionV1)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add(legacy)
	f.Add([]byte{1, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > dictionaryBlockMaxBytes {
			return
		}
		_ = decodeDictionaryBlock(data, segmentVersion, nil)
		_ = decodeDictionaryBlock(data, segmentVersionV1, nil)
	})
}

func FuzzParseManifest(f *testing.F) {
	valid, err := marshalManifest(indexManifest{
		generation: 1,
		segments: []manifestSegment{{
			name: "segment-00000000000000000001-seed.ks", documents: 1, bytes: segmentHeaderSize,
		}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte("short"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > manifestMaximumBytes {
			return
		}
		_, _ = parseManifest(data)
	})
}
