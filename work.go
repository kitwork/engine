package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/core"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Port   int      `json:"port" yaml:"port"`
	Source string   `json:"source" yaml:"source"`
	Master []string `json:"master" yaml:"master"`
}

func Run(files ...string) error {
	// Initialize Config with default values
	cfg := &Config{
		Port:   8080,
		Source: ".",
	}

	// If no files are specified, automatically search for default config files
	if len(files) == 0 {
		defaultFiles := []string{"config.kitwork.json", "config.kitwork.yaml", "config.kitwork.yml"}
		for _, defFile := range defaultFiles {
			if _, err := os.Stat(defFile); err == nil {
				files = append(files, defFile)
				break // Load only the first default config file found
			}
		}
	}

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

		ext := strings.ToLower(filepath.Ext(file))
		var unmarshalErr error
		if ext == ".json" {
			unmarshalErr = json.Unmarshal(bytes, cfg)
		} else if ext == ".yaml" || ext == ".yml" {
			unmarshalErr = yaml.Unmarshal(bytes, cfg)
		} else {
			return fmt.Errorf("unsupported config file extension: %s (only .json, .yaml, .yml are supported)", file)
		}

		if unmarshalErr != nil {
			return fmt.Errorf("failed to parse config file %s: %w", file, unmarshalErr)
		}

		fmt.Printf("Loaded configuration from %s\n", file)
	}

	// Fallback master db if empty
	if len(cfg.Master) == 0 {
		cfg.Master = []string{"config/database/master.yaml"}
	}

	// Initialize and run the engine
	engine := core.New(cfg.Source)

	fmt.Printf("Starting Kitwork Server on port :%d...\n", cfg.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), engine)
}
