package security

import (
	"os"

	"github.com/kitwork/engine/security/database"
	"github.com/kitwork/engine/security/server"
	"github.com/kitwork/engine/security/smtp"
	"gopkg.in/yaml.v3"
)

// Re-export types using aliases for backward compatibility
type DBConfig = database.Config
type ServerConfig = server.Config
type SMTPConfig = smtp.Config

type Config struct {
	Database database.Config
	Server   server.Config
	SMTP     smtp.Config
}

// LoadConfigFromDir reads modular config files from a directory
func LoadConfigFromDir(dir string) (*Config, error) {
	cfg := &Config{}

	// 1. Load Database (master.yaml)
	if data, err := os.ReadFile(dir + "/database/master.yaml"); err == nil {
		yaml.Unmarshal(data, &cfg.Database)
	}

	// 2. Load Server (http.yaml)
	if data, err := os.ReadFile(dir + "/server/http.yaml"); err == nil {
		yaml.Unmarshal(data, &cfg.Server)
	}

	// 3. Load SMTP (mail.yaml)
	if data, err := os.ReadFile(dir + "/smtp/mail.yaml"); err == nil {
		yaml.Unmarshal(data, &cfg.SMTP)
	}

	// Set defaults
	if cfg.Database.Type == "" {
		cfg.Database.Type = "postgres"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}

	return cfg, nil
}
