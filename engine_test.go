package engine

import (
	"context"
	"testing"
)

func TestWorkDiscoveryAndExecution(t *testing.T) {
	// 1. Khởi tạo Engine
	e := New()

	// 2. Đoạn mã JS mô phỏng: Khai báo Blueprint + Logic thực thi
	jsCode := `
		const w = work({ name: "OrderSystem" });
		
		w.router("POST", "/v1/order");
		w.retry(3, "1s");
		w.version("v1.0.2");

		let price = 100;
		let tax = 0.1;
		let total = price * (1 + tax);

		w.print("Processing order total:", total);
		
		total; // Trả về kết quả cuối cùng
	`

	// 3. Chạy quá trình Build (Discovery Phase)
	w, err := e.Build(jsCode)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// 4. Kiểm tra xem Blueprint có được "đúc" đúng không
	t.Run("Verify Blueprint", func(t *testing.T) {
		if w.Name != "OrderSystem" {
			t.Errorf("Expected name OrderSystem, got %s", w.Name)
		}
		if w.Kind() != "router" {
			t.Errorf("Expected kind router, got %s", w.Kind())
		}
		if len(w.Routes) != 1 || w.Routes[0].Path != "/v1/order" {
			t.Errorf("Router not registered correctly")
		}
		if w.Retries != 3 {
			t.Errorf("Expected retries 3, got %d", w.Retries)
		}
		t.Logf("Blueprint ID: %s", w.ID())
	})

	// 5. Kiểm tra khả năng thực thi (Execution Phase)
	t.Run("Verify Execution", func(t *testing.T) {
		ctx := context.Background()
		// Kích hoạt Work thông qua Engine
		result := e.Trigger(ctx, w)

		// Kiểm tra kết quả tính toán trong JS (100 * 1.1 = 110)
		if result.Float() < 109.99 || result.Float() > 110.01 {
			t.Errorf("Expected result 110 (with tolerance), got %v", result.Float())
		}
	})
}

func TestHotSwapLogic(t *testing.T) {
	e := New()

	// Logic cũ
	code1 := `const w = work("App"); w.router("GET", "/"); "Old"`
	w1, _ := e.Build(code1)

	// Logic mới (cùng Route nhưng logic khác)
	code2 := `const w = work("App"); w.router("GET", "/"); "New"`
	w2, _ := e.Build(code2)

	// ID phải giống nhau vì cùng Route (RT:GET/)
	if w1.ID() != w2.ID() {
		t.Errorf("ID should be deterministic based on routes")
	}

	// Nhưng thực thi phải ra kết quả mới
	res := e.Trigger(context.Background(), w2)
	if res.Text() != "New" {
		t.Errorf("Execution should use the new EntryBlock")
	}
}
