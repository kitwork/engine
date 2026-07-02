package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
type ServerBuilder struct {
	config map[string]value.Value
	ran    bool
	path   string
	err    string
}

func NewServerBuilder() *ServerBuilder {
	return &ServerBuilder{
		config: make(map[string]value.Value),
	}
}

func coerceToNumeric(v value.Value) value.Value {
	if v.K == value.String {
		if f, err := strconv.ParseFloat(v.Text(), 64); err == nil {
			return value.New(f)
		}
	}
	return v
}

func (b *ServerBuilder) Port(v value.Value) *ServerBuilder {
	b.config["port"] = coerceToNumeric(v)
	return b
}

func (b *ServerBuilder) Root(v value.Value) *ServerBuilder {
	b.config["root"] = v
	return b
}

func (b *ServerBuilder) Hostname(v value.Value) *ServerBuilder {
	b.config["hostname"] = v
	return b
}

func (b *ServerBuilder) HotReload(v value.Value) *ServerBuilder {
	b.config["hot_reload"] = v
	return b
}

func (b *ServerBuilder) AllowLocal(v value.Value) *ServerBuilder {
	b.config["allow_local"] = v
	return b
}

func (b *ServerBuilder) Redirects(v value.Value) *ServerBuilder {
	b.config["redirects"] = v
	return b
}

func (b *ServerBuilder) Canonical(v value.Value) *ServerBuilder {
	b.config["canonical"] = v
	return b
}

func (b *ServerBuilder) RateLimit(v value.Value) *ServerBuilder {
	b.config["rate_limit"] = v
	return b
}

func (b *ServerBuilder) Databases(v value.Value) *ServerBuilder {
	b.config["databases"] = v
	return b
}

func (b *ServerBuilder) Database(v value.Value) *ServerBuilder {
	var current []value.Value
	if d, ok := b.config["databases"]; ok && d.K == value.Array {
		current = *d.V.(*[]value.Value)
	}
	current = append(current, v)
	b.config["databases"] = value.Value{K: value.Array, V: &current}
	return b
}

func (b *ServerBuilder) Logger(v value.Value) *ServerBuilder {
	b.config["logger"] = v
	return b
}

func (b *ServerBuilder) Run(args ...value.Value) value.Value {
	b.ran = true
	if len(args) > 0 {
		arg := args[0]
		if arg.K == value.String {
			text := arg.Text()
			lower := strings.ToLower(text)
			if strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".js") {
				b.path = text
			} else if f, err := strconv.ParseFloat(text, 64); err == nil {
				// Shorthand string port: server.run("8080")
				b.config["port"] = value.New(f)
			} else {
				// Fallback to treat other string as path
				b.path = text
			}
		} else if arg.IsNumeric() {
			// Shorthand numeric port: server.run(8080)
			b.config["port"] = arg
		} else {
			// Legacy object config: server.run({ port: 8080 })
			b.config = make(map[string]value.Value)
			if arg.IsMap() {
				for k, val := range arg.Map() {
					b.config[k] = val
				}
			}
		}
	}

	// Validate config fields early
	if portVal, ok := b.config["port"]; ok {
		portNum := coerceToNumeric(portVal)
		if portNum.K == value.Number {
			if portNum.N < 1 || portNum.N > 65535 {
				b.err = fmt.Sprintf("invalid port number: %v (must be 1-65535)", portNum.N)
				return value.New(b.err)
			}
			b.config["port"] = portNum
		} else {
			b.err = fmt.Sprintf("invalid port value type: %s", portVal.K.String())
			return value.New(b.err)
		}
	}
	if rootVal, ok := b.config["root"]; ok {
		if rootVal.K == value.String && rootVal.Text() == "" {
			b.err = "invalid root directory: cannot be empty"
			return value.New(b.err)
		}
	}

	return value.Value{K: value.Nil}
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

	builder := NewServerBuilder()
	serverObj := value.New(builder)

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
	if !builder.ran {
		return nil, fmt.Errorf("config file did not call server.run({...})")
	}
	if builder.err != "" {
		return nil, fmt.Errorf("config validation error: %s", builder.err)
	}
	if builder.path != "" {
		// server.run("path"): nạp file config được trỏ tới (tương đối so với file này).
		p := builder.path
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(file), p)
		}
		return loadReferencedConfig(p)
	}

	// value.Value → map[string]interface{} qua MarshalJSON có sẵn của value.
	configVal := value.New(builder.config)
	jsonBytes, err := json.Marshal(configVal)
	if err != nil {
		return nil, fmt.Errorf("config serialize error: %w", err)
	}
	raw := make(map[string]interface{})
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("config decode error: %w", err)
	}
	return raw, nil
}
