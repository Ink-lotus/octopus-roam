package op

import (
	"fmt"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

// These keys are supplied by the business, including the valid zero hour.
// Reject ambiguous inputs before writing rows or publishing statistics caches.
func validateStatsPrimaryKeys(totals []model.StatsTotal, hours []model.StatsHourly, apiKeys []model.StatsAPIKey) error {
	if len(totals) > 1 {
		return fmt.Errorf("stats_total must contain at most one row with ID 1")
	}
	for _, total := range totals {
		if total.ID != 1 {
			return fmt.Errorf("stats_total ID %d must be 1", total.ID)
		}
	}
	seenHours := [24]bool{}
	for _, hour := range hours {
		if hour.Hour < 0 || hour.Hour >= len(seenHours) {
			return fmt.Errorf("stats_hourly hour %d must be between 0 and 23", hour.Hour)
		}
		if seenHours[hour.Hour] {
			return fmt.Errorf("duplicate stats_hourly hour %d", hour.Hour)
		}
		seenHours[hour.Hour] = true
	}
	seenAPIKeys := make(map[int]bool, len(apiKeys))
	for _, key := range apiKeys {
		if key.APIKeyID <= 0 {
			return fmt.Errorf("stats_api_key ID %d must be positive", key.APIKeyID)
		}
		if seenAPIKeys[key.APIKeyID] {
			return fmt.Errorf("duplicate stats_api_key ID %d", key.APIKeyID)
		}
		seenAPIKeys[key.APIKeyID] = true
	}
	return nil
}
