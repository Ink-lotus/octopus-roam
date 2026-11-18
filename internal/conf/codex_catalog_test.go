package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestCodexCatalogConfigDefaultsFileAndEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, input    string
		environment    string
		enabled, proxy bool
	}{
		{"defaults", `{}`, "", true, false},
		{"missing enabled", `{"codex_catalog":{"use_proxy":true}}`, "", true, true},
		{"file", `{"codex_catalog":{"enabled":true,"use_proxy":true}}`, "", true, true},
		{"file disabled", `{"codex_catalog":{"enabled":false,"use_proxy":true}}`, "", false, true},
		{"environment", `{"codex_catalog":{"enabled":false,"use_proxy":true}}`, "true", true, false},
		{"environment disabled", `{"codex_catalog":{"enabled":true,"use_proxy":true}}`, "false", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			before := AppConfig
			t.Cleanup(func() { viper.Reset(); AppConfig = before })
			// Isolate the process's inherited runtime settings.
			t.Setenv("OCTOPUS_ROAM_CODEX_CATALOG_ENABLED", "")
			t.Setenv("OCTOPUS_ROAM_CODEX_CATALOG_USE_PROXY", "")
			if test.environment != "" {
				t.Setenv("OCTOPUS_ROAM_CODEX_CATALOG_ENABLED", test.environment)
				t.Setenv("OCTOPUS_ROAM_CODEX_CATALOG_USE_PROXY", "false")
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.input), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Load(path); err != nil {
				t.Fatal(err)
			}
			if AppConfig.CodexCatalog.Enabled != test.enabled || AppConfig.CodexCatalog.UseProxy != test.proxy {
				t.Fatalf("catalog settings: %+v", AppConfig.CodexCatalog)
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != test.input {
				t.Fatal("loading rewrote the configuration")
			}
		})
	}
}
