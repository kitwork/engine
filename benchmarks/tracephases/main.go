// Command tracephases summarizes KitDB regions from `go tool trace -d=parsed`.
// The debug format is Go-version-specific; malformed/unmatched regions fail closed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var event = regexp.MustCompile(`G=(\d+) Region(Begin|End) Time=(\d+) Task=\d+ Type="(kitdb\.[^"]+)"`)

type span struct {
	name string
	at   int64
}
type phase struct {
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	TotalMS float64 `json:"summed_wall_ms"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
	MaxMS   float64 `json:"max_ms"`
}

func summarize(input io.Reader) ([]phase, error) {
	stacks := make(map[string][]span)
	samples := make(map[string][]int64)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "Region") || !strings.Contains(line, `Type="kitdb.`) {
			continue
		}
		match := event.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("unsupported trace event: %s", line)
		}
		count++
		if count > 2_000_000 {
			return nil, fmt.Errorf("trace region event limit exceeded")
		}
		at, err := strconv.ParseInt(match[3], 10, 64)
		if err != nil {
			return nil, err
		}
		id, name := match[1], match[4]
		stack := stacks[id]
		if match[2] == "Begin" {
			stacks[id] = append(stack, span{name, at})
			continue
		}
		if len(stack) == 0 || stack[len(stack)-1].name != name || at < stack[len(stack)-1].at {
			return nil, fmt.Errorf("unmatched trace end: %s", line)
		}
		duration := at - stack[len(stack)-1].at
		samples[name] = append(samples[name], duration)
		if strings.HasPrefix(name, "kitdb.storage.") {
			for _, parent := range stack[:len(stack)-1] {
				if parent.name == "kitdb.checkpoint.execute" {
					key := "kitdb.checkpoint.execute/" + name
					samples[key] = append(samples[key], duration)
					break
				}
			}
		}
		stacks[id] = stack[:len(stack)-1]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("no KitDB trace regions; build with -tags=kitdb_profile")
	}
	for id, stack := range stacks {
		if len(stack) != 0 {
			return nil, fmt.Errorf("unfinished regions on goroutine %s", id)
		}
	}
	result := make([]phase, 0, len(samples))
	for name, values := range samples {
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		var total int64
		for _, value := range values {
			total += value
		}
		quantile := func(percent int) float64 { return float64(values[(len(values)*percent+99)/100-1]) / 1e6 }
		result = append(result, phase{name, len(values), float64(total) / 1e6, quantile(50), quantile(95), quantile(99), float64(values[len(values)-1]) / 1e6})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func main() {
	var input io.Reader = os.Stdin
	var command *exec.Cmd
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: tracephases [trace-file]")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		command = exec.Command("go", "tool", "trace", "-d=parsed", os.Args[1])
		command.Stderr = os.Stderr
		pipe, err := command.StdoutPipe()
		if err == nil {
			err = command.Start()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		input = pipe
	}
	result, err := summarize(input)
	if command != nil {
		if err != nil {
			_ = command.Process.Kill()
		}
		waitErr := command.Wait()
		if err == nil {
			err = waitErr
		}
	}
	if err == nil {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(result)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
