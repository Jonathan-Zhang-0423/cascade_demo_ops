package config

import (
	"log/slog"
	"os"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	LogLevel    slog.Level
}

func Load() Config {
	return Config{
		HTTPAddr:    env("HTTP_ADDR", ":4000"),
		DatabaseURL: env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/cascade_demoops"),
		LogLevel:    slog.LevelInfo,
	}
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}