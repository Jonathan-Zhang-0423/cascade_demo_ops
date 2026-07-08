package repository

import (
	"errors"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/repository/postgres"
	"cascade-demoops/backend/internal/repository/sqlite"
)

type Factory interface {
	Dialect() string
}

func NewFactory(runtime config.AppRuntimeConfig) (Factory, error) {
	switch runtime.DatabaseDialect {
	case config.DatabaseSQLite:
		if runtime.SQLitePath == "" {
			return nil, errors.New("SQLitePath is required for sqlite repository factory")
		}
		return sqlite.NewFactory(runtime.SQLitePath), nil
	case config.DatabasePostgres:
		if runtime.DatabaseURL == "" {
			return nil, errors.New("DatabaseURL is required for postgres repository factory")
		}
		return postgres.NewFactory(runtime.DatabaseURL), nil
	default:
		return nil, errors.New("unsupported database dialect")
	}
}
