package config

import (
	"os"
	"strings"
)

type Config struct {
	Addr          string
	DataPath      string
	Env           string
	BaseURL       string
	SecureCookies bool
	TrustProxy    bool
}

func Load() Config {
	env := getenv("HEARTH_ENV", "development")
	secure := env == "production"
	if v, ok := os.LookupEnv("HEARTH_SECURE_COOKIES"); ok {
		secure = v == "true" || v == "1"
	}
	return Config{
		Addr:          getenv("HEARTH_ADDR", ":8080"),
		DataPath:      getenv("HEARTH_DATA", filepathDefault()),
		Env:           env,
		BaseURL:       strings.TrimRight(strings.TrimSpace(os.Getenv("HEARTH_BASE_URL")), "/"),
		SecureCookies: secure,
		TrustProxy:    os.Getenv("HEARTH_TRUST_PROXY") == "true" || os.Getenv("HEARTH_TRUST_PROXY") == "1",
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func filepathDefault() string {
	return "data/hearth.db"
}
