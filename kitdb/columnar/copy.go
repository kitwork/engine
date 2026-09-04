package columnar

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

// DataOffset is the first group offset relative to the columnar section.
func (r *Reader) DataOffset() int64 { return r.start }

// CopyGroups validates and streams complete groups without materializing rows.
// The destination must remain unpublished on error; it may contain partial data.
// Buffers are bounded by one column block, independently of the copied range.
func (r *Reader) CopyGroups(ctx context.Context, dst io.Writer, offset, length int64) (uint64, error) {
	if ctx == nil || dst == nil || r == nil || r.input == nil {
		return 0, fmt.Errorf("columnar: nil group copy argument")
	}
	if offset < r.start || offset > r.input.Size() || length < 0 || length > r.input.Size()-offset {
		return 0, fmt.Errorf("columnar: invalid group copy bounds")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	header := make([]byte, groupHeaderSize(r.version, len(r.Fields)))
	var buffer []byte
	end := offset + length
	var total uint64
	for offset < end {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if int64(len(header)) > end-offset {
			return 0, fmt.Errorf("columnar: truncated copied group header")
		}
		if _, err := r.input.ReadAt(header, offset); err != nil {
			return 0, err
		}
		rows, err := r.validateGroupHeader(header)
		if err != nil {
			return 0, err
		}
		lengths, groupLength, err := r.groupPayloadLengths(header, rows)
		if err != nil {
			return 0, err
		}
		offset += int64(len(header))
		if groupLength > end-offset {
			return 0, fmt.Errorf("columnar: truncated copied group")
		}
		if err := writeGroupBytes(dst, header); err != nil {
			return 0, err
		}
		for i, field := range r.Fields {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			blockLength := lengths[i]
			if cap(buffer) < int(blockLength) {
				buffer = make([]byte, int(blockLength))
			} else {
				buffer = buffer[:int(blockLength)]
			}
			data := buffer
			if _, err := r.input.ReadAt(data, offset); err != nil {
				return 0, err
			}
			if crc32.Checksum(data, crcTable) != binary.LittleEndian.Uint32(header[8+i*4:]) {
				return 0, fmt.Errorf("columnar: copied block checksum mismatch")
			}
			actual, err := inspectVector(data, field, rows, nil)
			if err != nil {
				return 0, err
			}
			if r.version >= formatVersion2 {
				expected, err := r.columnStatistics(header, i, field, rows)
				if err != nil {
					return 0, err
				}
				if !sameColumnStatistics(actual, expected) {
					return 0, fmt.Errorf("columnar: copied block statistics mismatch")
				}
			}
			if err := writeGroupBytes(dst, data); err != nil {
				return 0, err
			}
			offset += blockLength
		}
		total += uint64(rows)
	}
	return total, nil
}

func writeGroupBytes(dst io.Writer, data []byte) error {
	n, err := dst.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
