package work

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/utilities/safepath"
)

// disk() keeps each value in a file of the app's .database/disk/ (KITWORK_STORAGE.md mục 2, 22/10/2026): the
// disk of this machine, kept across a restart, never served, never deployed. A file is named by the SHA-256
// of the encoded key, under a folder of its first two hex digits, and holds the key itself, so a file of
// another key is never taken for this one. A write goes to a file beside it and is renamed over the old one,
// so a read sees the old value or the new one, never half of one. Full, a write fails; nothing kept is
// pushed out, only what has expired is swept first, and only when something kept can have expired since the
// last sweep. Every path is checked against the app's folder as it is reached, links resolved: a
// .database/disk that leads into another app's folder is refused, not read or written.

// The ceilings of disk() of one app (mục 9; the numbers are open, mục 10.2). Vars so tests can lower them.
var (
	storageDiskKeys  = 100_000
	storageDiskBytes = 1 << 30
)

// diskMagic begins every file of disk(), with the version of its layout.
var diskMagic = []byte("KWS1")

// diskPlace is disk() of one app. Its lock orders reads before writes in this process (a file renamed while
// read is refused on Windows) and keeps the count of keys and bytes the ceilings are measured on, and when
// the soonest value kept expires.
type diskPlace struct {
	dir string
	app *safepath.Boundary // the app's folder: every path of disk() stays inside it

	mu      sync.RWMutex
	counted bool
	keys    int
	bytes   int64
	// expiryKnown is true once a sweep has read every file; nextExpiry is then when the soonest value kept
	// expires, zero when none does. Before it is past, a sweep would find nothing to remove.
	expiryKnown bool
	nextExpiry  time.Time
}

func (p *placeSet) openDisk(root string) (storagePlace, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("%w: app đang dừng", errStorageUnavailable)
	}
	if p.disk == nil {
		app, err := safepath.NewBoundary(root)
		if err != nil {
			return nil, fmt.Errorf("%w: thư mục của app: %v", errStorageUnavailable, err)
		}
		p.disk = &diskPlace{dir: filepath.Join(root, ".database", "disk"), app: app}
	}
	return p.disk, nil
}

// errStorageOutside is a path of disk() that leads outside the app's folder.
var errStorageOutside = errors.New("disk() đi ra ngoài thư mục của app")

// inside refuses path when, links resolved, it leads outside the app's folder.
func (d *diskPlace) inside(path string) error {
	inside, err := d.app.Contains(path)
	if err != nil || !inside {
		return fmt.Errorf("%w: .database/disk/ của app dẫn tới chỗ khác qua một liên kết; không đọc, không ghi ở đó (KITWORK_STORAGE.md mục 9)", errStorageOutside)
	}
	return nil
}

func (d *diskPlace) close() {}

// file is where key is kept.
func (d *diskPlace) file(key string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(d.dir, name[:2], name)
}

// diskRecord is a file of disk(): its key, when it expires, its value.
type diskRecord struct {
	key      string
	expireAt time.Time
	data     []byte
}

func encodeDiskRecord(record diskRecord) []byte {
	out := make([]byte, 0, len(diskMagic)+8+binary.MaxVarintLen64+len(record.key)+len(record.data))
	out = append(out, diskMagic...)
	var expire int64
	if !record.expireAt.IsZero() {
		expire = record.expireAt.UnixNano()
	}
	out = binary.BigEndian.AppendUint64(out, uint64(expire))
	out = binary.AppendUvarint(out, uint64(len(record.key)))
	out = append(out, record.key...)
	return append(out, record.data...)
}

var errDiskRecord = errors.New("file của disk() hỏng")

func decodeDiskRecord(content []byte) (diskRecord, error) {
	if len(content) < len(diskMagic)+8 || !bytes.Equal(content[:len(diskMagic)], diskMagic) {
		return diskRecord{}, errDiskRecord
	}
	rest := content[len(diskMagic):]
	expire := int64(binary.BigEndian.Uint64(rest))
	rest = rest[8:]
	size, read := binary.Uvarint(rest)
	if read <= 0 || size > uint64(len(rest)-read) {
		return diskRecord{}, errDiskRecord
	}
	record := diskRecord{key: string(rest[read : read+int(size)]), data: rest[read+int(size):]}
	if expire != 0 {
		record.expireAt = time.Unix(0, expire)
	}
	return record, nil
}

// read is the live record of key, if there is one; expired is true for one that has expired.
func (d *diskPlace) read(key string) (record diskRecord, found, expired bool, err error) {
	path := d.file(key)
	if err := d.inside(path); err != nil {
		return diskRecord{}, false, false, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return diskRecord{}, false, false, nil
	}
	if err != nil {
		return diskRecord{}, false, false, err
	}
	record, err = decodeDiskRecord(content)
	if err != nil || record.key != key {
		// another key's file under the same name, or damage: not this key's value
		return diskRecord{}, false, false, nil
	}
	if !record.expireAt.IsZero() && !storageClock().Before(record.expireAt) {
		return record, false, true, nil
	}
	return record, true, false, nil
}

func (d *diskPlace) get(key string) ([]byte, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	record, found, _, err := d.read(key)
	return record.data, found, err
}

func (d *diskPlace) expiry(key string) (time.Time, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	record, found, _, err := d.read(key)
	return record.expireAt, found, err
}

func (d *diskPlace) set(key string, data []byte, expireAt time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.count(); err != nil {
		return err
	}
	content := encodeDiskRecord(diskRecord{key: key, expireAt: expireAt, data: data})
	path := d.file(key)
	if err := d.inside(path); err != nil {
		return err
	}
	previous := int64(-1)
	if info, err := os.Stat(path); err == nil {
		previous = info.Size()
	}
	keys, size := d.keys, d.bytes+int64(len(content))
	if previous >= 0 {
		size -= previous
	} else {
		keys++
	}
	if keys > storageDiskKeys || size > int64(storageDiskBytes) {
		// what has expired is gone already: sweep it before refusing, when something can have expired
		if storageSweepDue(d.expiryKnown, d.nextExpiry) {
			if err := d.sweep(); err != nil {
				return err
			}
			keys, size = d.keys, d.bytes+int64(len(content))
			if info, err := os.Stat(path); err == nil {
				size -= info.Size()
			} else {
				keys++
			}
		}
		if keys > storageDiskKeys || size > int64(storageDiskBytes) {
			return fmt.Errorf("%w: disk() của app đã tới trần %d khoá, %d byte; lần ghi hỏng, không gì đã lưu bị xoá (KITWORK_STORAGE.md mục 9)", errStorageFull, storageDiskKeys, storageDiskBytes)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// the folders made just now are checked too: one may have been a link already
	if err := d.inside(path); err != nil {
		return err
	}
	if err := writeFileAtomic(path, content); err != nil {
		return err
	}
	d.keys, d.bytes = keys, size
	d.nextExpiry = storageSooner(d.expiryKnown, d.nextExpiry, expireAt)
	return nil
}

func (d *diskPlace) remove(key string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.count(); err != nil {
		return false, err
	}
	_, found, expired, err := d.read(key)
	if err != nil {
		return false, err
	}
	if !found && !expired {
		return false, nil
	}
	path := d.file(key)
	info, statErr := os.Stat(path)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if statErr == nil {
		d.keys--
		d.bytes -= info.Size()
	}
	return found, nil
}

// count measures what the folder holds, once per process: from then on each write keeps the count.
// Called with mu held for writing.
func (d *diskPlace) count() error {
	if d.counted {
		return nil
	}
	keys, size, err := d.walk(nil)
	if err != nil {
		return err
	}
	d.keys, d.bytes, d.counted = keys, size, true
	return nil
}

// sweep removes the files of values that have expired, counts again, and learns when the soonest value kept
// expires. Called with mu held for writing.
func (d *diskPlace) sweep() error {
	if storageSwept != nil {
		storageSwept("disk")
	}
	now := storageClock()
	var soonest time.Time
	keys, size, err := d.walk(func(path string, content []byte) bool {
		record, err := decodeDiskRecord(content)
		if err != nil || record.expireAt.IsZero() {
			return false
		}
		if now.Before(record.expireAt) {
			soonest = storageSooner(true, soonest, record.expireAt)
			return false
		}
		return os.Remove(path) == nil
	})
	if err != nil {
		return err
	}
	d.keys, d.bytes, d.counted = keys, size, true
	d.expiryKnown, d.nextExpiry = true, soonest
	return nil
}

// walk counts the files of the folder and their bytes; drop, when given, is asked of each file and a file it
// removed is not counted. A folder that does not exist yet holds nothing.
func (d *diskPlace) walk(drop func(path string, content []byte) bool) (int, int64, error) {
	if err := d.inside(d.dir); err != nil {
		return 0, 0, err
	}
	keys, size := 0, int64(0)
	err := filepath.WalkDir(d.dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if drop != nil {
			if content, readErr := os.ReadFile(path); readErr == nil && drop(path, content) {
				return nil
			}
		}
		keys++
		size += info.Size()
		return nil
	})
	return keys, size, err
}

// writeFileAtomic writes content to path through a file beside it renamed over path.
func writeFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	temporary := filepath.Join(dir, ".write-"+hex.EncodeToString(suffix[:]))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
