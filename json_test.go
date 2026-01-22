package engine

import (
	"context"
	"testing"
)

func TestJsonResponseExperiment(t *testing.T) {
	e := New()

	// Kịch bản: Một request Get trả về JSON với thông tin thời gian hiện tại
	source := `
		work({ name: "ApiServer" }).router("GET", "/status");

		let data = {
			status: "success",
			timestamp: now(),
			code: 200
		};

		json(data);
	`

	w, err := e.Build(source)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Thực thi - Trigger giống như khi có request HTTP tới
	res := e.Trigger(context.Background(), w)

	t.Run("Check Response Body", func(t *testing.T) {
		response := w.Response

		if response.IsNil() || response.IsInvalid() {
			t.Fatalf("Expected JSON object, got %s (Text: %s)", response.K.String(), response.Text())
		}

		if response.Get("status").Text() != "success" {
			t.Errorf("Expected status success, got %v", response.Get("status").Text())
		}

		t.Logf("Final JSON Response: %v", response.Text())
	})

	t.Run("Check Shortcut Execution", func(t *testing.T) {
		// Kết quả trả về của json(data) là context work
		if res.K.String() != "struct" {
			t.Errorf("Expected work context return, got %v", res.K.String())
		}
	})
}
