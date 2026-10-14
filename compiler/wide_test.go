package compiler

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
)

// A program longer than a two-byte jump reaches (bytecode 3, 14/10/2026). apptop.vn's largest route
// compiled to 65,038 bytes of the 65,535 a program could hold; when a jump's address passes 65,535
// the compiler writes the program again with every jump wide (three bytes), and a constant past index
// 65,535 is read with a wide PUSH, LOAD or STORE. A program that fits is written as before.

// wideFeatures is one function that writes every kind of jump: for…of with break (ITER, FALSE, JUMP),
// a counting for, a ternary, ?. and ??, && and ||, switch and a destructuring default.
const wideFeatures = `(items, limit) => {
	let total = 0;
	for (const item of items) {
		if (item > limit) { break; }
		total = total + (item % 2 === 0 ? item : -item);
	}
	for (let i = 0; i < 3; i++) { total = total + i; }
	const o = { a: { b: 2 } };
	const none = null;
	const picked = none?.a ?? o?.a?.b;
	const both = (total > 0 && picked) || "none";
	let word = "";
	switch (picked) {
	case 1: word = "one"; break;
	case 2: word = "two"; break;
	default: word = "many";
	}
	const { x = 5 } = {};
	return [total, picked, both, word, x];
}`

// wideSource is the features before and after `padding` statements of 12 bytes and three constants
// each, so the second copy of the features sits past the padding.
func wideSource(padding int, tail string) string {
	var source strings.Builder
	source.WriteString("let pad = 0;\nconst early = " + wideFeatures + ";\nvar first = early([1, 2, 3, 10], 5);\n")
	for index := 0; index < padding; index++ {
		source.WriteString("pad = pad + 1;\n")
	}
	source.WriteString("const late = " + wideFeatures + ";\nvar last = late([1, 2, 3, 10], 5);\n")
	source.WriteString("const twice = (n) => n * 2;\n" + tail)
	return source.String()
}

func runWide(t *testing.T, compiled *Bytecode) *runtime.VM {
	t.Helper()
	vm := runtime.New(compiled.Program)
	builtins.InjectJSCompat(vm.Globals)
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	return vm
}

func opcodeCounts(compiled *Bytecode) map[string]int {
	counts := map[string]int{}
	for _, opcode := range compiled.Program.Profile().Opcodes {
		counts[opcode.Name] = opcode.Count
	}
	return counts
}

func TestAProgramPastTwoByteJumpsCompilesWideAndRunsTheSame(t *testing.T) {
	const tail = "var result = [first, last, twice(21), pad];\n"
	small, err := CompileSource(wideSource(0, tail))
	if err != nil {
		t.Fatal(err)
	}
	big, err := CompileSource(wideSource(23_000, tail))
	if err != nil {
		t.Fatal(err)
	}

	smallCounts, bigCounts := opcodeCounts(small), opcodeCounts(big)
	if small.Len() > runtime.MaxNarrowOperand {
		t.Fatalf("the small program is %d bytes", small.Len())
	}
	for _, name := range []string{"PUSH_WIDE", "LOAD_WIDE", "STORE_WIDE", "JUMP_WIDE", "TRUE_WIDE", "FALSE_WIDE", "ITER_WIDE"} {
		if smallCounts[name] != 0 {
			t.Fatalf("a program that fits two bytes holds %s: %v", name, smallCounts)
		}
		if bigCounts[name] == 0 {
			t.Fatalf("the %d-byte program with %d constants holds no %s: %v", big.Len(), len(big.Constants()), name, bigCounts)
		}
	}
	for _, name := range []string{"JUMP", "TRUE", "FALSE", "ITER"} {
		if bigCounts[name] != 0 || smallCounts[name] == 0 {
			t.Fatalf("%s: %d in the wide program, %d in the small one (every jump of a wide program is wide)", name, bigCounts[name], smallCounts[name])
		}
	}
	if big.Len() <= runtime.MaxNarrowOperand || len(big.Constants()) <= runtime.MaxNarrowOperand+1 {
		t.Fatalf("the big program is %d bytes and %d constants: not past two bytes", big.Len(), len(big.Constants()))
	}

	const want = `[[1,2,2,"two",5],[1,2,2,"two",5],42,%d]`
	smallResult, _ := runWide(t, small).Vars["result"].MarshalJSON()
	bigResult, _ := runWide(t, big).Vars["result"].MarshalJSON()
	if string(smallResult) != strings.Replace(want, "%d", "0", 1) || string(bigResult) != strings.Replace(want, "%d", "23000", 1) {
		t.Fatalf("small %s, big %s", smallResult, bigResult)
	}

	again, err := CompileSource(wideSource(23_000, tail))
	if err != nil {
		t.Fatal(err)
	}
	if again.Checksum() != big.Checksum() {
		t.Fatal("compiling the wide program twice gave two programs")
	}
	if err := ValidateArtifact(big); err != nil {
		t.Fatalf("the wide program's artifact: %v", err)
	}
}

func TestAJumpJustPastTwoBytesMakesTheProgramWide(t *testing.T) {
	// the if at the end jumps to the program's last instruction, so the last jump's address is the
	// program's length less one
	const tail = "var result = [first, last, twice(21), pad];\nif (pad < 0) { pad = 0; }\n"
	base, err := CompileSource(wideSource(0, tail))
	if err != nil {
		t.Fatal(err)
	}
	// each padding statement is 12 bytes: from a little under the reach of two bytes to a little past
	fits := (runtime.MaxNarrowOperand - base.Len()) / 12
	narrow, wide := 0, 0
	for padding := fits - 2; padding <= fits+3; padding++ {
		compiled, err := CompileSource(wideSource(padding, tail))
		if err != nil {
			t.Fatal(err)
		}
		report, err := runtime.InspectProgram(compiled.Program)
		if err != nil {
			t.Fatal(err)
		}
		farthest, wideJumps, narrowJumps := 0, 0, 0
		for _, instruction := range report.Instructions {
			if instruction.Target == nil {
				continue
			}
			farthest = max(farthest, *instruction.Target)
			if instruction.Opcode.IsWide() {
				wideJumps++
			} else {
				narrowJumps++
			}
		}
		switch {
		case wideJumps == 0 && farthest <= runtime.MaxNarrowOperand:
			narrow++
		case narrowJumps == 0 && farthest > runtime.MaxNarrowOperand && compiled.Len() > runtime.MaxNarrowOperand:
			wide++
		default:
			t.Fatalf("padding %d: %d bytes, %d narrow and %d wide jumps, farthest address %d", padding, compiled.Len(), narrowJumps, wideJumps, farthest)
		}
		result, _ := runWide(t, compiled).Vars["result"].MarshalJSON()
		if want := `[[1,2,2,"two",5],[1,2,2,"two",5],42,` + itoa(padding) + `]`; string(result) != want {
			t.Fatalf("padding %d: result %s, want %s", padding, result, want)
		}
	}
	if narrow == 0 || wide == 0 {
		t.Fatalf("%d narrow and %d wide programs: the range does not cross two bytes", narrow, wide)
	}
}

func TestAFailureInAWideProgramNamesTheCallAndItsPlace(t *testing.T) {
	tail := "const o = { x: 3 };\nconst r = safe(() => o.x());\nvar result = [r.error.message, r.error.at];\n"
	source := wideSource(23_000, tail)
	line := strings.Count(source, "\n") - 1
	compiled, err := CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if opcodeCounts(compiled)["JUMP_WIDE"] == 0 {
		t.Fatal("the program is not wide")
	}
	result := runWide(t, compiled).Vars["result"]
	if message, at := result.At(0).Text(), result.At(1).Text(); message != "o.x không phải hàm (là số): không gọi được" ||
		!strings.Contains(at, ":"+itoa(line)+":") {
		t.Fatalf("message %q at %q, want o.x … at line %d", message, at, line)
	}
}

func TestAProgramPastTheLimitIsACompileErrorAtItsPlace(t *testing.T) {
	// an array of 200,000 numbers: about 2.1 MiB of code, past MaxBytecodeSize
	var source strings.Builder
	source.WriteString("const before = 1;\nvar result = [")
	for index := 0; index < 200_000; index++ {
		source.WriteString("1,")
	}
	source.WriteString("1];\n")
	_, err := CompileSource(source.String())
	if err == nil {
		t.Fatal("a program past the limit compiled")
	}
	if message := err.Error(); !strings.HasPrefix(message, "<source>:2:") ||
		!strings.Contains(message, "quá giới hạn 2097152 byte của một chương trình") {
		t.Fatalf("error = %q", message)
	}
}

func itoa(number int) string {
	var digits []byte
	for {
		digits = append([]byte{byte('0' + number%10)}, digits...)
		number /= 10
		if number == 0 {
			return string(digits)
		}
	}
}
