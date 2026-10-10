// Package config loads server configuration from YAML/JSON files and
// INSPECTOR_* environment overrides.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LocalTarget is a directly reachable Go process exposing net/http/pprof.
type LocalTarget struct {
	Name     string `json:"name" yaml:"name"`
	PprofURL string `json:"pprof_url" yaml:"pprof_url"`
}

// DockerConfig controls Docker socket access and container allowlist.
type DockerConfig struct {
	Socket           string   `json:"socket" yaml:"socket"`
	Allowlist        []string `json:"allowlist" yaml:"allowlist"`
	DefaultPprofPort int      `json:"default_pprof_port" yaml:"default_pprof_port"`
}

// LimitsConfig bounds profile collection.
type LimitsConfig struct {
	MaxProfileMB    int `json:"max_profile_mb" yaml:"max_profile_mb"`
	DefaultTimeoutS int `json:"default_timeout_s" yaml:"default_timeout_s"`
	MaxCPUSeconds   int `json:"max_cpu_seconds" yaml:"max_cpu_seconds"`
	MaxTraceSeconds int `json:"max_trace_seconds" yaml:"max_trace_seconds"`
}

// ServerConfig selects transports.
type ServerConfig struct {
	HTTPAddr string `json:"http_addr" yaml:"http_addr"`
	Stdio    bool   `json:"stdio" yaml:"stdio"`
}

// Config is the full server configuration.
type Config struct {
	Local  []LocalTarget `json:"local" yaml:"local"`
	Docker DockerConfig  `json:"docker" yaml:"docker"`
	Limits LimitsConfig  `json:"limits" yaml:"limits"`
	Server ServerConfig  `json:"server" yaml:"server"`
}

// Defaults returns a Config with safe defaults.
func Defaults() Config {
	return Config{
		Docker: DockerConfig{
			Socket:           "unix:///var/run/docker.sock",
			DefaultPprofPort: 6060,
		},
		Limits: LimitsConfig{
			MaxProfileMB:    64,
			DefaultTimeoutS: 60,
			MaxCPUSeconds:   120,
			MaxTraceSeconds: 30,
		},
		Server: ServerConfig{
			Stdio: true,
		},
	}
}

// Load reads path (YAML or JSON) when non-empty, then applies environment
// overrides. A missing file is not an error: defaults are used.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return Config{}, fmt.Errorf("read config %q: %w", path, err)
			}
		} else {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".json" {
				if err := json.Unmarshal(data, &cfg); err != nil {
					return Config{}, fmt.Errorf("parse config %q: %w", path, err)
				}
			} else {
				if err := yaml.Unmarshal(data, &cfg); err != nil {
					return Config{}, fmt.Errorf("parse config %q: %w", path, err)
				}
			}
		}
	}
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("INSPECTOR_HTTP_ADDR"); v != "" {
		cfg.Server.HTTPAddr = v
	}
	if v := os.Getenv("INSPECTOR_STDIO"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Server.Stdio = b
		}
	}
	if v := os.Getenv("DOCKER_HOST"); v != "" {
		cfg.Docker.Socket = v
	}
	if v := os.Getenv("INSPECTOR_DOCKER_SOCKET"); v != "" {
		cfg.Docker.Socket = v
	}
	if v := os.Getenv("INSPECTOR_DOCKER_ALLOWLIST"); v != "" {
		cfg.Docker.Allowlist = strings.Split(v, ",")
		for i := range cfg.Docker.Allowlist {
			cfg.Docker.Allowlist[i] = strings.TrimSpace(cfg.Docker.Allowlist[i])
		}
	}
	if v := os.Getenv("INSPECTOR_MAX_PROFILE_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Limits.MaxProfileMB = n
		}
	}
	if v := os.Getenv("INSPECTOR_TIMEOUT_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Limits.DefaultTimeoutS = n
		}
	}
	if v := os.Getenv("INSPECTOR_MAX_CPU_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Limits.MaxCPUSeconds = n
		}
	}
}

// Validate checks invariants.
func (c Config) Validate() error {
	if c.Limits.MaxProfileMB <= 0 {
		return fmt.Errorf("limits.max_profile_mb must be > 0")
	}
	if c.Limits.DefaultTimeoutS <= 0 {
		return fmt.Errorf("limits.default_timeout_s must be > 0")
	}
	if c.Limits.MaxCPUSeconds <= 0 {
		return fmt.Errorf("limits.max_cpu_seconds must be > 0")
	}
	if c.Limits.MaxTraceSeconds <= 0 {
		c.Limits.MaxTraceSeconds = 30
	}
	for _, t := range c.Local {
		if t.Name == "" || t.PprofURL == "" {
			return fmt.Errorf("local target requires name and pprof_url")
		}
	}
	if c.Server.HTTPAddr != "" {
		if _, _, err := net.SplitHostPort(c.Server.HTTPAddr); err != nil {
			return fmt.Errorf("server.http_addr %q: %w", c.Server.HTTPAddr, err)
		}
	}
	return nil
}

// MaxProfileBytes returns the byte limit for one profile blob.
func (c Config) MaxProfileBytes() int64 {
	return int64(c.Limits.MaxProfileMB) << 20
}
