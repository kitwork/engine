package snapshotfile

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func generationFixture(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.columnar")
	w, err := CreateGeneration(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Add("rows", func(out io.Writer) error { _, err := io.WriteString(out, "abcdef"); return err }); err != nil {
		t.Fatal(err)
	}
	if err := w.Publish(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	return path
}

func generationOpen(t testing.TB, path string) *Reader {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func generationValue(t testing.TB, r *Reader, want string) {
	t.Helper()
	section, err := r.Section("rows")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(section)
	if err != nil || string(data) != want {
		t.Fatalf("section=%q, expected %q: %v", data, want, err)
	}
}

func generationAppend(t testing.TB, path string, base *Reader) *GenerationWriter {
	t.Helper()
	w, err := AppendGeneration(path, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err := w.Add("rows", func(out io.Writer) error {
		if err := Reference(out, base, "rows", 0, 2); err != nil {
			return err
		}
		if _, err := io.WriteString(out, "XY"); err != nil {
			return err
		}
		return Reference(out, base, "rows", 4, 2)
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestGenerationAppendReferencesAndPinnedReader(t *testing.T) {
	path := generationFixture(t)
	base := generationOpen(t, path)
	initial := base.GenerationInfo()
	w := generationAppend(t, path, base)
	if w.BytesWritten() != 2 {
		t.Fatalf("reference wrote old bytes: %d", w.BytesWritten())
	}
	if err := w.Publish(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	current := generationOpen(t, path)
	generationValue(t, current, "abXYef")
	generationValue(t, base, "abcdef")
	if current.GenerationInfo().Number != initial.Number+1 {
		t.Fatal(current.GenerationInfo())
	}
	var oldMeta, newMeta string
	if err := base.Metadata(&oldMeta); err != nil {
		t.Fatal(err)
	}
	if err := current.Metadata(&newMeta); err != nil {
		t.Fatal(err)
	}
	if oldMeta != "first" || newMeta != "second" {
		t.Fatal(oldMeta, newMeta)
	}
	section, err := current.Section("rows")
	if err != nil {
		t.Fatal(err)
	}
	for start := int64(0); start <= 6; start++ {
		for length := 0; length <= 8; length++ {
			buf := make([]byte, length)
			n, err := section.ReadAt(buf, start)
			want := min(length, 6-int(start))
			if n != want || string(buf[:n]) != "abXYef"[start:start+int64(want)] {
				t.Fatalf("read range %d+%d: %q %d %v", start, length, buf, n, err)
			}
			if n < length && !errors.Is(err, io.EOF) {
				t.Fatalf("missing short read EOF: %v", err)
			}
		}
	}
	if stale, err := AppendGeneration(path, base); err == nil {
		stale.Close()
		t.Fatal("accepted stale append base")
	}
}

func TestGenerationAbortedAppendAndOldRootRecovery(t *testing.T) {
	path := generationFixture(t)
	base := generationOpen(t, path)
	w := generationAppend(t, path, base)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Publish(ctx, "never"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	w.Close()
	reopened := generationOpen(t, path)
	generationValue(t, reopened, "abcdef")
	if reopened.GenerationInfo().ObsoleteBytes != 2 {
		t.Fatal(reopened.GenerationInfo())
	}
	w = generationAppend(t, path, reopened)
	if err := w.Publish(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	current := generationOpen(t, path)
	if current.GenerationInfo().Number != 2 {
		t.Fatal(current.GenerationInfo())
	}
	generationValue(t, current, "abXYef")
}

func TestGenerationCorruptNewestRootOrDirectoryFallsBack(t *testing.T) {
	for _, mode := range []string{"root", "directory", "truncated-directory"} {
		t.Run(mode, func(t *testing.T) {
			path := generationFixture(t)
			base := generationOpen(t, path)
			w := generationAppend(t, path, base)
			if err := w.Publish(context.Background(), "second"); err != nil {
				t.Fatal(err)
			}
			current := generationOpen(t, path)
			state := *current.generation
			current.Close()
			base.Close()
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "truncated-directory" {
				err = file.Truncate(state.directoryOffset + state.directoryLength - 1)
			} else {
				offset := int64(state.slot*rootSize + rootSize - 1)
				if mode == "directory" {
					offset = state.directoryOffset
				}
				var b [1]byte
				_, err = file.ReadAt(b[:], offset)
				if err == nil {
					b[0] ^= 1
					_, err = file.WriteAt(b[:], offset)
				}
			}
			if closeErr := file.Close(); err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			recovered := generationOpen(t, path)
			generationValue(t, recovered, "abcdef")
			if recovered.GenerationInfo().Number != 1 {
				t.Fatal(recovered.GenerationInfo())
			}
			w = generationAppend(t, path, recovered)
			if err := w.Publish(context.Background(), "repaired"); err != nil {
				t.Fatal(err)
			}
			generationValue(t, generationOpen(t, path), "abXYef")
		})
	}
}

func TestGenerationInvalidDirectoriesAndReferenceBounds(t *testing.T) {
	valid := directory{Entries: []Entry{{Name: "rows", Length: 6, Extents: []Extent{{generationDataStart, 6}}}}}
	for _, mode := range []string{"overlap", "header", "length", "negative", "duplicate", "future"} {
		t.Run(mode, func(t *testing.T) {
			data, _ := json.Marshal(valid)
			var bad directory
			if err := json.Unmarshal(data, &bad); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "overlap":
				bad.Entries = append(bad.Entries, Entry{Name: "second", Length: 2, Extents: []Extent{{generationDataStart + 1, 2}}})
			case "header":
				bad.Entries[0].Extents[0].Offset = 0
			case "length":
				bad.Entries[0].Length++
			case "negative":
				bad.Entries[0].Extents[0].Length = -1
			case "duplicate":
				bad.Entries = append(bad.Entries, bad.Entries[0])
			case "future":
				bad.Entries[0].Extents[0].Offset = generationDataStart + 100
			}
			if _, err := validateGenerationDirectory(bad, generationDataStart+6); err == nil {
				t.Fatal("accepted invalid directory")
			}
		})
	}
	path := generationFixture(t)
	base := generationOpen(t, path)
	w, err := AppendGeneration(path, base)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Add("bad", func(out io.Writer) error { return Reference(out, base, "rows", 5, 2) }); err == nil {
		t.Fatal("accepted out of bounds reference")
	}
	if err := w.Publish(context.Background(), "invalid"); err == nil {
		t.Fatal("published failed reference")
	}
	generationValue(t, generationOpen(t, path), "abcdef")
}

func TestGenerationCompactionPolicyAndReplacement(t *testing.T) {
	path := generationFixture(t)
	base := generationOpen(t, path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, (1<<20)+1)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	current := generationOpen(t, path)
	if !current.NeedsCompaction() {
		t.Fatal(current.GenerationInfo())
	}
	w, err := CreateGeneration(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	section, err := current.Section("rows")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Add("rows", func(out io.Writer) error { return Copy(context.Background(), out, section) }); err != nil {
		t.Fatal(err)
	}
	current.Close()
	base.Close()
	if err := w.Publish(context.Background(), "compact"); err != nil {
		t.Fatal(err)
	}
	compacted := generationOpen(t, path)
	generationValue(t, compacted, "abcdef")
	if compacted.NeedsCompaction() || compacted.GenerationInfo().ObsoleteBytes != 0 {
		t.Fatal(compacted.GenerationInfo())
	}
}

func TestGenerationProcessExitPublicationMatrix(t *testing.T) {
	const childPath = "KITDB_GENERATION_CRASH_PATH"
	if path := os.Getenv(childPath); path != "" {
		base := generationOpen(t, path)
		rewrite := os.Getenv("KITDB_GENERATION_CRASH_MODE") == "rewrite"
		var w *GenerationWriter
		var err error
		if rewrite {
			w, err = CreateGeneration(path)
		} else {
			w, err = AppendGeneration(path, base)
		}
		if err != nil {
			t.Fatal(err)
		}
		w.fault = func(stage string) error {
			if stage == os.Getenv("KITDB_GENERATION_CRASH_STAGE") {
				os.Exit(73)
			}
			return nil
		}
		if err := w.Add("rows", func(out io.Writer) error {
			if rewrite {
				_, err := io.WriteString(out, "abNEW")
				return err
			}
			if err := Reference(out, base, "rows", 0, 2); err != nil {
				return err
			}
			_, err := io.WriteString(out, "NEW")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		base.Close()
		if err := w.Publish(context.Background(), "second"); err != nil {
			t.Fatal(err)
		}
		t.Fatal("did not reach crash boundary")
	}
	for _, mode := range []string{"append", "rewrite"} {
		stages := []string{"after-entry", "after-directory-sync", "after-root-write", "after-root-sync"}
		if mode == "rewrite" {
			stages = append(stages, "before-rename", "after-rename")
		}
		for _, stage := range stages {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				path := generationFixture(t)
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				child := exec.Command(executable, "-test.run=^TestGenerationProcessExitPublicationMatrix$", "-test.timeout=20s")
				child.Env = append(os.Environ(), childPath+"="+path, "KITDB_GENERATION_CRASH_STAGE="+stage, "KITDB_GENERATION_CRASH_MODE="+mode)
				output, err := child.CombinedOutput()
				if child.ProcessState == nil || child.ProcessState.ExitCode() != 73 {
					t.Fatalf("child: %v %s", err, output)
				}
				r := generationOpen(t, path)
				var meta string
				if err := r.Metadata(&meta); err != nil {
					t.Fatal(err)
				}
				if (mode == "rewrite" && stage != "after-rename") || stage == "after-entry" || stage == "after-directory-sync" {
					if meta != "first" {
						t.Fatal(meta)
					}
					generationValue(t, r, "abcdef")
				} else {
					if meta != "second" {
						t.Fatal(meta)
					}
					generationValue(t, r, "abNEW")
				}
			})
		}
	}
}

func TestGenerationWriteAndPublicationFailures(t *testing.T) {
	for _, stage := range []string{"write", "after-directory-sync", "after-root-write", "after-root-sync"} {
		t.Run(stage, func(t *testing.T) {
			path := generationFixture(t)
			base := generationOpen(t, path)
			w, err := AppendGeneration(path, base)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			if stage == "write" {
				if err := w.file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = w.Add("rows", func(out io.Writer) error { _, err := io.WriteString(out, "new"); return err })
			if stage == "write" {
				if err == nil {
					t.Fatal("accepted failed file write")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				w.fault = func(point string) error {
					if point == stage {
						return io.ErrClosedPipe
					}
					return nil
				}
				if err := w.Publish(context.Background(), "new"); !errors.Is(err, io.ErrClosedPipe) {
					t.Fatal(err)
				}
			}
			if err := w.Publish(context.Background(), "retry"); err == nil {
				t.Fatal("retried uncertain/failed publication")
			}
			w.Close()
			r := generationOpen(t, path)
			if stage == "write" || stage == "after-directory-sync" {
				generationValue(t, r, "abcdef")
			} else {
				generationValue(t, r, "new")
			}
		})
	}
}

func TestGenerationCancellationAfterRootWriteDoesNotPretendRollback(t *testing.T) {
	path := generationFixture(t)
	base := generationOpen(t, path)
	w := generationAppend(t, path, base)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.fault = func(stage string) error {
		if stage == "after-root-write" {
			cancel()
		}
		return nil
	}
	if err := w.Publish(ctx, "committed"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("did not exercise cancellation")
	}
	generationValue(t, generationOpen(t, path), "abXYef")
}

func TestGenerationTornRootNeverPublishesMixedDirectory(t *testing.T) {
	path := generationFixture(t)
	base := generationOpen(t, path)
	w := generationAppend(t, path, base)
	if err := w.Publish(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	current := generationOpen(t, path)
	slot := current.generation.slot
	current.Close()
	base.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int{1, 8, 16, 32, 48, 512, 2048, rootSize - 1} {
		bad := bytes.Clone(data)
		clear(bad[slot*rootSize+cut : (slot+1)*rootSize])
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		r, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		generationValue(t, r, "abcdef")
		r.Close()
	}
}

func FuzzGenerationDirectory(f *testing.F) {
	f.Add([]byte(`{"Entries":[{"Name":"rows","Length":6,"Extents":[{"Offset":8192,"Length":6}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		var d directory
		if json.Unmarshal(data, &d) != nil {
			return
		}
		live, err := validateGenerationDirectory(d, 1<<20)
		if err == nil && (live < 0 || live > (1<<20)-generationDataStart) {
			t.Fatal(live)
		}
	})
}

func TestGenerationRejectsDifferentRootIdentity(t *testing.T) {
	path := generationFixture(t)
	base := generationOpen(t, path)
	w := generationAppend(t, path, base)
	if err := w.Publish(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	base.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[48] ^= 1
	binary.LittleEndian.PutUint32(data[rootSize-4:], crc32.Checksum(data[:rootSize-4], checksum))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(path); err == nil {
		r.Close()
		t.Fatal("accepted mismatched root identities")
	} else if !strings.Contains(err.Error(), "identities") {
		t.Fatal(err)
	}
}
