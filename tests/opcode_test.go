package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/kitwork/engine"
)

func TestOpCodes(t *testing.T) {
	t.Run("Loop and Energy", func(t *testing.T) {
		jsCode := `
			let sum = 45;
			sum;
		`
		res := engine.Script(jsCode)
		if res.Error() != nil {
			t.Fatalf("Execution error: %s", res.Error())
		}

		fmt.Printf("Result: %v, Energy: %d\n", res.Value(), res.Energy())

		if res.Value().(float64) != 45 {
			t.Errorf("Expected 45, got %v", res.Value())
		}

		if res.Energy() == 0 {
			t.Errorf("Expected non-zero energy usage")
		}
	})

	t.Run("Logical Operators", func(t *testing.T) {
		jsCode := `
			let a = true;
			let b = false;
			({
				and: a && b,
				or: a || b,
				not: !a
			});
		`
		res := engine.Script(jsCode)
		if res.Error() != nil {
			t.Fatalf("Execution error: %s", res.Error())
		}

		val := res.Value().(map[string]any)
		if val["and"].(bool) != false || val["or"].(bool) != true || val["not"].(bool) != false {
			t.Errorf("Logical ops failed: %v", val)
		}
	})

	t.Run("Defer System", func(t *testing.T) {
		jsCode := `
			let state = { x: 10 };
			defer(() => { state.x = 20; });
			state;
		`
		res := engine.Script(jsCode)
		if res.Error() != nil {
			t.Fatalf("Execution error: %s", res.Error())
		}

		val := res.Value().(map[string]any)
		if val["x"].(float64) != 20 {
			t.Errorf("Expected x to be 20 after defer, got %v", val["x"])
		}
	})

	t.Run("Spawn/Go Routine", func(t *testing.T) {
		jsCode := `
			let state = { count: 10 };
			go(() => {
				state.count = 20;
			});
			state;
		`
		res := engine.Script(jsCode)
		// Chờ xử lý goroutine (Tăng thời gian chờ để đảm bảo tính ổn định của test)
		time.Sleep(100 * time.Millisecond)

		val := res.Value().(map[string]any)
		if val["count"].(float64) != 20 {
			t.Errorf("Expected count to be 20 after spawn, got %v", val["count"])
		}
	})

	t.Run("Parallel Processing", func(t *testing.T) {
		jsCode := `
			const { user, posts } = parallel({
				user: () => "Bob",
				posts: () => ["p1", "p2"]
			});
			({ name: user, count: posts.length });
		`
		res := engine.Script(jsCode)
		if res.Error() != nil {
			t.Fatalf("Execution error: %s", res.Error())
		}

		val := res.Value().(map[string]any)
		if val["name"] != "Bob" || val["count"].(float64) != 2 {
			t.Errorf("Parallel failed: %v", val)
		}
	})
}
