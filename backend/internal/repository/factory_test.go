package repository

import (
	"testing"

	"cascade-demoops/backend/internal/config"
)

func TestRepositoryFactorySelectsSQLiteForDesktop(t *testing.T) {
	factory, err := NewFactory(config.AppRuntimeConfig{
		DatabaseDialect: config.DatabaseSQLite,
		SQLitePath:      "cascade_demoops.db",
	})
	if err != nil {
		t.Fatal(err)
	}
	if factory.Dialect() != "sqlite" {
		t.Fatalf("dialect = %q", factory.Dialect())
	}
}

func TestRepositoryFactorySelectsPostgresForCloud(t *testing.T) {
	factory, err := NewFactory(config.AppRuntimeConfig{
		DatabaseDialect: config.DatabasePostgres,
		DatabaseURL:     "postgres://example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if factory.Dialect() != "postgres" {
		t.Fatalf("dialect = %q", factory.Dialect())
	}
}

func TestRepositoryFactoryRequiresConnectionDetails(t *testing.T) {
	if _, err := NewFactory(config.AppRuntimeConfig{DatabaseDialect: config.DatabaseSQLite}); err == nil {
		t.Fatal("expected sqlite path error")
	}
	if _, err := NewFactory(config.AppRuntimeConfig{DatabaseDialect: config.DatabasePostgres}); err == nil {
		t.Fatal("expected postgres url error")
	}
}
