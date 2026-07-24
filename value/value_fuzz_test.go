package value

import (
	"testing"
)

func FuzzValueConversion(f *testing.F) {
	seeds := []string{
		`hello`,
		`12345`,
		`true`,
		`{"key":"val"}`,
		`[1,2,3]`,
		`null`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		vStr := NewString(input)
		_ = vStr.Text()
		_ = vStr.Truthy()
		_ = vStr.Interface()

		vNum := New(float64(len(input)))
		_ = vNum.Text()
		_ = vNum.Truthy()
		_ = vNum.Interface()
	})
}
