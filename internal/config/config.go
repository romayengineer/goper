package config

import "log/slog"

type Provider interface {
	ProxyPort() int
	GetAPIPort() int
	GetCADir() string
	IsTransparent() bool
	IsVerbose() bool
	GetBufferSize() int
	GetLogFormat() string
	GetLogLevel() slog.Level
}

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

func (c *Config) ProxyPort() int        { return c.Port }
func (c *Config) GetAPIPort() int        { return c.APIPort }
func (c *Config) GetCADir() string       { return c.CADir }
func (c *Config) IsTransparent() bool    { return c.Transparent }
func (c *Config) IsVerbose() bool        { return c.Verbose }
func (c *Config) GetBufferSize() int     { return c.BufferSize }
func (c *Config) GetLogFormat() string   { return c.LogFormat }
func (c *Config) GetLogLevel() slog.Level { return c.LogLevel }
