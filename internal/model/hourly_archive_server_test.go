package model

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func serverArchiveCells(t *testing.T, dialect string) []HourlyArchiveCell {
	t.Helper()
	types := []string{"bigint", "longtext", "bigint", "bigint", "double", "double", "bigint", "bigint", "bigint"}
	values := []any{int64(1145), []byte{0, 0xff}, nil, int64(math.MaxInt64), math.Copysign(0, -1), 0.1, int64(7), int64(8), int64(9)}
	if dialect == "postgres" {
		types[1], types[4], types[5] = "text", "real", "double precision"
		values[4], values[5] = []byte{0x80, 0, 0, 0}, []byte{0x7f, 0xf8, 0, 0, 0, 0, 0, 0x45}
	}
	cells := make([]HourlyArchiveCell, len(values))
	for i := range values {
		var err error
		cells[i], err = NewServerHourlyArchiveCell(dialect+"-migration-18", HourlyArchiveColumns()[i], types[i], values[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	return cells
}

func TestServerHourlyArchivesPreserveNativeTypesAndBits(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			cells := serverArchiveCells(t, dialect)
			a, err := NewHourlyArchive(dialect+"-migration-18", cells)
			if err != nil || a.Format != 2 || a.Validate() != nil {
				t.Fatal("invalid server archive", err)
			}
			if cells[1].Value != "AP8=" || cells[2].Type != "null" || cells[3].Value != "9223372036854775807" {
				t.Fatal("native values changed")
			}
			if dialect == "postgres" && (cells[4].Value != "80000000" || cells[5].Value != "7ff8000000000045") {
				t.Fatal("float width, negative zero or NaN payload lost")
			}
			if dialect == "mysql" && (cells[4].Value != "8000000000000000" || cells[5].Value != "3fb999999999999a") {
				t.Fatal("double rounded")
			}
			encoded, _ := json.Marshal(a)
			var restored StatsHourlyArchive
			if json.Unmarshal(encoded, &restored) != nil || restored != a || restored.Validate() != nil {
				t.Fatal("archive JSON changed the evidence")
			}
			for _, change := range []func([]HourlyArchiveCell){
				func(c []HourlyArchiveCell) { c[4].SQLType = "numeric" },
				func(c []HourlyArchiveCell) { c[4].Value = "00" },
				func(c []HourlyArchiveCell) { c[1].Type = "blob" },
				func(c []HourlyArchiveCell) { c[0].Type = "null"; c[0].Value = "" },
				func(c []HourlyArchiveCell) { c[3].Value = "0922" },
			} {
				bad := append([]HourlyArchiveCell(nil), cells...)
				change(bad)
				if _, err := NewHourlyArchive(a.Source, bad); err == nil {
					t.Fatal("invalid native archive accepted")
				}
			}
			legacy, err := NewHourlyArchive("sqlite-migration-18", cells)
			if err == nil || legacy.Validate() == nil {
				t.Fatal("server payload mislabeled as SQLite")
			}
		})
	}
}

func TestServerHourlyCellRejectsLossyRepresentations(t *testing.T) {
	for _, tc := range []struct {
		source, column, sqlType string
		value                   any
	}{
		{"mysql-migration-18", "input_cost", "double", []byte("0.1")},
		{"postgres-migration-18", "input_cost", "real", 0.1},
		{"postgres-migration-18", "input_cost", "real", []byte{0, 0, 0, 0, 0, 0, 0, 0}},
		{"mysql-migration-18", "input_cost", "decimal(20,10)", []byte("0.1")},
		{"mysql-migration-18", "hour", "bigint", "24"},
		{"postgres-migration-18", "unknown", "bigint", int64(24)},
	} {
		if _, err := NewServerHourlyArchiveCell(tc.source, tc.column, tc.sqlType, tc.value); err == nil {
			t.Fatal("lossy or unknown server value accepted")
		}
	}
	unsigned, err := NewServerHourlyArchiveCell("mysql-migration-18", "input_token", "bigint unsigned", uint64(math.MaxUint64))
	if err != nil || unsigned.Type != "unsigned" || unsigned.Value != "18446744073709551615" {
		t.Fatal("unsigned value lost", err)
	}
	legacy := make([]HourlyArchiveCell, len(HourlyArchiveColumns()))
	for i, column := range HourlyArchiveColumns() {
		legacy[i], _ = NewHourlyArchiveCell(column, int64(24))
	}
	a, err := NewHourlyArchive("sqlite-migration-18", legacy)
	if err != nil || a.Format != 1 || strings.Contains(a.Payload, "sql_type") {
		t.Fatal("existing SQLite format changed", err)
	}
}
