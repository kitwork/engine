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
	"gopkg.in/yaml.v3"
)

type Config struct {
	Port      int              `json:"port" yaml:"port"`
	Root      string           `json:"root" yaml:"root"`
	Database  *database.Config `json:"database" yaml:"database"`
	SystemDB  *database.Config `json:"systemdb" yaml:"systemdb"`
	Domains   []string         `json:"domains" yaml:"domains"`
	MaxEnergy uint64           `json:"max_energy" yaml:"max_energy"`
	HotReload bool             `json:"hot_reload" yaml:"hot_reload"`
	Hostname  string           `json:"hostname" yaml:"hostname"`
}

func Run(files ...string) (err error) {
	// Initialize Config with default values
	cfg := &Config{
		Port: 8080,
		Root: ".",
	}

	// If no files are specified, automatically search for default config files
	// if len(files) == 0 {
	// 	defaultFiles := []string{"config.kitwork.json", "config.kitwork.yaml", "config.kitwork.yml"}
	// 	for _, defFile := range defaultFiles {
	// 		if _, err := os.Stat(defFile); err == nil {
	// 			files = append(files, defFile)
	// 			break // Load only the first default config file found
	// 		}
	// 	}
	// }

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
			unmarshalErr = json.Unmarshal(expandedContent, cfg)
		} else if ext == ".yaml" || ext == ".yml" {
			unmarshalErr = yaml.Unmarshal(expandedContent, cfg)
		} else {
			return fmt.Errorf("unsupported config file extension: %s (only .json, .yaml, .yml are supported)", file)
		}

		if unmarshalErr != nil {
			return fmt.Errorf("failed to parse config file %s: %w", file, unmarshalErr)
		}

		fmt.Printf("Loaded configuration from %s\n", file)
		break
	}

	if cfg.SystemDB != nil {
		if database.System, err = cfg.SystemDB.Connect(); err != nil {
			return fmt.Errorf("failed to connect to database: %w", err)
		}
		defer database.System.Close()

	} else {
		fmt.Println("System Database is not provided")
	}

	if cfg.Database != nil {
		if database.Default, err = cfg.Database.Connect(); err != nil {
			return fmt.Errorf("failed to connect to database: %w", err)
		}
		defer database.Default.Close()

	} else {
		fmt.Println("Database is not provided")
	}

	// Assign configured domains to the ssl package
	ssl.Domains = cfg.Domains

	// Initialize and run the engine
	handler := core.New(cfg.Root, cfg.MaxEnergy, cfg.HotReload, cfg.Hostname)

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
	if cfg.Database != nil {
		fmt.Printf("\033[1;34m» Database:\033[0m          %s (%s:%d)\n", cfg.Database.Type, cfg.Database.Host, cfg.Database.Port)
	}
	fmt.Println("\033[1;30m==================================================\033[0m")

	return http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), handler)
}
