package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	writerLockHelperEnvironment = "KITWORK_SEARCH_WRITER_LOCK_HELPER"
	writerLockHelperDirectory   = "KITWORK_SEARCH_WRITER_LOCK_DIRECTORY"
	writerLockHelperReady       = "KITWORK_SEARCH_WRITER_LOCK_READY"
)

func TestIndexWriterExcludesSecondWriterButAllowsReaders(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	first, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Add(context.Background(), Document{
		ID: "doc", Fields: map[string]string{"text": "visible while locked"},
	}); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if _, err := first.Commit(context.Background()); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}

	if second, err := NewIndexWriter(directory, schema, WriterOptions{}); !errors.Is(err, ErrWriterLocked) {
		if second != nil {
			_ = second.Close()
		}
		_ = first.Close()
		t.Fatalf("second writer error = %v", err)
	}
	reader, err := OpenIndex(directory, schema)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	hits, err := reader.Search(context.Background(), MatchQuery{Field: "text", Text: "visible"}, SearchOptions{})
	_ = reader.Close()
	if err != nil || len(hits) != 1 || hits[0].ID != "doc" {
		_ = first.Close()
		t.Fatalf("reader while writer locked = %#v, %v", hits, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatalf("writer lock was not released by Close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestIndexWriterConstructorFailureReleasesLock(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Add(context.Background(), Document{ID: "doc"}); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	otherSchema, err := NewSchema(Text("other", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	if failed, err := NewIndexWriter(directory, otherSchema, WriterOptions{}); !errors.Is(err, ErrSchemaMismatch) {
		if failed != nil {
			_ = failed.Close()
		}
		t.Fatalf("schema mismatch constructor error = %v", err)
	}
	reopened, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatalf("failed constructor leaked writer lock: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestIndexWriterLockReleasedAfterProcessCrash(t *testing.T) {
	directory := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestIndexWriterLockProcessHelper$")
	command.Env = append(os.Environ(),
		writerLockHelperEnvironment+"=1",
		writerLockHelperDirectory+"="+directory,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	running := true
	defer func() {
		if running {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == writerLockHelperReady {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			_ = command.Wait()
			running = false
			t.Fatalf("writer lock helper exited before ready: %s", stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		running = false
		t.Fatalf("writer lock helper did not become ready: %s", stderr.String())
	}

	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	if writer, err := NewIndexWriter(directory, schema, WriterOptions{}); !errors.Is(err, ErrWriterLocked) {
		if writer != nil {
			_ = writer.Close()
		}
		t.Fatalf("parent opened writer held by child process: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	running = false

	deadline := time.Now().Add(3 * time.Second)
	for {
		writer, err := NewIndexWriter(directory, schema, WriterOptions{})
		if err == nil {
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(err, ErrWriterLocked) || time.Now().After(deadline) {
			t.Fatalf("OS did not release writer lock after process crash: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIndexWriterLockProcessHelper(t *testing.T) {
	if os.Getenv(writerLockHelperEnvironment) != "1" {
		return
	}
	directory := os.Getenv(writerLockHelperDirectory)
	if directory == "" {
		t.Fatal("writer lock helper directory is empty")
	}
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := fmt.Fprintln(os.Stdout, writerLockHelperReady); err != nil {
		t.Fatal(err)
	}
	select {}
}
