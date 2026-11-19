package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Environment string
	Port        int
	DatabaseURL string
	JWTSecret   string
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func Load() (Config, error) {
	cfg := Config{
		Environment: getEnv("APP_ENV", "development"),
		Port:        8080,
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
	}

	portValue := os.Getenv("APP_PORT")
	if portValue != "" {
		port, err := strconv.Atoi(portValue)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("invalid APP_PORT: %q", portValue)
		}
		cfg.Port = port
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	if cfg.JWTSecret == "" {
		return Config{}, errors.New("JWT_SECRET is required")
	}

	if cfg.Environment != "development" &&
		cfg.Environment != "test" &&
		cfg.Environment != "production" {
		return Config{}, fmt.Errorf("invalid APP_ENV: %q", cfg.Environment)
	}

	return cfg, nil
}
