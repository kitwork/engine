package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/ssl"
	"github.com/kitwork/engine/work"
	"gopkg.in/yaml.v3"
)

func Run(files ...string) (err error) {
	raw := make(map[string]interface{})

	// Load and override configurations from the file list
	for _, file := range files {
		bytes, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				// Skip if file does not exist to support fallback chain
				continue
			}
			return fmt.Errorf("unable to read config file %s: %w", file, err)
		}

		// Expand environment variables
		expandedContent := []byte(os.ExpandEnv(string(bytes)))

		ext := strings.ToLower(filepath.Ext(file))
		var unmarshalErr error
		if ext == ".json" {
			unmarshalErr = json.Unmarshal(expandedContent, &raw)
		} else if ext == ".yaml" || ext == ".yml" {
			unmarshalErr = yaml.Unmarshal(expandedContent, &raw)
		} else {
			return fmt.Errorf("unsupported config file extension: %s (only .json, .yaml, .yml are supported)", file)
		}

		if unmarshalErr != nil {
			return fmt.Errorf("failed to parse config file %s: %w", file, unmarshalErr)
		}

		fmt.Printf("Loaded configuration from %s\n", file)
		break
	}

	cfg, err := ParseConfig(raw)
	if err != nil {
		return fmt.Errorf("failed to process configuration: %w", err)
	}

	var systemConnected bool
	for i := range cfg.Databases {
		dbCfg := cfg.Databases[i]
		alias := dbCfg.Alias
		if alias == "" {
			alias = "default"
		}
		database.Configs[alias] = dbCfg

		if dbCfg.Alias == "system" {
			dbConn, err := dbCfg.Connect()
			if err != nil {
				return fmt.Errorf("failed to connect to system database: %w", err)
			}
			defer dbConn.Close()

			database.System = dbConn
			systemConnected = true
		}
	}

	if !systemConnected {
		fmt.Println("System Database is not provided")
	}

	// Pass global settings to the work package
	work.AllowLocal = cfg.AllowLocal
	work.ServerPort = cfg.Port
	work.RateLimitEnabled = cfg.RateLimit.Enabled
	if cfg.RateLimit.Period > 0 {
		work.RateLimitPeriod = cfg.RateLimit.Period
	}
	if cfg.RateLimit.Rate > 0 {
		work.DefaultTenantRate = cfg.RateLimit.Rate
	}
	if cfg.RateLimit.IpRate > 0 {
		work.DefaultTenantIpRate = cfg.RateLimit.IpRate
	}
	if cfg.RateLimit.BrowserRate > 0 {
		work.DefaultTenantBrowserRate = cfg.RateLimit.BrowserRate
	}
	if cfg.RateLimit.UserRate > 0 {
		work.DefaultTenantUserRate = cfg.RateLimit.UserRate
	}

	// Assign configured domains to the ssl package
	ssl.Domains = cfg.Domains

	// Initialize and run the engine
	handler := core.New(cfg.Root, cfg.MaxEnergy, cfg.HotReload, cfg.Hostname)
	handler.RateLimit.Enabled = cfg.RateLimit.Enabled
	handler.RateLimit.Rate = cfg.RateLimit.Rate
	handler.RateLimit.IpRate = cfg.RateLimit.IpRate
	handler.RateLimit.BrowserRate = cfg.RateLimit.BrowserRate
	if cfg.RateLimit.Period > 0 {
		handler.RateLimit.Period = cfg.RateLimit.Period
	}

	if len(cfg.Domains) > 0 {
		tlsConfig := ssl.AutoSSL()

		go func() {
			server := &http.Server{
				Addr:      ":443",
				Handler:   handler,
				TLSConfig: tlsConfig,
			}
			fmt.Printf("Starting secure HTTPS Kitwork Server on port :443 for domains: %s...\n", strings.Join(cfg.Domains, ", "))
			if err := server.ListenAndServeTLS("", ""); err != nil {
				fmt.Printf("[HTTPS] Error: %v\n", err)
			}
		}()
	}

	// Print premium startup welcome banner
	modeStr := "Multi-Tenant"
	switch cfg.Root {
	case "", "./", "../", "/", ".", "..":
		modeStr = "Standalone (Root: .)"
	default:
		modeStr = fmt.Sprintf("Multi-Tenant (Root: %s)", cfg.Root)
	}

	fmt.Println("\033[36m" + `
   __  ___ __                      __   
  / / / (_) /___ _      ______  __/ /__ 
 / /_/ / / __/ \ \ /\ / / __ \/ __  '_/ 
/ __  / / /_    \ V  V / /_/ / /  <    
/_/ /_/_/\__/     \_/\_/\____/_/  |_|   ` + "\033[0m")
	fmt.Println("\033[1;30m==================================================\033[0m")
	fmt.Printf("\033[1;35m» Engine Mode:\033[0m       %s\n", modeStr)
	fmt.Printf("\033[1;32m» Local Access:\033[0m      http://localhost:%d\033[0m\n", cfg.Port)
	for _, db := range cfg.Databases {
		aliasStr := db.Alias
		if aliasStr == "" {
			aliasStr = "default"
		}
		if db.Type == "sqlite" || db.Type == "sqlite3" {
			name := db.Name
			if name == "" {
				name = db.Host
			}
			fmt.Printf("\033[1;34m» Database (%s):\033[0m    SQLite (%s)\n", aliasStr, name)
		} else {
			fmt.Printf("\033[1;34m» Database (%s):\033[0m    %s (%s:%d)\n", aliasStr, db.Type, db.Host, db.Port)
		}
	}
	fmt.Println("\033[1;30m==================================================\033[0m")

	return http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), handler)
}
