package value

import (
	"testing"
)

func TestValueConstructorsAndTypes(t *testing.T) {
	vStr := NewString("hello")
	if vStr.K != String || vStr.Text() != "hello" {
		t.Fatalf("Expected string 'hello', got %v", vStr)
	}

	vNum := New(42)
	if vNum.K != Number || vNum.N != 42 {
		t.Fatalf("Expected number 42, got %v", vNum)
	}

	vBool := New(true)
	if vBool.K != Bool || !vBool.Truthy() {
		t.Fatalf("Expected boolean true, got %v", vBool)
	}
}
