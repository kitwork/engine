package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
	"gopkg.in/yaml.v3"
)

// loadReferencedConfig nạp file config mà server.run("path") trỏ tới: .js (đệ quy
// qua evalConfigJS), .json hoặc .yaml/.yml → map[string]interface{} cho ParseConfig.
func loadReferencedConfig(path string) (map[string]interface{}, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".js" {
		return evalConfigJS(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("server.run: %w", err)
	}
	expanded := []byte(os.ExpandEnv(string(data)))
	raw := map[string]interface{}{}
	switch ext {
	case ".json":
		err = json.Unmarshal(expanded, &raw)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(expanded, &raw)
	default:
		return nil, fmt.Errorf("server.run: unsupported config file %q (cần .js/.json/.yaml)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("server.run: parse %s: %w", path, err)
	}
	return raw, nil
}

// evalConfigJS chạy một file bootstrap (vd server.kitwork.js) trong một VM SETUP
// tối giản — chỉ có builtin `server` + `env`, KHÔNG có router/database/runtime của
// tenant — rồi trả về object config được truyền vào server.run({...}) dưới dạng
// map[string]interface{}, sẵn sàng cho ParseConfig (giống hệt nguồn .json/.yaml).
//
// `env` ở đây là env CẤP HOST (root .env + biến môi trường thật). Mỗi TENANT có
// `env` riêng, cô lập theo path, dựng trong work.Tenant.Run() (kitwork().env).
//
// An toàn để thực thi vì subset Kitwork cấm `while` + có gas → không thể treo.
func evalConfigJS(file string) (map[string]interface{}, error) {
	bytecode, err := compiler.CompileFile(file)
	if err != nil {
		return nil, fmt.Errorf("config compile error: %w", err)
	}

	vm := runtime.New(bytecode.Instructions, bytecode.Constants)
	vm.SourceMap = bytecode.SourceMap
	vm.MaxEnergy = 100_000_000 // rộng tay: setup chạy đúng 1 lần, không phải hot path

	var captured value.Value
	var capturedPath string
	var captureSet, ran bool

	// server.run(arg): arg là OBJECT config, HOẶC một chuỗi đường dẫn tới file
	// config (.json/.yaml/.js) — khi đó nạp file đó thay cho object.
	runFunc := value.NewFunc(func(args ...value.Value) value.Value {
		ran = true
		if len(args) > 0 {
			if args[0].K == value.String {
				capturedPath = args[0].Text()
			} else {
				captured = args[0]
				captureSet = true
			}
		}
		return value.Value{K: value.Nil}
	})
	serverObj := value.New(map[string]value.Value{"run": runFunc})

	// HOST env: root .env (cạnh file config) phủ bởi biến môi trường THẬT (OS thắng).
	// KHÁC với env riêng từng tenant — cái đó cô lập theo path trong work.Tenant.
	envMap := work.ParseDotEnv(filepath.Join(filepath.Dir(file), ".env"))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			envMap[kv[:i]] = kv[i+1:]
		}
	}
	var missingEnv []string
	envObj := work.NewEnvWithMissing(envMap, &missingEnv)

	// kitwork() trong context setup trả về {server, env} (KHÔNG phải tenant runtime).
	setupRoot := value.New(map[string]value.Value{
		"server": serverObj,
		"env":    envObj,
	})
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value { return setupRoot })
	vm.Builtins = []value.Value{kitworkFunc}
	vm.Globals["kitwork"] = kitworkFunc

	res := vm.Run()
	if res.K == value.Invalid {
		return nil, fmt.Errorf("config runtime error: %v", res.V)
	}
	if len(missingEnv) > 0 {
		return nil, fmt.Errorf("missing required env var(s): %s", strings.Join(missingEnv, ", "))
	}
	if !ran {
		return nil, fmt.Errorf("config file did not call server.run({...})")
	}
	if capturedPath != "" {
		// server.run("path"): nạp file config được trỏ tới (tương đối so với file này).
		p := capturedPath
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(file), p)
		}
		return loadReferencedConfig(p)
	}
	if !captureSet {
		return map[string]interface{}{}, nil // server.run() rỗng → dùng mặc định
	}

	// value.Value → map[string]interface{} qua MarshalJSON có sẵn của value.
	jsonBytes, err := json.Marshal(captured)
	if err != nil {
		return nil, fmt.Errorf("config serialize error: %w", err)
	}
	raw := make(map[string]interface{})
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("config decode error: %w", err)
	}
	return raw, nil
}
