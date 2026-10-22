package relational

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

type lifecycleFileDigest struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Fail closed on links, nested directories, and unbounded evidence. This only
// copies a quiescent mixed-workload fixture; it is not a live backup API.
func lifecycleDigests(root string) ([]lifecycleFileDigest, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("fixture root is not a plain directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || len(entries) > 256 {
		return nil, fmt.Errorf("fixture file count outside 1..256")
	}
	var result []lifecycleFileDigest
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("non-regular fixture: %s", entry.Name())
		}
		total += info.Size()
		if info.Size() > 256<<20 || total > 1<<30 {
			return nil, fmt.Errorf("fixture exceeds copy budget")
		}
		file, err := os.Open(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(file, info.Size()+1))
		if err := errors.Join(readErr, file.Close()); err != nil {
			return nil, err
		}
		if n != info.Size() {
			return nil, fmt.Errorf("fixture changed while hashing")
		}
		result = append(result, lifecycleFileDigest{entry.Name(), n, hex.EncodeToString(hash.Sum(nil))})
	}
	return result, nil
}

func lifecycleCopyFixture(source, target string) ([]lifecycleFileDigest, error) {
	before, err := lifecycleDigests(source)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return nil, err
	}
	for _, entry := range before {
		in, err := os.Open(filepath.Join(source, entry.Name))
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(filepath.Join(target, entry.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, errors.Join(err, in.Close())
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, entry.Bytes+1))
		if err := errors.Join(copyErr, in.Close(), out.Sync(), out.Close()); err != nil {
			return nil, err
		}
		if n != entry.Bytes {
			return nil, fmt.Errorf("fixture changed during copy")
		}
	}
	after, err := lifecycleDigests(source)
	if err != nil {
		return nil, err
	}
	copied, err := lifecycleDigests(target)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(before, copied) {
		return nil, fmt.Errorf("forensic copy digest mismatch")
	}
	return before, nil
}

// Reconstruct from ACK counts, not from the database being verified. The writer
// updates one disjoint group per commit and cycles over all 1024 rows.
func lifecycleACKModel(ack uint64, batch int) ([]int64, error) {
	if (batch != 1 && batch != 32) || ack > 1_000_000_000 {
		return nil, fmt.Errorf("invalid ACK model bounds")
	}
	groups := uint64(mixedRows / batch)
	model := make([]int64, mixedRows)
	for row := range model {
		model[row] = int64(row % 64)
		group := uint64(row / batch)
		if ack > group {
			model[row] = int64(ack-1-(ack-1-group)%groups) + 65
		}
	}
	return model, nil
}

func lifecycleVerifyCopy(root string, window mixedWindow) (verified int, resultErr error) {
	if window.Databases < 1 || window.Databases > 16 || len(window.Tenants) != window.Databases {
		return 0, fmt.Errorf("incomplete per-tenant ACK evidence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for id, report := range window.Tenants {
		batch := 1
		role := "normal"
		if window.Noisy && id == 0 {
			batch, role = 32, "noisy"
		}
		if report.ID != id || report.Role != role || report.ChangedRows != report.Acknowledged*uint64(batch) || report.CommitSyncs != report.Acknowledged {
			return verified, fmt.Errorf("database %d incomplete ACK accounting", id)
		}
		model, err := lifecycleACKModel(report.Acknowledged, batch)
		if err != nil {
			return verified, err
		}
		tenant := &mixedTenant{path: filepath.Join(root, fmt.Sprintf("tenant_%02d.kitdb", id)), expected: model,
			payload: fmt.Sprintf("tenant-%02d:", id) + strings.Repeat("x", 502)}
		if err := verifyMixedReopen(ctx, tenant, kitdbengine.OpenOptions{}); err != nil {
			return verified, fmt.Errorf("database %d copy reopen: %w", id, err)
		}
		verified++
	}
	return verified, nil
}

func TestLifecycleACKModel(t *testing.T) {
	for _, batch := range []int{1, 32} {
		naive, _ := lifecycleACKModel(0, batch)
		for ack := uint64(0); ack < 2200; ack++ {
			model, err := lifecycleACKModel(ack, batch)
			if err != nil || !reflect.DeepEqual(naive, model) {
				t.Fatalf("batch=%d ack=%d: %v", batch, ack, err)
			}
			start := int(ack*uint64(batch)) % mixedRows
			for row := start; row < start+batch; row++ {
				naive[row] = int64(ack) + 65
			}
		}
	}
	if _, err := lifecycleACKModel(1, 2); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if _, err := lifecycleACKModel(1_000_000_001, 1); err == nil {
		t.Fatal("unbounded ACK accepted")
	}
}

func TestLifecycleForensicCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "original")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	window, err := runMixedWindow(source, mixedConfig{Name: "copy_smoke", Databases: 2, Noisy: true, Duration: 350 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "copy")
	before, err := lifecycleCopyFixture(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycleCopyFixture(source, target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite accepted: %v", err)
	}
	if count, err := lifecycleVerifyCopy(target, window); err != nil || count != 2 {
		t.Fatalf("verified=%d: %v", count, err)
	}
	after, err := lifecycleDigests(source)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("verification mutated originals", err)
	}
	window.Tenants[0].Acknowledged++
	window.Tenants[0].CommitSyncs++
	window.Tenants[0].ChangedRows += 32
	if _, err := lifecycleVerifyCopy(target, window); err == nil {
		t.Fatal("incorrect ACK model accepted")
	}
	if err := os.Mkdir(filepath.Join(source, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycleCopyFixture(source, filepath.Join(root, "bad")); err == nil {
		t.Fatal("nested fixture accepted")
	}
}
