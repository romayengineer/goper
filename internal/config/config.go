package config

import "log/slog"

type Config struct {
	Port        int
	APIPort     int
	CADir       string
	Transparent bool
	Verbose     bool
	BufferSize  int
	LogFormat   string
	LogLevel    slog.Level
}

func Default() *Config {
	return &Config{
		Port:        8080,
		APIPort:     8081,
		CADir:       "~/.goper/ca",
		Transparent: false,
		Verbose:     false,
		BufferSize:  10000,
		LogFormat:   "text",
		LogLevel:    slog.LevelInfo,
	}
}
