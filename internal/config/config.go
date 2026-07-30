package config

type Config struct {
	Port        int
	APIPort     int
	CADir       string
	Transparent bool
	Verbose     bool
	BufferSize  int
}

func Default() *Config {
	return &Config{
		Port:        8080,
		APIPort:     8081,
		CADir:       "~/.goper/ca",
		Transparent: false,
		Verbose:     false,
		BufferSize:  10000,
	}
}
