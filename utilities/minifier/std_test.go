//go:build stdminify

package minifier

import "testing"

func TestJSStrictStdMinifyIsExplicitPassthrough(t *testing.T) {
	for _, input := range []string{
		`const  add  =  ( left , right )  =>  left  +  right ;`,
		`function broken( {`,
	} {
		output, err := JSStrict(input)
		if err != nil {
			t.Fatalf("explicit stdminify passthrough returned an error for %q: %v", input, err)
		}
		if output != input {
			t.Fatalf("explicit stdminify passthrough changed %q to %q", input, output)
		}
	}
}
