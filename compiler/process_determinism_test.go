package compiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	coldProcessHelperEnv = "KITWORK_COMPILER_COLD_PROCESS_HELPER"
	coldProcessOutputEnv = "KITWORK_COMPILER_COLD_PROCESS_OUTPUT"
)

type coldProcessCompilerReport struct {
	CompilerFingerprint string                     `json:"compiler_fingerprint"`
	Fixtures            []coldProcessFixtureReport `json:"fixtures"`
}

type coldProcessFixtureReport struct {
	Name              string `json:"name"`
	ProgramChecksum   string `json:"program_checksum"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	ArtifactBytes     int    `json:"artifact_bytes"`
	SourceFingerprint string `json:"source_fingerprint"`
	CacheKey          string `json:"cache_key"`
}

func TestCompilerV2ArtifactDeterminismAcrossColdProcesses(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var baseline []byte
	for iteration := 0; iteration < 6; iteration++ {
		outputPath := filepath.Join(t.TempDir(), fmt.Sprintf("compiler-%d.json", iteration))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(
			ctx,
			executable,
			"-test.run=^TestCompilerArtifactDeterminismColdProcessHelper$",
			"-test.count=1",
		)
		command.Env = coldProcessEnvironment(outputPath)
		output, commandErr := command.CombinedOutput()
		contextErr := ctx.Err()
		cancel()
		if contextErr != nil {
			t.Fatalf("cold compiler process %d: %v", iteration, contextErr)
		}
		if commandErr != nil {
			t.Fatalf(
				"cold compiler process %d: %v\n%s",
				iteration,
				commandErr,
				output,
			)
		}

		data, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("read cold compiler report %d: %v", iteration, err)
		}
		var report coldProcessCompilerReport
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("decode cold compiler report %d: %v", iteration, err)
		}
		if report.CompilerFingerprint != Fingerprint() || len(report.Fixtures) != 4 {
			t.Fatalf("invalid cold compiler report %d: %+v", iteration, report)
		}
		if iteration == 0 {
			baseline = append([]byte(nil), data...)
			continue
		}
		if !bytes.Equal(data, baseline) {
			t.Fatalf(
				"compiler artifacts changed across cold processes\nbaseline: %s\n process: %s",
				baseline,
				data,
			)
		}
	}
}

func TestCompilerArtifactDeterminismColdProcessHelper(t *testing.T) {
	if os.Getenv(coldProcessHelperEnv) != "1" {
		t.Skip("cold compiler helper process")
	}
	outputPath := os.Getenv(coldProcessOutputEnv)
	if outputPath == "" {
		t.Fatal("cold compiler helper output path is empty")
	}

	report, err := buildColdProcessCompilerReport()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildColdProcessCompilerReport() (coldProcessCompilerReport, error) {
	fixtures := []struct {
		name   string
		source string
	}{
		{name: "arithmetic", source: `var result = (40 + 4) / 2;`},
		{
			name: "closures-and-callbacks",
			source: `
const make = (base) => (number) => base + number;
var result = [1, 2, 3].map((item) => make(10)(item)).join(",");
`,
		},
		{
			name: "bounded-control-flow",
			source: `
var result = 0;
for (let index = 0; index < 8; index++) {
	if (index > 3) { result = result + index; }
}
`,
		},
		{
			name: "switch-fallthrough",
			source: `
var result = "";
switch (2) {
case 1:
case 2:
	result = result + "a";
case 3:
	result = result + "b";
	break;
default:
	result = "wrong";
}
`,
		},
	}

	report := coldProcessCompilerReport{CompilerFingerprint: Fingerprint()}
	for _, fixture := range fixtures {
		compiled, err := CompileSource(fixture.source)
		if err != nil {
			return coldProcessCompilerReport{}, fmt.Errorf("compile %s: %w", fixture.name, err)
		}
		if err := ValidateArtifact(compiled); err != nil {
			return coldProcessCompilerReport{}, fmt.Errorf("validate %s: %w", fixture.name, err)
		}
		artifact, err := compiled.MarshalBinary()
		if err != nil {
			return coldProcessCompilerReport{}, fmt.Errorf("encode %s: %w", fixture.name, err)
		}
		digest := sha256.Sum256(artifact)
		report.Fixtures = append(report.Fixtures, coldProcessFixtureReport{
			Name:              fixture.name,
			ProgramChecksum:   compiled.Program.Checksum(),
			ArtifactSHA256:    hex.EncodeToString(digest[:]),
			ArtifactBytes:     len(artifact),
			SourceFingerprint: compiled.SourceFingerprint(),
			CacheKey:          compiled.CacheKey(),
		})
	}
	return report, nil
}

func coldProcessEnvironment(outputPath string) []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key := entry
		if separator := strings.IndexByte(entry, '='); separator >= 0 {
			key = entry[:separator]
		}
		if strings.EqualFold(key, coldProcessHelperEnv) ||
			strings.EqualFold(key, coldProcessOutputEnv) {
			continue
		}
		result = append(result, entry)
	}
	return append(
		result,
		coldProcessHelperEnv+"=1",
		coldProcessOutputEnv+"="+outputPath,
	)
}
