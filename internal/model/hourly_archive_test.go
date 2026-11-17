package model

import (
	"math"
	"reflect"
	"testing"
)

func TestHourlyArchivePreservesSQLiteStorageClasses(t *testing.T) {
	values := []any{int64(1145), string([]byte{0xff, 0x00}), nil, int64(9223372036854775807), math.Float64frombits(0x8000000000000000), 0.125, []byte{0, 255}, int64(7), "raw legacy text"}
	cells := make([]HourlyArchiveCell, len(values))
	for i, value := range values {
		var err error
		cells[i], err = NewHourlyArchiveCell(HourlyArchiveColumns()[i], value)
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := NewHourlyArchive("sqlite-migration-18", cells)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if cells[4].Value != "8000000000000000" || cells[2].Type != "null" || cells[6].Type != "blob" {
		t.Fatal("raw values normalized")
	}
	b, err := NewHourlyArchive("sqlite-migration-18", cells)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("content identity is unstable", err)
	}
	for _, edit := range []func(*StatsHourlyArchive){
		func(a *StatsHourlyArchive) { a.Payload += " " },
		func(a *StatsHourlyArchive) { a.ID = "00" },
		func(a *StatsHourlyArchive) { a.Format++ },
		func(a *StatsHourlyArchive) { a.OriginalHour = 0 },
		func(a *StatsHourlyArchive) { a.Source = "unreviewed" },
	} {
		bad := a
		edit(&bad)
		if bad.Validate() == nil {
			t.Fatal("tampered archive accepted")
		}
	}
}
