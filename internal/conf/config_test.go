package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadRoamEnvironmentOverrides(t *testing.T) {
	viper.Reset()
	savedConfig := AppConfig
	t.Cleanup(func() {
		viper.Reset()
		AppConfig = savedConfig
	})

	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"server":{"host":"127.0.0.1","port":8080},"database":{"type":"sqlite","path":"data/data.db"},"log":{"level":"info"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	databasePath := filepath.Join(directory, "roam.db")
	t.Setenv("OCTOPUS_ROAM_SERVER_PORT", "18081")
	t.Setenv("OCTOPUS_ROAM_DATABASE_PATH", databasePath)
	if err := Load(configPath); err != nil {
		t.Fatal(err)
	}
	if AppConfig.Server.Port != 18081 {
		t.Fatalf("environment did not override configured port: %d", AppConfig.Server.Port)
	}
	if AppConfig.Database.Path != databasePath {
		t.Fatal("environment did not override configured database path")
	}
	if AppConfig.Server.Host != "127.0.0.1" {
		t.Fatal("file configuration was not retained for the host")
	}
}
