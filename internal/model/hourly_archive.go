package model

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// StatsHourlyArchive is evidence, never a source for live statistics. The payload
// preserves SQLite values AND storage classes, including NULLs and float bits.
// Content addressing makes restoration/replay idempotent without merging snapshots.
type StatsHourlyArchive struct {
	ID           string `json:"id" gorm:"primaryKey;size:64"`
	Format       int    `json:"format"`
	Source       string `json:"source" gorm:"size:32"`
	OriginalHour int64  `json:"original_hour"`
	Payload      string `json:"payload" gorm:"type:text"`
}

func (StatsHourlyArchive) TableName() string { return "stats_hourly_archives" }

type HourlyArchiveCell struct {
	Column string `json:"column"`
	Type   string `json:"type"`
	Value  string `json:"value"`
}

func HourlyArchiveColumns() []string {
	return []string{"hour", "date", "input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed"}
}

// NewHourlyArchive accepts only a completely understood schema. Unknown columns
// require review instead of being silently omitted from a purported full archive.
func NewHourlyArchive(source string, cells []HourlyArchiveCell) (StatsHourlyArchive, error) {
	raw, err := json.Marshal(cells)
	if err != nil || len(cells) == 0 {
		return StatsHourlyArchive{}, fmt.Errorf("invalid hourly archive cells")
	}
	hour, err := strconv.ParseInt(cells[0].Value, 10, 64)
	if err != nil {
		return StatsHourlyArchive{}, fmt.Errorf("invalid hourly archive key")
	}
	a := StatsHourlyArchive{Format: 1, Source: source, OriginalHour: hour, Payload: string(raw)}
	a.ID = a.digest()
	return a, a.Validate()
}

func (a StatsHourlyArchive) digest() string {
	raw, _ := json.Marshal([]any{a.Format, a.Source, a.OriginalHour, a.Payload})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (a StatsHourlyArchive) Validate() error {
	bad := func() error { return fmt.Errorf("invalid hourly archive; inspect an independent copy") }
	if a.Format != 1 || a.OriginalHour < 24 || a.ID != a.digest() {
		return bad()
	}
	switch a.Source {
	case "sqlite-migration-18", "backup-1000", "backup-1001", "backup-1002", "backup-1003":
	default:
		return bad()
	}
	var cells []HourlyArchiveCell
	if json.Unmarshal([]byte(a.Payload), &cells) != nil || len(cells) != len(HourlyArchiveColumns()) {
		return bad()
	}
	canonical, _ := json.Marshal(cells)
	if string(canonical) != a.Payload {
		return bad()
	}
	for i, column := range HourlyArchiveColumns() {
		cell := cells[i]
		if cell.Column != column {
			return bad()
		}
		switch cell.Type {
		case "null":
			if cell.Value != "" {
				return bad()
			}
		case "integer":
			v, err := strconv.ParseInt(cell.Value, 10, 64)
			if err != nil || strconv.FormatInt(v, 10) != cell.Value {
				return bad()
			}
		case "real":
			// Hex IEEE-754 bits avoid decimal round trips and preserve negative zero.
			v, err := strconv.ParseUint(cell.Value, 16, 64)
			if err != nil || fmt.Sprintf("%016x", v) != cell.Value {
				return bad()
			}
		case "text", "blob":
			// Base64 preserves all bytes, including malformed UTF-8 in old SQLite.
			v, err := base64.StdEncoding.DecodeString(cell.Value)
			if err != nil || base64.StdEncoding.EncodeToString(v) != cell.Value {
				return bad()
			}
		default:
			return bad()
		}
	}
	if cells[0].Type != "integer" || cells[0].Value != strconv.FormatInt(a.OriginalHour, 10) {
		return bad()
	}
	return nil
}

func NewHourlyArchiveCell(column string, value any) (HourlyArchiveCell, error) {
	c := HourlyArchiveCell{Column: column}
	switch v := value.(type) {
	case nil:
		c.Type = "null"
	case int64:
		c.Type, c.Value = "integer", strconv.FormatInt(v, 10)
	case float64:
		c.Type, c.Value = "real", fmt.Sprintf("%016x", math.Float64bits(v))
	case string:
		c.Type, c.Value = "text", base64.StdEncoding.EncodeToString([]byte(v))
	case []byte:
		c.Type, c.Value = "blob", base64.StdEncoding.EncodeToString(v)
	default:
		return c, fmt.Errorf("unsupported SQLite hourly storage value")
	}
	return c, nil
}

func ArchiveLegacyHourly(row StatsHourly, version int) (StatsHourlyArchive, error) {
	values := []any{int64(row.Hour), row.Date, row.InputToken, row.OutputToken, row.InputCost, row.OutputCost, row.WaitTime, row.RequestSuccess, row.RequestFailed}
	cells := make([]HourlyArchiveCell, len(values))
	for i, value := range values {
		var err error
		cells[i], err = NewHourlyArchiveCell(HourlyArchiveColumns()[i], value)
		if err != nil {
			return StatsHourlyArchive{}, err
		}
	}
	return NewHourlyArchive(fmt.Sprintf("backup-%d", version), cells)
}
