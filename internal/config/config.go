// Package config loads monsieur-logger settings from environment variables.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dantotech/monsieur-logger/internal/generator"
)

// Config is the process configuration.
type Config struct {
	HTTPAddr          string
	LogLevel          string
	MaxConcurrentRuns int
	Limits            generator.Limits
	Host              generator.Host

	// AutoStart, when non-nil, is started as soon as the process is up.
	AutoStart *generator.Spec
}

// Load reads the configuration from the environment.
func Load() (Config, error) {
	c := Config{
		HTTPAddr: env("HTTP_ADDR", ":8080"),
		LogLevel: env("LOG_LEVEL", "info"),
		Host: generator.Host{
			Pod:       os.Getenv("POD_NAME"),
			Namespace: os.Getenv("POD_NAMESPACE"),
			Node:      os.Getenv("NODE_NAME"),
		},
	}
	var err error
	if c.MaxConcurrentRuns, err = envInt("MAX_CONCURRENT_RUNS", 5); err != nil {
		return c, err
	}
	c.Limits = generator.DefaultLimits
	if c.Limits.MaxLogsPerSecond, err = envInt("MAX_LOGS_PER_SECOND", c.Limits.MaxLogsPerSecond); err != nil {
		return c, err
	}
	if c.Limits.MaxLogLength, err = envInt("MAX_LOG_LENGTH", c.Limits.MaxLogLength); err != nil {
		return c, err
	}
	if c.Limits.MaxDurationSeconds, err = envInt("MAX_DURATION_SECONDS", c.Limits.MaxDurationSeconds); err != nil {
		return c, err
	}

	if b, _ := strconv.ParseBool(os.Getenv("AUTOSTART_ENABLED")); b {
		s := &generator.Spec{RunID: os.Getenv("AUTOSTART_RUN_ID")}
		if s.LogsPerSecond, err = envInt("AUTOSTART_LOGS_PER_SECOND", 0); err != nil {
			return c, err
		}
		if s.LogLength, err = envInt("AUTOSTART_LOG_LENGTH", 0); err != nil {
			return c, err
		}
		if s.DurationSeconds, err = envInt("AUTOSTART_DURATION_SECONDS", 60); err != nil {
			return c, err
		}
		if v := os.Getenv("AUTOSTART_LEVELS"); v != "" {
			w, err := ParseLevels(v)
			if err != nil {
				return c, fmt.Errorf("AUTOSTART_LEVELS: %w", err)
			}
			s.Levels = &w
		}
		if v := os.Getenv("AUTOSTART_LABELS"); v != "" {
			if err := json.Unmarshal([]byte(v), &s.Labels); err != nil {
				return c, fmt.Errorf("AUTOSTART_LABELS must be a JSON object of strings: %w", err)
			}
		}
		c.AutoStart = s
	}
	return c, nil
}

// ParseLevels parses "debug=1,info=8,warn=1,error=0".
func ParseLevels(s string) (generator.LevelWeights, error) {
	var w generator.LevelWeights
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return w, fmt.Errorf("expected level=weight, got %q", part)
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return w, fmt.Errorf("invalid weight for %q: %w", k, err)
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "debug":
			w.Debug = n
		case "info":
			w.Info = n
		case "warn", "warning":
			w.Warn = n
		case "error":
			w.Error = n
		default:
			return w, fmt.Errorf("unknown level %q", k)
		}
	}
	return w, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}
