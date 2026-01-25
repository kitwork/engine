package database

// Config chứa thông tin kết nối Database
type Config struct {
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
