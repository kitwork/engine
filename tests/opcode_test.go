package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kitwork/engine/core"
)

func TestForLoopAndEnergy(t *testing.T) {
	e := core.New()
	jsCode := `
		let list = [10, 20, 30];
		let sum = 0;
		for (item in list) {
			sum = sum + item;
		}
		return sum;
	`
	w, err := e.Build(jsCode)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	res := e.Trigger(context.Background(), w)
	if res.Error != "" {
		t.Fatalf("Execution error: %s", res.Error)
	}

	fmt.Printf("Result: %v, Energy: %d\n", res.Value.Interface(), res.Energy)

	if res.Value.N != 60 {
		t.Errorf("Expected 60, got %v", res.Value.N)
	}

	if res.Energy == 0 {
		t.Errorf("Expected non-zero energy usage")
	}
}

func TestLogicalOps(t *testing.T) {
	e := core.New()
	jsCode := `
		let a = true;
		let b = false;
		return {
			and: a && b,
			or: a || b,
			not: !a
		};
	`
	w, err := e.Build(jsCode)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	res := e.Trigger(context.Background(), w)
	if res.Error != "" {
		t.Fatalf("Execution error: %s", res.Error)
	}

	fmt.Printf("Logical Result: %v, Energy: %d\n", res.Value.Interface(), res.Energy)

	val := res.Value.Map()
	if val["and"].Truthy() != false {
		t.Error("and failed")
	}
	if val["or"].Truthy() != true {
		t.Error("or failed")
	}
	if val["not"].Truthy() != false {
		t.Error("not failed")
	}
}

func TestDefer(t *testing.T) {
	e := core.New()
	jsCode := `
		let state = { x: 10 };
		defer(() => { state.x = 20; });
		return state;
	`
	w, err := e.Build(jsCode)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	res := e.Trigger(context.Background(), w)
	if res.Error != "" {
		t.Fatalf("Execution error: %s", res.Error)
	}

	fmt.Printf("Defer Result: %v, Energy: %d\n", res.Value.Interface(), res.Energy)

	if res.Value.Get("x").N != 20 {
		t.Errorf("Expected x to be 20 after defer, got %v", res.Value.Get("x").N)
	}
}

func TestSpawn(t *testing.T) {
	e := core.New()
	jsCode := `
		let state = { count: 10 };
		go(() => {
			state.count = 20;
		});
		return state;
	`
	w, err := e.Build(jsCode)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	res := e.Trigger(context.Background(), w)

	// Wait a bit for the goroutine to finish (DSL side side-effect)
	time.Sleep(10 * time.Millisecond)

	fmt.Printf("Spawn Final State: %v, Energy: %d\n", res.Value.Interface(), res.Energy)

	if res.Value.Get("count").N != 20 {
		t.Errorf("Expected count to be 20 after spawn execution, got %v", res.Value.Get("count").N)
	}
}

func TestParallel(t *testing.T) {
	e := core.New()
	jsCode := `
		const { user, posts } = parallel({
			user: () => { return "Bob"; },
			posts: () => { return ["p1", "p2"]; }
		});
		return { user: user, postCount: posts.length };
	`
	w, err := e.Build(jsCode)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	res := e.Trigger(context.Background(), w)
	if res.Error != "" {
		t.Fatalf("Execution error: %s", res.Error)
	}

	fmt.Printf("Parallel Result: %v, Energy: %d\n", res.Value.Interface(), res.Energy)

	val := res.Value.Map()
	if val["user"].Text() != "Bob" {
		t.Errorf("Expected Bob, got %v", val["user"].Text())
	}
	if val["postCount"].N != 2 {
		t.Errorf("Expected 2 posts, got %v", val["postCount"].N)
	}

	// Part 2: Array Destructuring
	jsCode2 := `
		const [ first, second ] = parallel([
			() => { return "First Task"; },
			() => { return "Second Task"; }
		]);
		return { a: first, b: second };
	`
	w2, err2 := e.Build(jsCode2)
	if err2 != nil {
		t.Fatalf("Build 2 failed: %v", err2)
	}
	res2 := e.Trigger(context.Background(), w2)
	fmt.Printf("Parallel Array Result: %v\n", res2.Value.Interface())
	val2 := res2.Value.Map()
	if val2["a"].Text() != "First Task" {
		t.Errorf("Expected First Task, got %v", val2["a"].Text())
	}
	if val2["b"].Text() != "Second Task" {
		t.Errorf("Expected Second Task, got %v", val2["b"].Text())
	}
}
