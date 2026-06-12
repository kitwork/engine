package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

// buildLinearScript tạo script tuyến tính có số instruction biết trước:
// mỗi câu `x = x + 7 * 3 - 2;` biên dịch thành đúng 9 instruction
// (LOAD, PUSH, PUSH, MUL, ADD, PUSH, SUB, STORE, POP).
func buildLinearScript(statements int) (src string, instrPerRun float64) {
	var sb strings.Builder
	sb.WriteString("let x = 0;\n")
	for i := 0; i < statements; i++ {
		sb.WriteString("x = x + 7 * 3 - 2;\n")
	}
	// prolog `let x = 0` ≈ 2 instruction (PUSH, STORE) + RETURN cuối chương trình
	return sb.String(), float64(statements*9 + 3)
}

func compileSource(b *testing.B, src string) *compiler.Bytecode {
	b.Helper()
	l := compiler.NewLexer(src)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		b.Fatalf("parse error: %s", p.Errors()[0])
	}
	c := compiler.NewCompiler(src)
	if err := c.Compile(prog); err != nil {
		b.Fatalf("compile error: %v", err)
	}
	return c.ByteCodeResult()
}

// BenchmarkVMCoreOps đo tốc độ thực thi bytecode thuần của VM.
// Chạy: go test ./work/ -bench VMCoreOps -benchtime 2s -run xxx
// Metric "mops/s" = triệu instruction/giây (kiểm chứng con số ~14.1M ops/s).
func BenchmarkVMCoreOps(b *testing.B) {
	src, instrPerRun := buildLinearScript(2000)
	bc := compileSource(b, src)

	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.MaxEnergy = 0 // không giới hạn để đo throughput thô

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.FastReset(bc.Instructions, bc.Constants, vm.Globals, bc.SourceMap)
		res := vm.Run()
		_ = res
	}
	b.StopTimer()

	totalInstr := instrPerRun * float64(b.N)
	seconds := b.Elapsed().Seconds()
	b.ReportMetric(totalInstr/seconds/1e6, "mops/s")
	b.ReportMetric(seconds/totalInstr*1e9, "ns/instr")
}

// BenchmarkColdBootScript đo thời gian "cold boot" cấp script:
// lexer → parser → compiler → VM mới → thực thi, cho một app cỡ thực tế.
func BenchmarkColdBootScript(b *testing.B) {
	src, _ := buildLinearScript(300) // ~300 dòng, cỡ một app.kitwork.js thật

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := compiler.NewLexer(src)
		p := compiler.NewParser(l)
		prog := p.ParseProgram()
		if len(p.Errors()) > 0 {
			b.Fatal(p.Errors()[0])
		}
		c := compiler.NewCompiler(src)
		if err := c.Compile(prog); err != nil {
			b.Fatal(err)
		}
		bc := c.ByteCodeResult()
		vm := runtime.New(bc.Instructions, bc.Constants)
		_ = vm.Run()
	}
}

// BenchmarkColdBootTenant đo cold boot cấp tenant đầy đủ:
// esbuild bundle + compile + chạy app.kitwork.js đăng ký route.
// Đây mới là con số "cold boot" mà người dùng cảm nhận khi hot reload.
func BenchmarkColdBootTenant(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "kitwork-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		b.Fatal(err)
	}

	script := `
import { router } from "kitwork"

const fmtPrice = (n) => n.toFixed(0)

router.get("/hello").handle((req, res) => {
    return res.text("hello world")
})

router.get("/api/json").handle((req, res) => {
    return res.json({ id: 1, name: "kitwork", runtime: true, price: fmtPrice(1500) })
})
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(script), 0644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tenant := NewTenant(tmpDir, "localhost")
		if err := tenant.Run(); err != nil {
			b.Fatal(err)
		}
	}
}
