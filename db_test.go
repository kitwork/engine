package engine

import (
	"context"
	"testing"
)

func TestDBAndAutoResponse(t *testing.T) {
	e := New()

	source := `
		work({ name: "db_test" }).router("POST", "/users");

		// DB query builder: Trả về kết quả cuối cùng mà không cần gọi json()
		return db().from("users").take(3);
	`

	w, err := e.Build(source)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Trigger - Giả lập engine chạy khi có request
	e.Trigger(context.Background(), w)

	t.Run("Check Auto-JSON from DB", func(t *testing.T) {
		resp := w.Response
		if !resp.IsArray() {
			t.Fatalf("Expected Array response from DB, got %s", resp.K.String())
		}
		if resp.Len() != 3 {
			t.Errorf("Expected 3 items, got %d", resp.Len())
		}

		t.Logf("Response: %s", resp.Text())
	})
}

func TestValueChainingDX(t *testing.T) {
	e := New()

	source := `
		let val = 123.45;
		let s = val.string();
		let i = val.int();
		
		{
			str: s,
			integer: i,
			original: val.json() // Chaining .json()
		};
	`

	w, err := e.Build(source)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	res := e.Trigger(context.Background(), w)
	resp := w.Response

	t.Run("Check Chaining Results", func(t *testing.T) {
		if resp.Get("str").Text() != "123.45" {
			t.Errorf("Expected '123.45', got %v", resp.Get("str").Text())
		}
		if resp.Get("integer").Float() != 123 {
			t.Errorf("Expected 123, got %v", resp.Get("integer").Float())
		}
		t.Logf("Chaining Result: %s", resp.Text())
	})
}
