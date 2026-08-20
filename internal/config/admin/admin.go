package admin

type Config struct {
	AuthRequired bool
	Token        string
}

func ApplyDefaults(c *Config) {}
