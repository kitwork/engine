package server

type Config struct {
	Port  int  `yaml:"port"`
	Debug bool `yaml:"debug"`
}
