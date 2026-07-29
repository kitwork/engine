package compiler

import (
	"bytes"
	"testing"

	"github.com/kitwork/engine/runtime"
)

func TestCompilerEmitsCommitAtExpressionBoundaries(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []byte
	}{
		{
			name: "discarded expression",
			src:  `1;`,
			want: []byte{
				byte(runtime.PUSH), 0, 0,
				byte(runtime.COMMIT),
				byte(runtime.POP),
				byte(runtime.RETURN),
			},
		},
		{
			name: "variable declaration",
			src:  `const x = 1;`,
			want: []byte{
				byte(runtime.PUSH), 0, 0,
				byte(runtime.STORE), 0, 1,
				byte(runtime.COMMIT),
				byte(runtime.POP),
				byte(runtime.RETURN),
			},
		},
		{
			name: "return",
			src:  `return 1;`,
			want: []byte{
				byte(runtime.PUSH), 0, 0,
				byte(runtime.COMMIT),
				byte(runtime.RETURN),
				byte(runtime.RETURN),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bytecode, err := CompileSource(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			instructions := bytecode.Instructions()
			if !bytes.Equal(instructions, tt.want) {
				t.Fatalf("instructions = %v, want %v", instructions, tt.want)
			}
		})
	}
}
