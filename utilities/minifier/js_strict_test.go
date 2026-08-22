package minifier

import (
	"fmt"
	"sync"
	"testing"
)

func TestJSStrictIsDeterministicAndConcurrencySafe(t *testing.T) {
	input := `; (function (global) {
  "use strict";
  global.__kitMinifierContract = function (left, right) {
    return left + right;
  };
})(globalThis);
`
	want, err := JSStrict(input)
	if err != nil {
		t.Fatal(err)
	}

	const (
		workers    = 32
		iterations = 32
	)
	start := make(chan struct{})
	errors := make(chan error, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for worker := range workers {
		go func() {
			defer group.Done()
			<-start
			for iteration := range iterations {
				got, strictErr := JSStrict(input)
				if strictErr != nil {
					errors <- fmt.Errorf("worker %d iteration %d: %w", worker, iteration, strictErr)
					return
				}
				if got != want {
					errors <- fmt.Errorf("worker %d iteration %d produced different bytes", worker, iteration)
					return
				}
			}
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
