package search

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"sort"
)

const (
	identifierDirectoryStride      = 128
	identifierBloomBitsPerDocument = 10
	identifierBloomHashes          = 7
	identifierDirectoryReadBytes   = 1 << 20
)

// identifierDirectory answers "where is this identifier in the segment" mostly
// from memory. A Bloom filter over every identifier hash rejects an absent
// identifier without I/O, and the hash of every 128th entry narrows a present
// one to a single block read. It costs about 1.4 bytes per document, built by
// one sequential read of the identifier index, which also checks its body
// checksum.
type identifierDirectory struct {
	index  *identifierIndex
	bloom  []uint64
	bits   uint64
	sparse [][16]byte
}

func loadIdentifierDirectory(ctx context.Context, index *identifierIndex) (*identifierDirectory, error) {
	bits := uint64(index.documents) * identifierBloomBitsPerDocument
	if bits < 64 {
		bits = 64
	}
	directory := &identifierDirectory{
		index: index, bloom: make([]uint64, (bits+63)/64), bits: bits,
		sparse: make([][16]byte, 0, (index.documents+identifierDirectoryStride-1)/identifierDirectoryStride),
	}
	checksum := crc32.New(crcTable)
	buffer := make([]byte, identifierDirectoryReadBytes-identifierDirectoryReadBytes%identifierIndexEntrySize)
	var previous [16]byte
	for position := uint32(0); position < index.documents; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count := min(uint32(len(buffer)/identifierIndexEntrySize), index.documents-position)
		chunk := buffer[:count*identifierIndexEntrySize]
		offset := uint64(identifierIndexHeaderSize) + uint64(position)*identifierIndexEntrySize
		if err := readAtFull(index.file, chunk, offset); err != nil {
			return nil, err
		}
		_, _ = checksum.Write(chunk)
		for entry := uint32(0); entry < count; entry++ {
			var hash [16]byte
			copy(hash[:], chunk[entry*identifierIndexEntrySize:entry*identifierIndexEntrySize+16])
			if position+entry > 0 && bytes.Compare(previous[:], hash[:]) > 0 {
				return nil, corruptIndexf("identifier index entries are not ordered")
			}
			previous = hash
			if (position+entry)%identifierDirectoryStride == 0 {
				directory.sparse = append(directory.sparse, hash)
			}
			directory.add(hash)
		}
		position += count
	}
	if checksum.Sum32() != index.bodyCRC {
		return nil, corruptIndexf("identifier index body checksum does not match")
	}
	return directory, nil
}

// rebind keeps the in-memory filter and samples and reads blocks through
// another opening of the same sidecar, such as a later container generation.
func (directory *identifierDirectory) rebind(index *identifierIndex) *identifierDirectory {
	return &identifierDirectory{index: index, bloom: directory.bloom, bits: directory.bits, sparse: directory.sparse}
}

func identifierBloomProbes(hash [16]byte) (uint64, uint64) {
	return binary.LittleEndian.Uint64(hash[:8]), binary.LittleEndian.Uint64(hash[8:]) | 1
}

func (directory *identifierDirectory) add(hash [16]byte) {
	first, step := identifierBloomProbes(hash)
	for probe := uint64(0); probe < identifierBloomHashes; probe++ {
		bit := (first + probe*step) % directory.bits
		directory.bloom[bit/64] |= 1 << (bit % 64)
	}
}

func (directory *identifierDirectory) mayContain(hash [16]byte) bool {
	first, step := identifierBloomProbes(hash)
	for probe := uint64(0); probe < identifierBloomHashes; probe++ {
		bit := (first + probe*step) % directory.bits
		if directory.bloom[bit/64]&(1<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

// Find returns the document of identifier in segment, as identifierIndex.Find
// does, reading at most the blocks that can hold its hash.
func (directory *identifierDirectory) Find(ctx context.Context, segment *Segment, identifier string) (uint32, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	target := identifierHash(identifier)
	if directory.index.documents == 0 || !directory.mayContain(target) {
		return 0, false, nil
	}
	// The first entry whose hash may equal target is in the block before the
	// first sampled hash that is not smaller than target.
	block := sort.Search(len(directory.sparse), func(position int) bool {
		return bytes.Compare(directory.sparse[position][:], target[:]) >= 0
	})
	if block > 0 {
		block--
	}
	buffer := make([]byte, identifierDirectoryStride*identifierIndexEntrySize)
	for start := uint32(block) * identifierDirectoryStride; start < directory.index.documents; start += identifierDirectoryStride {
		count := min(uint32(identifierDirectoryStride), directory.index.documents-start)
		chunk := buffer[:count*identifierIndexEntrySize]
		if err := readAtFull(directory.index.file, chunk, uint64(identifierIndexHeaderSize)+uint64(start)*identifierIndexEntrySize); err != nil {
			return 0, false, err
		}
		for entry := uint32(0); entry < count; entry++ {
			encoded := chunk[entry*identifierIndexEntrySize : (entry+1)*identifierIndexEntrySize]
			comparison := bytes.Compare(encoded[:16], target[:])
			if comparison < 0 {
				continue
			}
			if comparison > 0 {
				return 0, false, nil
			}
			document := binary.LittleEndian.Uint32(encoded[16:20])
			if document >= directory.index.documents {
				return 0, false, corruptIndexf("identifier index document %d is out of range", document)
			}
			actual, err := segment.documentIdentifier(document)
			if err != nil {
				return 0, false, err
			}
			if actual == identifier {
				return document, true, nil
			}
		}
	}
	return 0, false, nil
}
