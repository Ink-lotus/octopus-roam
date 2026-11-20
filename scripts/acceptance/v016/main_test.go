package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestFixtureFeatureBoundariesAndNativeArchiveRoundTrip(t *testing.T) {
	files, err := inputs()
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range files {
		t.Run(name, func(t *testing.T) {
			var d model.DBDump
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			if issues := integrity.Inspect(&d); len(issues) != 0 {
				t.Fatal(issues)
			}
			c := d.Channels[0]
			if (len(c.CooldownRules) > 0) != (d.Version >= 1001) || c.StripResponsesHistory != (d.Version >= 1002) || c.CustomHeaderModelOnly != (d.Version >= 1003) {
				t.Fatal("historical fixture has features outside its contract")
			}
			for _, rule := range c.CooldownRules {
				if (rule.Scope == model.CooldownScopeKey || rule.NotifyWebhook) != (d.Version == 1006) {
					t.Fatal("extended cooldown was relabelled as an old content version")
				}
			}
			for _, a := range d.StatsHourlyArchives {
				if err := a.Validate(); err != nil {
					t.Fatal(err)
				}
				var cells []model.HourlyArchiveCell
				if err := json.Unmarshal([]byte(a.Payload), &cells); err != nil || cells[2].Type != "null" || cells[5].Value != "8000000000000000" {
					t.Fatal("NULL or negative zero evidence was lost", err)
				}
				if a.Source == "postgres-migration-18" && (cells[4].Type != "real32" || cells[4].Value != "3dcccccd") {
					t.Fatal("PostgreSQL REAL evidence changed")
				}
			}
			encoded, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			var restored model.DBDump
			if err := json.Unmarshal(encoded, &restored); err != nil || !reflect.DeepEqual(d.StatsHourlyArchives, restored.StatsHourlyArchives) {
				t.Fatal("archive round trip changed evidence", err)
			}
		})
	}
}

func TestGeneratorRefusesExistingOutputAndAlteredFixtures(t *testing.T) {
	parent := t.TempDir()
	path := parent + "/new"
	if err := run(path, false); err != nil {
		t.Fatal(err)
	}
	if err := run(path, true); err != nil {
		t.Fatal(err)
	}
	if err := run(path, false); err == nil {
		t.Fatal("generator overwrote existing evidence")
	}
	if _, err := fixture(1004, "both"); err == nil {
		t.Fatal("format 2 archive was relabelled as 1004")
	}
	if _, err := fixture(1007, "none"); err == nil {
		t.Fatal("unknown content version accepted")
	}
}
