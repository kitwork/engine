package kitdb

import (
	"fmt"
	"reflect"
	"testing"
)

func TestGenerationMergeHeapOrdersBothDirectionsWithoutBoxing(t *testing.T) {
	input := []generationHeapItem{
		{key: []byte("b"), segment: 0},
		{key: []byte("a"), segment: 1},
		{key: []byte("c"), segment: 2},
		{key: []byte("a"), segment: 3},
	}
	for _, test := range []struct {
		name    string
		reverse bool
		want    []string
	}{
		{name: "forward", want: []string{"a/3", "a/1", "b/0", "c/2"}},
		{name: "reverse", reverse: true, want: []string{"c/2", "b/0", "a/3", "a/1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			heap := generationMergeHeap{reverse: test.reverse}
			for _, item := range input {
				heap.push(item)
			}
			var got []string
			for heap.len() != 0 {
				item := heap.pop()
				got = append(got, fmt.Sprintf("%s/%d", item.key, item.segment))
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("heap order = %v, want %v", got, test.want)
			}
		})
	}
}
