package search

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkSegmentSearch(b *testing.B) {
	const documentCount = 100_000
	buildStarted := time.Now()
	schema := productSchema(b)
	builder, err := NewBuilder(schema, BuildOptions{FlushThresholdBytes: 256 << 20})
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < documentCount; index++ {
		document := Document{
			ID: fmt.Sprintf("sku-%08d", index),
			Fields: map[string]string{
				"title": "Áo thun cotton nam thể thao",
				"body":  "Sản phẩm cotton thoáng mát phù hợp sử dụng hàng ngày",
			},
		}
		if err := builder.Add(document); err != nil {
			b.Fatal(err)
		}
	}
	path := filepath.Join(b.TempDir(), "benchmark.ks")
	info, err := builder.Write(context.Background(), path)
	if err != nil {
		b.Fatal(err)
	}
	buildDuration := time.Since(buildStarted)
	segment, err := OpenSegment(path, schema)
	if err != nil {
		b.Fatal(err)
	}
	defer segment.Close()
	query := MatchQuery{Field: "title", Text: "ao thun cotton nam"}
	if _, err := segment.Search(context.Background(), query, SearchOptions{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := segment.Search(context.Background(), query, SearchOptions{}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(documentCount)/buildDuration.Seconds(), "build-docs/s")
	b.ReportMetric(float64(info.Bytes)/(1<<20), "index-MiB")
}

func BenchmarkIndexSearch(b *testing.B) {
	const documentCount = 100_000
	buildStarted := time.Now()
	schema := productSchema(b)
	directory := b.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 10_000, FlushThresholdBytes: 256 << 20},
	})
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < documentCount; index++ {
		document := Document{
			ID: fmt.Sprintf("sku-%08d", index),
			Fields: map[string]string{
				"title": "Áo thun cotton nam thể thao",
				"body":  "Sản phẩm cotton thoáng mát phù hợp sử dụng hàng ngày",
			},
		}
		if err := writer.Add(context.Background(), document); err != nil {
			b.Fatal(err)
		}
	}
	info, err := writer.Commit(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
	buildDuration := time.Since(buildStarted)
	index, err := OpenIndex(directory, schema)
	if err != nil {
		b.Fatal(err)
	}
	defer index.Close()
	query := MatchQuery{Field: "title", Text: "ao thun cotton nam"}
	if _, err := index.Search(context.Background(), query, SearchOptions{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := index.Search(context.Background(), query, SearchOptions{}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(documentCount)/buildDuration.Seconds(), "build-docs/s")
	b.ReportMetric(float64(info.Bytes)/(1<<20), "index-MiB")
	b.ReportMetric(float64(info.Segments), "segments")
}

func BenchmarkIndexMultiFieldSearch(b *testing.B) {
	const documentCount = 100_000
	buildStarted := time.Now()
	schema := productSchema(b)
	directory := b.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 10_000, FlushThresholdBytes: 256 << 20},
	})
	if err != nil {
		b.Fatal(err)
	}
	for document := 0; document < documentCount; document++ {
		body := "San pham cotton thoang mat phu hop su dung hang ngay"
		if document%100 == 0 {
			body += " limited"
		}
		if err := writer.Add(context.Background(), Document{
			ID: fmt.Sprintf("sku-%08d", document),
			Fields: map[string]string{
				"title": "Ao thun nam the thao",
				"body":  body,
			},
		}); err != nil {
			b.Fatal(err)
		}
	}
	info, err := writer.Commit(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
	buildDuration := time.Since(buildStarted)
	index, err := OpenIndex(directory, schema)
	if err != nil {
		b.Fatal(err)
	}
	defer index.Close()

	for _, benchmark := range []struct {
		name          string
		query         string
		coldFrequency bool
	}{
		{name: "warm-common-cross-field", query: "ao cotton"},
		{name: "cold-common-cross-field", query: "ao cotton", coldFrequency: true},
		{name: "warm-selective-cross-field", query: "ao limited"},
		{name: "cold-selective-cross-field", query: "ao limited", coldFrequency: true},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			query := MatchQuery{Fields: []string{"title", "body"}, Text: benchmark.query}
			if !benchmark.coldFrequency {
				if _, err := index.Search(context.Background(), query, SearchOptions{}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if benchmark.coldFrequency {
					clearMultiFrequencyCache(index)
				}
				if _, err := index.Search(context.Background(), query, SearchOptions{}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if benchmark.coldFrequency {
				clearMultiFrequencyCache(index)
			}
			if _, err := index.Search(context.Background(), query, SearchOptions{}); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(documentCount)/buildDuration.Seconds(), "build-docs/s")
			b.ReportMetric(float64(info.Bytes)/(1<<20), "index-MiB")
			b.ReportMetric(float64(info.Segments), "segments")
		})
	}
}

func clearMultiFrequencyCache(index *Index) {
	index.multiFreq.mu.Lock()
	index.multiFreq.entries = nil
	index.multiFreq.order = nil
	index.multiFreq.next = 0
	index.multiFreq.mu.Unlock()
}

func BenchmarkBlockMaxConjunctive(b *testing.B) {
	const documentCount = 100_000
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		b.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{FlushThresholdBytes: 256 << 20})
	if err != nil {
		b.Fatal(err)
	}
	highScore := strings.TrimSpace(strings.Repeat("alpha beta ", 8))
	lowScore := "alpha beta " + strings.Repeat("padding ", 126)
	for document := 0; document < documentCount; document++ {
		text := lowScore
		if document < postingBlockDocuments {
			text = highScore
		}
		if err := builder.Add(Document{
			ID: fmt.Sprintf("doc-%08d", document), Fields: map[string]string{"text": text},
		}); err != nil {
			b.Fatal(err)
		}
	}
	path := filepath.Join(b.TempDir(), "block-max.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		b.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		b.Fatal(err)
	}
	defer segment.Close()
	prepared, err := prepareMatchQuery(
		context.Background(), schema, MatchQuery{Field: "text", Text: "alpha beta"}, SearchOptions{},
	)
	if err != nil {
		b.Fatal(err)
	}

	for _, benchmark := range []struct {
		name     string
		blockMax bool
	}{
		{name: "block-max", blockMax: true},
		{name: "exhaustive", blockMax: false},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			query := prepared
			query.blockMax = benchmark.blockMax
			statistics := blockMaxStatistics{}
			query.statistics = &statistics
			if _, err := segment.searchMatch(context.Background(), query); err != nil {
				b.Fatal(err)
			}
			query.statistics = nil
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := segment.searchMatch(context.Background(), query); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(statistics.leadBlocksDecoded), "decoded-blocks/op")
			b.ReportMetric(float64(statistics.leadBlocksPruned), "pruned-blocks/op")
		})
	}
}
