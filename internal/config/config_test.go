package config

import (
	"testing"
)

func setBaseEnv(t *testing.T) {
	t.Helper()

	t.Setenv("APP_ENV", "")
	t.Setenv("APP_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "")
}

func TestLoadDefaults(t *testing.T) {
	setBaseEnv(t)

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/staynest")
	t.Setenv("JWT_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Environment != "development" {
		t.Errorf("expected environment development, got %q", cfg.Environment)
	}

	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}
}

func TestLoadCustomValues(t *testing.T) {
	setBaseEnv(t)

	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/staynest")
	t.Setenv("JWT_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.Environment != "test" {
		t.Errorf("expected environment test, got %q", cfg.Environment)
	}

	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
}

func TestLoadMissingDatabaseURL(t *testing.T) {
	setBaseEnv(t)

	t.Setenv("JWT_SECRET", "test-secret")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}

func TestLoadMissingJWTSecret(t *testing.T) {
	setBaseEnv(t)

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/staynest")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing JWT_SECRET")
	}
}

func TestLoadInvalidPort(t *testing.T) {
	invalidPorts := []string{
		"abc",
		"0",
		"-1",
		"65536",
	}

	for _, port := range invalidPorts {
		t.Run(port, func(t *testing.T) {
			setBaseEnv(t)

			t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/staynest")
			t.Setenv("JWT_SECRET", "test-secret")
			t.Setenv("APP_PORT", port)

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for invalid port %q", port)
			}
		})
	}
}

func TestLoadInvalidEnvironment(t *testing.T) {
	setBaseEnv(t)

	t.Setenv("APP_ENV", "staging")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/staynest")
	t.Setenv("JWT_SECRET", "test-secret")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid APP_ENV")
	}
}
