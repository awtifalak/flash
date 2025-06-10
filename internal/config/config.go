package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type RedisConfig struct {
	Addr     string
	Password string
}

type Config struct {
	Port               string
	DatabaseURL        string
	Redis              RedisConfig
	ReservationTimeout time.Duration
}

// Load loads configuration from environment variables.
func Load() (*Config, error) {
	timeout, err := strconv.Atoi(getEnv("RESERVATION_TIMEOUT", "600"))
	if err != nil {
		return nil, fmt.Errorf("invalid RESERVATION_TIMEOUT: %w", err)
	}

	cfg := &Config{
		Port: getEnv("PORT", "8080"),
		DatabaseURL: fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			getEnv("PG_USER", "postgres"),
			getEnv("PG_PASSWORD", "postgres"),
			getEnv("PG_HOST", "postgres"),
			getEnv("PG_PORT", "5432"),
			getEnv("PG_DB", "sales"),
		),
		Redis: RedisConfig{
			Addr:     fmt.Sprintf("%s:%s", getEnv("REDIS_HOST", "redis"), getEnv("REDIS_PORT", "6379")),
			Password: getEnv("REDIS_PASSWORD", ""),
		},
		ReservationTimeout: time.Duration(timeout) * time.Second,
	}
	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
