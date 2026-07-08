package database

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultPingTimeout = 5 * time.Second

type Config struct {
	DatabaseURL string
	PingTimeout time.Duration
}

func ConfigFromEnv() Config {
	return Config{DatabaseURL: os.Getenv("DATABASE_URL"), PingTimeout: defaultPingTimeout}
}

func NewPostgresPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	if cfg.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if cfg.PingTimeout <= 0 {
		cfg.PingTimeout = defaultPingTimeout
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func NewPostgresPoolFromEnv(ctx context.Context) (*pgxpool.Pool, error) {
	return NewPostgresPool(ctx, ConfigFromEnv())
}
