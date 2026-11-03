package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// testRequirement is one test a step's go test -json must show passed.
type testRequirement struct {
	Package string `json:"package"`
	Test    string `json:"test"`
}

type testEvidence struct {
	testRequirement
	Status  string `json:"status"`
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
}

func unrunStep(step gateStep, status, reason string) stepResult {
	result := stepResult{Name: step.Name, Command: append([]string(nil), step.Command...), Environment: cloneEnvironment(step.Env), Status: status, Error: reason}
	for _, requirement := range step.RequiredTests {
		result.Tests = append(result.Tests, testEvidence{testRequirement: requirement, Status: "not_run"})
	}
	return result
}

func runGatePlan(ctx context.Context, root string, steps []gateStep) ([]stepResult, string) {
	results := make([]stepResult, 0, len(steps))
	failure := ""
	for index, step := range steps {
		if failure != "" || ctx.Err() != nil {
			if failure == "" {
				failure = ctx.Err().Error()
			}
			results = append(results, unrunStep(step, "not_run", "gate stopped: "+failure))
			continue
		}
		fmt.Printf("\n[%d/%d] %s\n", index+1, len(steps), step.Name)
		result := runGateStep(ctx, root, step)
		results = append(results, result)
		if !result.Success {
			failure = result.Name + ": " + result.Error
		}
	}
	return results, failure
}

func runGateStep(ctx context.Context, root string, step gateStep) stepResult {
	started := time.Now().UTC()
	result := unrunStep(step, "failed", "")
	result.StartedAt = started
	if len(step.Command) == 0 {
		result.Error = "empty command"
		return result
	}
	if len(step.RequiredTests) > 0 && !hasArgument(step.Command, "-json") {
		result.Error = "required tests need go test -json evidence"
		return result
	}
	observer := newTestObserver(step.RequiredTests)
	command := exec.CommandContext(ctx, step.Command[0], step.Command[1:]...)
	command.Dir = root
	command.Env = environmentWith(os.Environ(), step.Env)
	command.Stdout = os.Stdout
	if hasArgument(step.Command, "-json") {
		command.Stdout = io.MultiWriter(os.Stdout, observer)
	}
	command.Stderr = os.Stderr
	err := command.Run()
	result.DurationMS = time.Since(started).Milliseconds()
	evidenceErr := observer.finish()
	result.Tests = observer.tests
	if hasArgument(step.Command, "-json") {
		result.SkippedTests = &observer.skipped
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if evidenceErr != nil {
		result.Error = evidenceErr.Error()
		skipped, failed := false, false
		for _, test := range result.Tests {
			skipped = skipped || test.Status == "skipped"
			failed = failed || test.Status == "failed"
		}
		if skipped && !failed {
			result.Status = "skipped"
		}
		return result
	}
	result.Success, result.Status = true, "passed"
	return result
}

func hasArgument(command []string, value string) bool {
	for _, argument := range command {
		if argument == value {
			return true
		}
	}
	return false
}

// Only required tests are retained. Output is streamed with a bounded line
// buffer, rather than collecting an unbounded go test transcript in RAM.
type testObserver struct {
	line    []byte
	tests   []testEvidence
	skipped int
	err     error
}

func newTestObserver(required []testRequirement) *testObserver {
	o := &testObserver{}
	for _, test := range required {
		o.tests = append(o.tests, testEvidence{testRequirement: test, Status: "not_run"})
	}
	return o
}

func (o *testObserver) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && o.err == nil {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		if len(o.line)+len(part) > 1<<20 {
			o.err = errors.New("go test JSON line exceeds 1 MiB evidence limit")
			break
		}
		o.line = append(o.line, part...)
		if end < 0 {
			break
		}
		o.observe(o.line)
		o.line = o.line[:0]
		p = p[end+1:]
	}
	return n, nil
}

func (o *testObserver) observe(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var event struct{ Action, Package, Test string }
	if err := json.Unmarshal(line, &event); err != nil {
		o.err = fmt.Errorf("decode go test JSON evidence: %w", err)
		return
	}
	if event.Action == "skip" && event.Test != "" {
		o.skipped++
	}
	for i := range o.tests {
		test := &o.tests[i]
		if test.Package != event.Package {
			continue
		}
		if event.Test == test.Test || strings.HasPrefix(event.Test, test.Test+"/") {
			switch event.Action {
			case "skip":
				test.Skipped++
			case "fail":
				test.Failed++
			case "pass":
				if event.Test == test.Test {
					test.Passed++
				}
			}
		}
	}
}

func (o *testObserver) finish() error {
	if o.err == nil && len(o.line) > 0 {
		o.observe(o.line)
	}
	for i := range o.tests {
		test := &o.tests[i]
		switch {
		case test.Failed > 0:
			test.Status = "failed"
		case test.Skipped > 0:
			test.Status = "skipped"
		case test.Passed > 0:
			test.Status = "passed"
		}
		if test.Status != "passed" && o.err == nil {
			o.err = fmt.Errorf("required test %s.%s is %s", test.Package, test.Test, test.Status)
		}
	}
	return o.err
}
