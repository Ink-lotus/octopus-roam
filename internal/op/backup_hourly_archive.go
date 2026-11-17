package op

import (
	"fmt"
	"math"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func validateHourlyArchives(d *model.DBDump) error {
	if len(d.StatsHourlyArchives) > 0 && (d.Product != dumpProduct || d.Version != 1004) {
		return fmt.Errorf("stats_hourly_archives requires backup version 1004")
	}
	seen := map[string]bool{}
	for _, row := range d.StatsHourlyArchives {
		if row.Validate() != nil || seen[row.ID] {
			return fmt.Errorf("invalid or duplicate hourly archive")
		}
		seen[row.ID] = true
	}
	return nil
}

// ConvertLegacyHourlyBackup is an explicit offline conversion, never an import
// fallback. It preserves the input and validates the remaining graph. A version
// label is eligibility, not proof of provenance; keep the original encrypted file.
func ConvertLegacyHourlyBackup(source *model.DBDump) (*model.DBDump, int, error) {
	if source == nil || source.Product != dumpProduct || source.Version < 1000 || source.Version > 1003 {
		return nil, 0, fmt.Errorf("legacy hourly conversion requires an octopus-roam 1000-1003 backup")
	}
	if len(source.StatsHourlyArchives) != 0 {
		return nil, 0, fmt.Errorf("legacy backup already contains archive data")
	}
	dump := cloneBackupInput(source)
	if err := validateBackupFeatureFields(dump); err != nil {
		return nil, 0, fmt.Errorf("legacy backup feature validation failed")
	}
	seen := map[int]bool{}
	hours := make([]model.StatsHourly, 0, len(dump.StatsHourly))
	for _, row := range dump.StatsHourly {
		if row.Hour < 0 || seen[row.Hour] || row.Date == "" || !validBackupDate(row.Date, row.StatsMetrics) || !validLegacyHourlyMetrics(row.StatsMetrics) {
			return nil, 0, fmt.Errorf("legacy hourly conversion rejected invalid or duplicate statistics")
		}
		seen[row.Hour] = true
		if row.Hour < 24 {
			hours = append(hours, row)
			continue
		}
		archive, err := model.ArchiveLegacyHourly(row, source.Version)
		if err != nil {
			return nil, 0, err
		}
		dump.StatsHourlyArchives = append(dump.StatsHourlyArchives, archive)
	}
	dump.StatsHourly, dump.Version = hours, 1004
	if err := validateStatsPrimaryKeys(dump.StatsTotal, dump.StatsHourly, dump.StatsAPIKey); err != nil {
		return nil, 0, err
	}
	if err := validateHourlyArchives(dump); err != nil {
		return nil, 0, err
	}
	// Validate a clone: conversion must not normalize or rewrite business data.
	check := cloneBackupInput(dump)
	if err := normalizeBackupRows(check); err != nil {
		return nil, 0, fmt.Errorf("legacy backup configuration validation failed")
	}
	if err := validateBackupGraph(check); err != nil {
		return nil, 0, fmt.Errorf("legacy backup integrity validation failed")
	}
	if err := validateBackupSettings(check.Settings); err != nil {
		return nil, 0, err
	}
	return dump, len(dump.StatsHourlyArchives), nil
}

func validLegacyHourlyMetrics(m model.StatsMetrics) bool {
	return m.InputToken >= 0 && m.OutputToken >= 0 && m.WaitTime >= 0 && m.RequestSuccess >= 0 && m.RequestFailed >= 0 &&
		m.InputCost >= 0 && m.OutputCost >= 0 && !math.IsNaN(m.InputCost) && !math.IsNaN(m.OutputCost) && !math.IsInf(m.InputCost, 0) && !math.IsInf(m.OutputCost, 0)
}
