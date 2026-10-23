// Package storage is the machinery behind kitwork/storage (docs/KITWORK_STORAGE.md): the identity of a key,
// the encoding a value is kept in, the RAM store of memory(), the single flight of get(() => …), and the
// hub that carries send() to observe(). It knows nothing of the VM beyond value.Value; the module the
// program sees, and where each piece lives (one per app), is the work package's.
package storage

import (
	"strconv"
	"strings"
)

// Place is where a value is kept: memory(), disk() or datastore(). One name in two places is two keys.
type Place string

const (
	Memory    Place = "memory"
	Disk      Place = "disk"
	Datastore Place = "datastore"
)

// Key is a value's identity inside its app (mục 3): the place, the root name, the segments.
type Key struct {
	Place    Place
	Name     string
	Segments []string
}

// With is the key with segments added after its own; the key itself never changes.
func (k Key) With(segments ...string) Key {
	joined := make([]string, 0, len(k.Segments)+len(segments))
	joined = append(joined, k.Segments...)
	joined = append(joined, segments...)
	return Key{Place: k.Place, Name: k.Name, Segments: joined}
}

// Encoded is the key as the stores and the hub hold it: the place, the name and each segment written
// with its length first, so no two keys ever meet (segment("a:b") is not segment("a", "b"), mục 4).
func (k Key) Encoded() string {
	var builder strings.Builder
	write := func(part string) {
		builder.WriteString(strconv.Itoa(len(part)))
		builder.WriteByte(':')
		builder.WriteString(part)
	}
	write(string(k.Place))
	write(k.Name)
	for _, segment := range k.Segments {
		write(segment)
	}
	return builder.String()
}

// Human is the key a person reads, key() of the handle: the name and the segments joined by ":". It is
// not the key a store holds: "a:b" reads the same as "a", "b".
func (k Key) Human() string {
	if len(k.Segments) == 0 {
		return k.Name
	}
	return k.Name + ":" + strings.Join(k.Segments, ":")
}
