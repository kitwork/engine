package security

import (
	"os"

	"gopkg.in/yaml.v3"
)

// DBConfig chứa thông tin kết nối Database
type DBConfig struct {
	Type     string `yaml:"type"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	SSLMode  string `yaml:"ssl"`
	Timezone string `yaml:"timezone"`
	Timeout  int    `yaml:"timeout"`
	MaxOpen  int    `yaml:"max_open"`
	MaxIdle  int    `yaml:"max_idle"`
	Lifetime int    `yaml:"lifetime"`
	MaxLimit int    `yaml:"max_limit"`
}

type ServerConfig struct {
	Port  int  `yaml:"port"`
	Debug bool `yaml:"debug"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type Config struct {
	Database DBConfig
	Server   ServerConfig
	SMTP     SMTPConfig
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
