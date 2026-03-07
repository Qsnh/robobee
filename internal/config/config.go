package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	SMTP     SMTPConfig     `yaml:"smtp"`
	Database DatabaseConfig `yaml:"database"`
	Workers  WorkersConfig  `yaml:"workers"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type SMTPConfig struct {
	Port     int    `yaml:"port"`
	Domain   string `yaml:"domain"`
	SystemCC string `yaml:"system_cc"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type WorkersConfig struct {
	BaseDir        string `yaml:"base_dir"`
	DefaultRuntime string `yaml:"default_runtime"`
}

type RuntimeConfig struct {
	ClaudeCode RuntimeEntry `yaml:"claude_code"`
	Codex      RuntimeEntry `yaml:"codex"`
}

type RuntimeEntry struct {
	Binary  string        `yaml:"binary"`
	Timeout time.Duration `yaml:"timeout"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
