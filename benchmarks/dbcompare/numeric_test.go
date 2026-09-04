package main

import "testing"

func TestNumericAggregateTextKeepsIntegerPrecision(t *testing.T) {
	for _, pair := range [][2]any{{"1141672", float64(1141672)}, {"9007199254740993", int64(9007199254740993)}, {"0.3333333333333333", float64(1) / 3}} {
		if err := checkRows([][]any{{pair[0]}}, [][]any{{pair[1]}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]any{{"9007199254740992", int64(9007199254740993)}, {"9223372036854775806", int64(9223372036854775807)}, {"0001", "1"}} {
		if err := checkRows([][]any{{pair[0]}}, [][]any{{pair[1]}}); err == nil {
			t.Fatalf("lost precision/type: %#v", pair)
		}
	}
}
