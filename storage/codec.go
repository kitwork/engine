package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/kitwork/engine/value"
)

// A value is kept as bytes in all three places, written once by Encode and read back by Decode: what
// memory() gives back is what disk() and datastore() give back for the same write (mục 5), and every read
// is a value of its own, so two requests never hold one object (a Go map shared by two VMs is a data
// race). What can be kept is what can be written as JSON, and bytes (mục 5): null, a boolean, a number, a
// string, bytes, an array, a plain object; a date and a duration keep their kind. A function, a handle of
// the engine (a database, a job, a routine, a stream), the context, are refused by name, never dropped.

// codecVersion is the first byte of every encoded value, so a format of later can be told apart.
const codecVersion byte = 1

// MaxDepth is how deep an array or an object may nest; past it the value holds itself or is too deep.
const MaxDepth = 64

const (
	tagNil      byte = 'n'
	tagFalse    byte = 'f'
	tagTrue     byte = 't'
	tagNumber   byte = 'd'
	tagTime     byte = 'T'
	tagDuration byte = 'D'
	tagString   byte = 's'
	tagBytes    byte = 'b'
	tagArray    byte = 'a'
	tagObject   byte = 'o'
)

// ErrTooLarge is a value whose encoding is past the limit Encode was given.
var ErrTooLarge = errors.New("storage: value past its size limit")

// ValueError is a value that cannot be kept: Path is where in it ("" for the value itself, ".items[2]"),
// Kind what is there in the words of the language.
type ValueError struct {
	Path string
	Kind string
}

func (e *ValueError) Error() string {
	if e.Path == "" {
		return "không lưu được " + e.Kind
	}
	return "không lưu được " + e.Kind + " ở " + e.Path
}

// SizeError is a value past the limit: Size is how far the encoding had come when it crossed.
type SizeError struct {
	Size  int
	Limit int
}

func (e *SizeError) Error() string {
	return fmt.Sprintf("giá trị dài hơn %d byte, quá trần %d byte của một giá trị", e.Size, e.Limit)
}

func (e *SizeError) Is(target error) bool { return target == ErrTooLarge }

// Encode writes v as one kept value, refusing it past limit bytes (limit <= 0 is no limit).
func Encode(v value.Value, limit int) ([]byte, error) {
	encoder := encoder{limit: limit, data: []byte{codecVersion}}
	if err := encoder.write(v, "", 0); err != nil {
		return nil, err
	}
	return encoder.data, nil
}

type encoder struct {
	data  []byte
	limit int
}

// check refuses the value once what is written is past the limit. A string is in memory already, so
// writing it before the check holds no more than the value itself.
func (e *encoder) check() error {
	if e.limit > 0 && len(e.data) > e.limit {
		return &SizeError{Size: len(e.data), Limit: e.limit}
	}
	return nil
}

func (e *encoder) byte(tag byte) error {
	e.data = append(e.data, tag)
	return e.check()
}

func (e *encoder) number(tag byte, n float64) error {
	e.data = append(e.data, tag)
	e.data = binary.BigEndian.AppendUint64(e.data, math.Float64bits(n))
	return e.check()
}

func (e *encoder) text(tag byte, text []byte) error {
	if e.limit > 0 && len(e.data)+len(text) > e.limit {
		return &SizeError{Size: len(e.data) + len(text), Limit: e.limit}
	}
	e.data = append(e.data, tag)
	e.data = binary.AppendUvarint(e.data, uint64(len(text)))
	e.data = append(e.data, text...)
	return e.check()
}

func (e *encoder) count(tag byte, n int) error {
	e.data = append(e.data, tag)
	e.data = binary.AppendUvarint(e.data, uint64(n))
	return e.check()
}

func (e *encoder) write(v value.Value, path string, depth int) error {
	switch v.K {
	case value.Nil:
		return e.byte(tagNil)
	case value.Bool:
		if v.Truthy() {
			return e.byte(tagTrue)
		}
		return e.byte(tagFalse)
	case value.Number:
		return e.number(tagNumber, v.N)
	case value.Time:
		return e.number(tagTime, v.N)
	case value.Duration:
		return e.number(tagDuration, v.N)
	case value.String:
		text, _ := v.AsString()
		return e.text(tagString, []byte(text))
	case value.Bytes:
		return e.text(tagBytes, v.Bytes())
	case value.Array:
		if depth >= MaxDepth {
			return &ValueError{Path: path, Kind: "mảng tự chứa mình hay lồng quá " + strconv.Itoa(MaxDepth) + " tầng"}
		}
		items := v.Array()
		if err := e.count(tagArray, len(items)); err != nil {
			return err
		}
		for index, item := range items {
			if err := e.write(item, path+"["+strconv.Itoa(index)+"]", depth+1); err != nil {
				return err
			}
		}
		return nil
	case value.Map:
		if depth >= MaxDepth {
			return &ValueError{Path: path, Kind: "object tự chứa mình hay lồng quá " + strconv.Itoa(MaxDepth) + " tầng"}
		}
		entries := mapEntries(v)
		if entries == nil {
			return &ValueError{Path: path, Kind: "object của engine"}
		}
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if err := e.count(tagObject, len(keys)); err != nil {
			return err
		}
		for _, key := range keys {
			if err := e.text(tagString, []byte(key)); err != nil {
				return err
			}
			if err := e.write(entries[key], path+"."+key, depth+1); err != nil {
				return err
			}
		}
		return nil
	case value.Func:
		return &ValueError{Path: path, Kind: "hàm"}
	case value.Proxy, value.Struct, value.Any:
		return &ValueError{Path: path, Kind: "handle hay object của engine (database, job, routine, luồng, context)"}
	}
	return &ValueError{Path: path, Kind: "giá trị kiểu " + v.K.String()}
}

// mapEntries are the entries of an object, nil when its payload is not one a program writes.
func mapEntries(v value.Value) map[string]value.Value {
	switch entries := v.V.(type) {
	case map[string]value.Value:
		if entries == nil {
			return map[string]value.Value{}
		}
		return entries
	case map[string]any:
		converted := make(map[string]value.Value, len(entries))
		for key, item := range entries {
			converted[key] = value.New(item)
		}
		return converted
	}
	return nil
}

// ErrCorrupt is bytes Decode cannot read: written by another format, or damaged.
var ErrCorrupt = errors.New("storage: kept value cannot be read")

// Decode reads a value Encode wrote, as a value of its own.
func Decode(data []byte) (value.Value, error) {
	if len(data) == 0 || data[0] != codecVersion {
		return value.Value{}, ErrCorrupt
	}
	decoder := decoder{data: data[1:]}
	result, err := decoder.read(0)
	if err != nil {
		return value.Value{}, err
	}
	if len(decoder.data) != 0 {
		return value.Value{}, ErrCorrupt
	}
	return result, nil
}

type decoder struct {
	data []byte
}

func (d *decoder) read(depth int) (value.Value, error) {
	if len(d.data) == 0 || depth > MaxDepth {
		return value.Value{}, ErrCorrupt
	}
	tag := d.data[0]
	d.data = d.data[1:]
	switch tag {
	case tagNil:
		return value.Value{K: value.Nil}, nil
	case tagFalse:
		return value.Value{K: value.Bool}, nil
	case tagTrue:
		return value.Value{K: value.Bool, N: 1}, nil
	case tagNumber, tagTime, tagDuration:
		if len(d.data) < 8 {
			return value.Value{}, ErrCorrupt
		}
		n := math.Float64frombits(binary.BigEndian.Uint64(d.data))
		d.data = d.data[8:]
		kind := value.Number
		if tag == tagTime {
			kind = value.Time
		} else if tag == tagDuration {
			kind = value.Duration
		}
		return value.Value{K: kind, N: n}, nil
	case tagString, tagBytes:
		text, err := d.text()
		if err != nil {
			return value.Value{}, err
		}
		if tag == tagString {
			return value.Value{K: value.String, V: string(text)}, nil
		}
		return value.Value{K: value.Bytes, V: append([]byte(nil), text...)}, nil
	case tagArray:
		count, err := d.count()
		if err != nil {
			return value.Value{}, err
		}
		items := make([]value.Value, count)
		for index := range items {
			if items[index], err = d.read(depth + 1); err != nil {
				return value.Value{}, err
			}
		}
		return value.New(items), nil
	case tagObject:
		count, err := d.count()
		if err != nil {
			return value.Value{}, err
		}
		entries := make(map[string]value.Value, count)
		for range count {
			if len(d.data) == 0 || d.data[0] != tagString {
				return value.Value{}, ErrCorrupt
			}
			d.data = d.data[1:]
			key, err := d.text()
			if err != nil {
				return value.Value{}, err
			}
			if entries[string(key)], err = d.read(depth + 1); err != nil {
				return value.Value{}, err
			}
		}
		return value.Value{K: value.Map, V: entries}, nil
	}
	return value.Value{}, ErrCorrupt
}

func (d *decoder) count() (int, error) {
	n, size := binary.Uvarint(d.data)
	// each item takes a byte at least: a count past what is left is damage, not a value
	if size <= 0 || n > uint64(len(d.data)) {
		return 0, ErrCorrupt
	}
	d.data = d.data[size:]
	return int(n), nil
}

func (d *decoder) text() ([]byte, error) {
	n, size := binary.Uvarint(d.data)
	if size <= 0 || n > uint64(len(d.data)-size) {
		return nil, ErrCorrupt
	}
	text := d.data[size : size+int(n)]
	d.data = d.data[size+int(n):]
	return text, nil
}
