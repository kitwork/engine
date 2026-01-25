package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/security"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Port      int      `yaml:"port"`
	Debug     bool     `yaml:"debug"`
	Sources   []string `yaml:"source"`
	Databases []string `yaml:"databases"`
	SMTPS     []string `yaml:"smtp"`
}

// Run starts the Kitwork Engine using the provided Config.
// It will automatically discover modular configs and database settings within cfg.Sources.
func Run(cfg *Config) {
	if cfg == nil {
		cfg = &Config{}
	}

	e := core.New()

	// 1. Map Base Config
	e.Config.Port = cfg.Port
	if e.Config.Port == 0 {
		e.Config.Port = 8080 // Default port
	}
	e.Config.Debug = cfg.Debug
	e.Config.Sources = cfg.Sources
	if len(e.Config.Sources) == 0 {
		e.Config.Sources = []string{"./"}
	}

	// 2. Automated Discovery & Initialization
	// Load explicitly defined databases from files
	for _, dbPath := range cfg.Databases {
		if data, err := os.ReadFile(dbPath); err == nil {
			var dbCfg security.DBConfig
			if err := yaml.Unmarshal(data, &dbCfg); err == nil {
				if err := work.InitDB(dbCfg); err == nil {
					fmt.Printf("✅ Database Connected from file: %s\n", dbPath)
				} else {
					fmt.Printf("❌ Database Connection Failed (%s): %v\n", dbPath, err)
				}
			}
		} else {
			fmt.Printf("⚠️  Database config file not found: %s\n", dbPath)
		}
	}

	// Load explicitly defined SMTP from files
	for _, smtpPath := range cfg.SMTPS {
		if data, err := os.ReadFile(smtpPath); err == nil {
			var smtpCfg security.SMTPConfig
			if err := yaml.Unmarshal(data, &smtpCfg); err == nil {
				fmt.Printf("📧 SMTP Config Loaded from file: %s (Host: %s)\n", smtpPath, smtpCfg.Host)
			}
		} else {
			fmt.Printf("⚠️  SMTP config file not found: %s\n", smtpPath)
		}
	}

	// 3. Automated Discovery & Initialization
	// This will scan each source for work.yaml, logic, and DATABASE configs.
	for _, dir := range e.Config.Sources {
		if e.Config.Debug {
			fmt.Printf("📦 Loading source: %s\n", dir)
		}
		loadConfigs(e, dir)
		loadLogic(e, dir)
	}

	// 3. Finalize & Fire
	e.SyncRegistry()
	bootServer(e, e.Config.Port)
}

// LoadConfig loads the root configuration file (config.yaml).
func LoadConfig(dir string) (*Config, error) {
	cfg := &Config{}

	// Root config
	path := filepath.Join(dir, "config.yaml")
	if data, err := os.ReadFile(path); err == nil {
		yaml.Unmarshal(data, cfg)
	} else if os.IsNotExist(err) {
		fmt.Printf("ℹ️  config.yaml not found at %s. Using default settings.\n", path)
	}

	// Set defaults
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if len(cfg.Sources) == 0 {
		cfg.Sources = []string{"./"}
	}

	return cfg, nil
}

func loadConfigs(e *core.Engine, dir string) {
	patterns := []string{"work.json", "work.yaml", "work.yml"}
	for _, p := range patterns {
		files, _ := filepath.Glob(filepath.Join(dir, p))
		for _, f := range files {
			content, _ := os.ReadFile(f)
			data := make(map[string]any)
			var err error
			if strings.HasSuffix(f, ".json") {
				err = json.Unmarshal(content, &data)
			} else {
				err = yaml.Unmarshal(content, &data)
			}
			if err != nil {
				fmt.Printf("❌ Config error [%s]: %v\n", f, err)
				continue
			}

			w := work.NewWork("generic")
			w.LoadFromConfig(data)
			e.RegisterWork(w)

			// Update global config if present in file
			if p, ok := data["port"].(int); ok {
				e.Config.Port = p
			}
			if p, ok := data["port"].(float64); ok {
				e.Config.Port = int(p)
			}
			if d, ok := data["debug"].(bool); ok {
				e.Config.Debug = d
			}
			if str, ok := data["source"].(string); ok {
				e.Config.Sources = append(e.Config.Sources, str)
			}
			if ss, ok := data["source"].([]any); ok {
				for _, s := range ss {
					if str, ok := s.(string); ok {
						e.Config.Sources = append(e.Config.Sources, str)
					}
				}
			}

			if e.Config.Debug {
				fmt.Printf("📦 Config loaded: %s [%s]\n", w.Name, f)
			}

			// SUPPORT MODULAR DB INITIALIZATION
			if dbRaw, ok := data["database"]; ok {
				var dbCfg security.DBConfig
				// Convert map to struct via JSON (easiest way in Go for generic maps)
				jsonData, _ := json.Marshal(dbRaw)
				json.Unmarshal(jsonData, &dbCfg)

				if dbCfg.Type != "" {
					if err := work.InitDB(dbCfg); err == nil {
						fmt.Printf("✅ Database Connected (from %s)\n", f)
					}
				}
			}
		}
	}
}

func loadLogic(e *core.Engine, dir string) {
	// Recursive walk to find all .js files
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		} // Skip read errors
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".js") {
			content, _ := os.ReadFile(path)
			w, err := e.Build(string(content))
			if err == nil {
				if e.Config.Debug {
					fmt.Printf("📜 Logic loaded: %s\n", path)
				}

				// GLOBAL BYTECODE PROPAGATION
				if w.Bytecode != nil {
					for _, other := range e.Registry {
						if other.Bytecode == nil {
							other.Bytecode = w.Bytecode
						}
					}
				}

				fmt.Printf("[loadLogic] Calling Trigger for Work: %s (bytecode: %v)\n", w.Name, w.Bytecode != nil)
				e.Trigger(context.TODO(), w, nil, nil)
			} else {
				fmt.Printf("❌ Code Error in %s: %v\n", path, err)
			}
		}
		return nil
	})
	if err != nil && e.Config.Debug {
		fmt.Printf("⚠️  Warning: Error walking directory %s: %v\n", dir, err)
	}
}

func bootServer(e *core.Engine, serverPort int) {
	port := strconv.Itoa(serverPort)
	fmt.Printf("🚀 Kitwork Engine online at http://localhost:%s\n", port)

	work.GlobalRouter.Mu.RLock()
	fmt.Printf("🔍 Routes registered: %d\n", len(work.GlobalRouter.Routes))
	for _, r := range work.GlobalRouter.Routes {
		fmt.Printf(" - %s %s (Fn Address: %d)\n", r.Method, r.Path, r.Fn.Address)
	}
	work.GlobalRouter.Mu.RUnlock()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		method := r.Method

		fmt.Printf("[HTTP] Incoming %s %s\n", method, path)

		var matchedRoute *work.Route
		pathParams := make(map[string]value.Value)

		work.GlobalRouter.Mu.RLock()
		for i := range work.GlobalRouter.Routes {
			rt := &work.GlobalRouter.Routes[i]
			if rt.Method == method {
				if params, ok := matchRoute(path, rt.Path); ok {
					matchedRoute = rt
					for k, v := range params {
						pathParams[k] = value.New(v)
					}
					fmt.Printf("[HTTP] Matched route: %s %s\n", rt.Method, rt.Path)
					break
				}
			}
		}
		work.GlobalRouter.Mu.RUnlock()

		if matchedRoute == nil {
			fmt.Printf("[HTTP] No match found for %s %s\n", method, path)
			http.NotFound(w, r)
			return
		}

		// 1. URL Query Params
		queryParams := make(map[string]value.Value)
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				queryParams[k] = value.New(v[0])
			}
		}

		// 2. JSON Body Params
		bodyParams := make(map[string]value.Value)
		if r.Method != "GET" && r.Body != nil {
			var bodyData map[string]any
			if err := json.NewDecoder(r.Body).Decode(&bodyData); err == nil {
				for k, v := range bodyData {
					bodyParams[k] = value.New(v)
				}
			}
		}

		res := e.ExecuteLambda(matchedRoute.Work, matchedRoute.Fn, r, w, queryParams, bodyParams, pathParams)
		if res.Error != "" {
			http.Error(w, res.Error, 500)
			return
		}

		responseVal := res.Response
		if responseVal.K == value.Nil {
			responseVal = res.Value
		}

		if res.ResType == "html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			htmlContent := ""
			if responseVal.K == value.Map {
				m := responseVal.Interface().(map[string]any)
				template, _ := m["template"].(string)
				data, _ := m["data"].(map[string]any)
				htmlContent = renderTemplate(template, data)
			} else {
				htmlContent = responseVal.Text()
			}
			w.Write([]byte(htmlContent))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		outputData, _ := json.Marshal(responseVal.Interface())
		fmt.Printf("[HTTP] Response: %s\n", string(outputData))
		w.Write(outputData)
	})

	p, _ := strconv.Atoi(port)
	for {
		addr := fmt.Sprintf(":%s", strconv.Itoa(p))
		l, err := net.Listen("tcp", addr)
		if err == nil {
			l.Close()
			fmt.Printf("🚀 Kitwork Engine online at http://localhost:%d\n", p)
			err = http.ListenAndServe(addr, nil)
			if err != nil {
				fmt.Printf("❌ Server Failed: %v\n", err)
			}
			break
		}
		p++
		if p > 9000 {
			break
		}
	}
}

func renderTemplate(tmpl string, data map[string]any) string {
	res := tmpl
	for k, v := range data {
		placeholder := "{{" + k + "}}"
		res = strings.ReplaceAll(res, placeholder, fmt.Sprintf("%v", v))
	}
	return res
}

func matchRoute(path, routePath string) (map[string]string, bool) {
	if path == routePath {
		return nil, true
	}

	pSegments := strings.Split(strings.Trim(path, "/"), "/")
	rSegments := strings.Split(strings.Trim(routePath, "/"), "/")

	if len(pSegments) != len(rSegments) {
		return nil, false
	}

	params := make(map[string]string)
	for i := 0; i < len(rSegments); i++ {
		if strings.HasPrefix(rSegments[i], ":") {
			params[rSegments[i][1:]] = pSegments[i]
		} else if rSegments[i] != pSegments[i] {
			return nil, false
		}
	}
	return params, true
}
