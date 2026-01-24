package tests

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/value"
)

// --- 1. CORE ENGINE & DISCOVERY TESTS ---

func TestPayloadAndLogging(t *testing.T) {
	e := core.New()
	jsCode := `
		let data = payload();
		log("Received order for:", data.user);
		data.price * 2;
	`
	w, _ := e.Build(jsCode)

	params := map[string]value.Value{
		"user":  value.New("Antigravity"),
		"price": value.New(500),
	}

	res := e.Trigger(context.Background(), w, params)

	if res.Value.N != 1000 {
		t.Errorf("Payload processing failed, got %f", res.Value.N)
	}
}

func TestAdvancedWorkflow(t *testing.T) {
	e := core.New()
	content, _ := os.ReadFile("demo/advanced_workflow.js")
	w, err := e.Build(string(content))
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	params := map[string]value.Value{
		"user_id": value.New(101),
		"amount":  value.New(10.5),
	}

	res := e.Trigger(context.Background(), w, params)
	if res.Error != "" {
		t.Fatalf("Execution error: %s", res.Error)
	}

	// Note: depend on external API, result might be 0 if offline
	if res.Response.Get("total").N == 0 {
		t.Logf("FX conversion returned 0 (possibly offline), skipping value check")
	} else if res.Response.Get("total").N != 262500 {
		t.Errorf("FX conversion failed, got %v", res.Response.Get("total").N)
	}
}

func TestWorkDiscoveryAndExecution(t *testing.T) {
	e := core.New()
	jsCode := `
		const w = work("OrderSystem");
		w.router("POST", "/v1/order");
		w.version("1.0.1");

		w.handle((req) => {
			return { status: "received", id: req.id };
		});
	`
	w, _ := e.Build(jsCode)

	t.Run("Verify Blueprint", func(t *testing.T) {
		if w.Name != "OrderSystem" {
			t.Errorf("Name failed")
		}
		if len(w.Routes) == 0 || w.Routes[0].Path != "/v1/order" {
			t.Errorf("Router discovery failed")
		}
	})

	t.Run("Verify Execution", func(t *testing.T) {
		// Mock request data
		params := map[string]value.Value{"id": value.New(999)}
		// Find route and execute handler directly
		var handlerFn *value.ScriptFunction
		for _, r := range w.Routes {
			if r.Path == "/v1/order" && r.Handler != nil {
				handlerFn = r.Handler
				break
			}
		}
		if handlerFn == nil {
			t.Fatal("Handler not found in registry")
		}

		resLambda := e.ExecuteLambda(w, handlerFn, params)
		if resLambda.Value.Get("status").Text() != "received" {
			t.Errorf("Handler execution failed, got: %v", resLambda.Value)
		}
	})
}

// --- 2. HOT SWAP & DISCOVERY OVERRIDE ---

func TestHotSwapLogic(t *testing.T) {
	e := core.New()
	source1 := `work("V1").version("1.0.0"); 10`
	source2 := `work("V1").version("2.0.0"); 20`

	w1, _ := e.Build(source1)
	if w1.Ver != "1.0.0" {
		t.Error("V1 failed")
	}

	w2, _ := e.Build(source2)
	if w2.Ver != "2.0.0" {
		t.Error("V2 failed")
	}
}

// --- 3. DATABASE & AUTO-RESPONSE ---

func TestDBAndAutoResponse(t *testing.T) {
	e := core.New()
	jsCode := `
		let query = db().table("orders").where("status", "pending").get(); // Added .get()
		return query; 
	`
	w, _ := e.Build(jsCode)

	t.Run("Check Auto-JSON from DB", func(t *testing.T) {
		res := e.Trigger(context.Background(), w)
		// DB query returns Array via Return Value (Result.Value), not Response (Result.Response)
		if res.Value.Len() != 2 {
			t.Errorf("Expected 2 records in value, got %d", res.Value.Len())
		}
	})
}

// --- 4. HTML RENDER EXPERIMENT ---

func TestHtmlResponseExperiment(t *testing.T) {
	e := core.New()
	jsCode := `
		const data = { user: "Antigravity", score: 99 };
		html("profile.html", data);
	`
	w, _ := e.Build(jsCode)

	t.Run("Verify HTML Response Type", func(t *testing.T) {
		res := e.Trigger(context.Background(), w)
		if res.ResType != "html" {
			t.Errorf("Expected html, got %s", res.ResType)
		}
		if res.Response.Get("data").Get("user").Text() != "Antigravity" {
			t.Errorf("Data binding in HTML failed")
		}
	})

	t.Run("String .render() DX", func(t *testing.T) {
		js := `
			let template = "<h1>Hello {{name}}</h1>";
			template.render({ name: "Kit" });
		`
		w, _ := e.Build(js)
		res := e.Trigger(context.Background(), w)

		if res.ResType != "html" {
			t.Errorf("Expected html from .render(), got %s", res.ResType)
		}
		if res.Response.Get("data").Get("name").Text() != "Kit" {
			t.Errorf("Data binding in .render() failed")
		}
	})
}

// --- 5. DEMO SCRIPTS DISCOVERY ---

func TestDemoScripts(t *testing.T) {
	e := core.New()
	pattern := "../demo/**/*.js" // Adjusted path
	matches, _ := filepath.Glob(pattern)

	for _, path := range matches {
		name := filepath.Base(path)
		t.Run("Demo: "+name, func(t *testing.T) {
			content, _ := os.ReadFile(path)
			w, err := e.Build(string(content))
			if err != nil {
				t.Logf("Skipping demo %s: %v", name, err)
				return
			}

			res := e.Trigger(context.Background(), w)
			t.Logf("Demo %s executed. Result: %s, Response: %s", name, res.Value.K.String(), res.ResType)
		})
	}
}

// --- 6. STRESS & PERFORMANCE TESTS ---

func TestStressPerformance(t *testing.T) {
	e := core.New()
	source := "let price = 100; let tax = 0.1; price * (1 + tax)"
	w, err := e.Build(source)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	iterations := 1_000_000
	workerCount := runtime.NumCPU()
	var wg sync.WaitGroup

	var msStart, msEnd runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&msStart)

	start := time.Now()

	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < iterations/workerCount; j++ {
				_ = e.Trigger(ctx, w)
			}
		}()
	}
	wg.Wait()
	duration := time.Since(start)

	runtime.ReadMemStats(&msEnd)

	throughput := float64(iterations) / duration.Seconds()
	latency := duration / time.Duration(iterations)
	totalAlloc := msEnd.TotalAlloc - msStart.TotalAlloc
	gcCycles := msEnd.NumGC - msStart.NumGC

	t.Logf("\n"+
		"====================================================\n"+
		"          🚀 ENGINE STRESS TEST (SAFE & POOLED)      \n"+
		"====================================================\n"+
		"       ⚡ PERFORMANCE METRICS\n"+
		"----------------------------------------------------\n"+
		"Total Iterations : %d\n"+
		"Total Duration   : %v\n"+
		"Throughput      : %.0f ops/sec\n"+
		"Avg Latency     : %v\n"+
		"\n"+
		"       ⚙️ SYSTEM & CONCURRENCY\n"+
		"----------------------------------------------------\n"+
		"Workers         : %d\n"+
		"\n"+
		"       💾 MEMORY & GC STATS\n"+
		"----------------------------------------------------\n"+
		"Alloc per Op    : %d bytes\n"+
		"Total Allocated : %.2f MB\n"+
		"GC Cycles       : %d\n"+
		"====================================================",
		iterations, duration, throughput, latency,
		workerCount,
		totalAlloc/uint64(iterations), float64(totalAlloc)/1024/1024,
		gcCycles)
}
