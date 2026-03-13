package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig        `yaml:"server"`
	Database     DatabaseConfig      `yaml:"database"`
	Workers      WorkersConfig       `yaml:"workers"`
	Runtime      RuntimeConfig       `yaml:"runtime"`
	Feishu       FeishuConfig        `yaml:"feishu"`
	DingTalk     DingTalkConfig      `yaml:"dingtalk"`
	MessageQueue MessageQueueConfig  `yaml:"message_queue"`
	MCP          MCPConfig           `yaml:"mcp"`
	Bee          BeeConfig           `yaml:"bee"`
}

type BeeConfig struct {
	Name    string      `yaml:"name"`
	WorkDir string      `yaml:"work_dir"`
	Persona string      `yaml:"persona"`
	Feeder  FeederConfig `yaml:"feeder"`
}

type FeederConfig struct {
	Interval           time.Duration `yaml:"interval"`
	BatchSize          int           `yaml:"batch_size"`
	Timeout            time.Duration `yaml:"timeout"`
	QueueWarnThreshold int           `yaml:"queue_warn_threshold"`
}

type FeishuConfig struct {
	Enabled   bool   `yaml:"enabled"`
	AppID     string `yaml:"app_id"`
	AppSecret string `yaml:"app_secret"`
}

type DingTalkConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

type MCPConfig struct {
	APIKey string `yaml:"api_key"`
}


type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type WorkersConfig struct {
	BaseDir string `yaml:"base_dir"`
}

type RuntimeConfig struct {
	ClaudeCode RuntimeEntry `yaml:"claude_code"`
}

type RuntimeEntry struct {
	Binary  string        `yaml:"binary"`
	Timeout time.Duration `yaml:"timeout"`
}

type MessageQueueConfig struct {
	DebounceWindow time.Duration `yaml:"debounce_window"`
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
	if err := applyDefaults(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyDefaults(cfg *Config) error {
	if cfg.Workers.BaseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		cfg.Workers.BaseDir = filepath.Join(home, ".robobee", "worker")
	}
	if cfg.MessageQueue.DebounceWindow == 0 {
		cfg.MessageQueue.DebounceWindow = 3 * time.Second
	}
	if cfg.Bee.Name == "" {
		cfg.Bee.Name = "bee"
	}
	if cfg.Bee.WorkDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		cfg.Bee.WorkDir = filepath.Join(home, ".robobee", "bee")
	}
	if cfg.Bee.Feeder.Interval == 0 {
		cfg.Bee.Feeder.Interval = 10 * time.Second
	}
	if cfg.Bee.Feeder.BatchSize == 0 {
		cfg.Bee.Feeder.BatchSize = 10
	}
	if cfg.Bee.Feeder.Timeout == 0 {
		cfg.Bee.Feeder.Timeout = 5 * time.Minute
	}
	if cfg.Bee.Feeder.QueueWarnThreshold == 0 {
		cfg.Bee.Feeder.QueueWarnThreshold = 100
	}
	return nil
}
