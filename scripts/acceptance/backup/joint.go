package main

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

// The joint runner cannot point at arbitrary databases, even on loopback.
func jointDatabaseName(name string) bool {
	return regexp.MustCompile(`^roam_backup_b04_(c0[1-8]|cross_(sqlite|mysql|postgres)_(seed|sqlite|mysql|postgres))$`).MatchString(name)
}

func (s *suite) verifyJointCase(base *model.DBDump) error {
	if !regexp.MustCompile(`^C0[1-8]$`).MatchString(s.opts.scenario) {
		return errors.New("B04 matrix requires one explicit C01-C08 case")
	}
	s.scope = s.opts.scenario
	defer db.Close()
	if err := s.step("initialize", func() (any, error) { return nil, s.boot(s.dsn) }); err != nil {
		return err
	}
	seed := cloneDump(base)
	seed.Groups[0].ActiveItemID = seed.GroupItems[0].ID
	if s.opts.scenario == "C06" || s.opts.scenario == "C07" {
		seed.StatsHourly = []model.StatsHourly{{Hour: 0, Date: time.Now().Format("20060102"), StatsMetrics: model.StatsMetrics{InputToken: 100}}}
		if s.opts.scenario == "C07" {
			seed.StatsHourly = append(seed.StatsHourly, model.StatsHourly{Hour: 1, Date: seed.StatsHourly[0].Date, StatsMetrics: model.StatsMetrics{InputToken: 200}})
		}
	}
	if err := s.step("complete-control-import", func() (any, error) {
		_, err := op.DBImportIncremental(s.ctx, seed)
		return nil, err
	}); err != nil {
		return err
	}
	input := cloneDump(seed)
	var rejected error
	switch s.opts.scenario {
	case "C02":
		input.Channels[0].BaseURL = "http://127.0.0.1:2"
		input.ChannelKeys[0].Key = "synthetic-must-not-replace"
		rejected = op.ErrBackupConflict
	case "C03":
		input = integrityInput(base, 1000)
		input.ChannelKeys[0].ID = seed.ChannelKeys[0].ID
		input.ChannelGrants[0].ChannelKeyID = seed.ChannelKeys[0].ID
		rejected = op.ErrBackupConflict
	case "C04":
		input = integrityInput(base, 2000)
		input.GroupItems[0].ID = seed.GroupItems[0].ID
		input.Groups[0].ActiveItemID = input.GroupItems[0].ID
		rejected = op.ErrBackupConflict
	case "C05":
		input.ChannelKeys[0].ID = 9000
		input.ChannelGrants[0].ChannelKeyID = 9000
		rejected = op.ErrBackupConflict
	case "C08":
		input = integrityInput(base, 3000)
		input.GroupItems[0].ChannelGrantID = 999999
		rejected = op.ErrBackupInput
	}
	if rejected != nil {
		if err := s.step("reject-with-identical-data-cache-and-publication", func() (any, error) {
			return s.rejectIntegrityInput(input, rejected)
		}); err != nil {
			return err
		}
	} else if err := s.step("safe-repeat-import", func() (any, error) {
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		if _, err := op.DBImportIncremental(s.ctx, input); err != nil {
			return nil, err
		}
		after, err := snapshot()
		if err == nil && before != after {
			err = &assertionFailure{"repeat changed database content"}
		}
		return map[string]string{"before": before, "after": after}, err
	}); err != nil {
		return err
	}
	if err := s.step("actual-graph-cache-and-hours", func() (any, error) {
		if err := s.verifyGraph(seed); err != nil {
			return nil, err
		}
		return s.jointCacheAndStatistics(seed)
	}); err != nil {
		return err
	}
	if s.opts.scenario == "C08" {
		if err := s.step("late-database-failure-full-rollback", func() (any, error) { return s.verifyRollback(seed) }); err != nil {
			return err
		}
	}
	return s.jointFollowup(seed)
}

func (s *suite) jointCacheAndStatistics(seed *model.DBDump) (any, error) {
	detail, err := op.ChannelDetailGet(seed.Channels[0].ID)
	if err != nil || detail.BaseURL != seed.Channels[0].BaseURL || len(detail.Keys) != len(seed.ChannelKeys) || len(detail.Models) != len(seed.ChannelModels) || len(detail.Grants) != len(seed.ChannelGrants) {
		return nil, &assertionFailure{"channel cache differs from the imported graph"}
	}
	for _, row := range seed.ChannelGrants {
		grant, err := op.ChannelGrantGet(row.ID)
		if err != nil || grant.ChannelKeyID != row.ChannelKeyID || grant.ChannelModelID != row.ChannelModelID {
			return nil, &assertionFailure{"grant cache changed parent ownership"}
		}
	}
	var hours []model.StatsHourly
	if err := db.GetDB().Order("hour").Find(&hours).Error; err != nil {
		return nil, err
	}
	if len(hours) != len(seed.StatsHourly) {
		return nil, &assertionFailure{"hourly row count differs"}
	}
	cache := op.StatsHourlyGet()
	for i, hour := range hours {
		if hour != seed.StatsHourly[i] {
			return nil, &assertionFailure{"hourly database slot or metrics changed"}
		}
		// The public view deliberately ends at the current hour; all 24 raw
		// slots are checked above and again after SaveCache and reconnect.
		if hour.Date == time.Now().Format("20060102") && hour.Hour < len(cache) && cache[hour.Hour] != hour {
			return nil, &assertionFailure{"hourly cache differs from the database"}
		}
	}
	dump, err := op.DBExportAll(s.ctx)
	if err != nil {
		return nil, err
	}
	if len(integrity.Inspect(dump)) != 0 {
		return nil, &assertionFailure{"actual database contains an invalid association"}
	}
	if len(seed.StatsTotal) == 1 && op.StatsTotalGet() != seed.StatsTotal[0] {
		return nil, &assertionFailure{"total statistics cache differs"}
	}
	return map[string]any{"actual_hours": hours, "visible_cache_slots": len(cache), "valid_graph": true, "cache_matches": true}, nil
}

func (s *suite) jointFollowup(seed *model.DBDump) error {
	if err := s.step("first-save-auto-add-and-repeat-save", func() (any, error) { return nil, s.saveAutoAdd(seed) }); err != nil {
		return err
	}
	if err := s.step("nine-entity-types-real-new-inserts", s.createEntities); err != nil {
		return err
	}
	if err := s.step("normal-statistics-save", func() (any, error) {
		before := op.StatsTotalGet().InputToken
		if err := op.StatsTotalUpdate(model.StatsMetrics{InputToken: 13}); err != nil {
			return nil, err
		}
		if err := op.SaveCache(); err != nil {
			return nil, err
		}
		var total model.StatsTotal
		if err := db.GetDB().First(&total, 1).Error; err != nil {
			return nil, err
		}
		if total.InputToken != before+13 || op.StatsTotalGet() != total {
			return nil, &assertionFailure{"normal statistics save lost metrics"}
		}
		return map[string]int64{"before": before, "database_and_cache_after": total.InputToken}, nil
	}); err != nil {
		return err
	}
	return s.step("reconnect-content-and-relations", func() (any, error) {
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		if err := db.Close(); err != nil {
			return nil, err
		}
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		after, err := snapshot()
		if err != nil || before != after {
			return nil, &assertionFailure{"reconnect changed business rows"}
		}
		dump, err := op.DBExportAll(s.ctx)
		if err == nil && len(integrity.Inspect(dump)) != 0 {
			err = &assertionFailure{"reconnect exposed an invalid graph"}
		}
		return map[string]string{"before": before, "after": after}, err
	})
}

func portableDumpHash(dump *model.DBDump) string {
	dump = cloneDump(dump)
	dump.ExportedAt = time.Time{}
	for _, rows := range []any{dump.Channels, dump.ChannelKeys, dump.ChannelModels, dump.ChannelGrants, dump.GroupCategories, dump.Groups, dump.GroupItems, dump.APIKeys, dump.WebhookEndpoints, dump.StatsTotal} {
		sortRows(rows, "ID")
	}
	sortRows(dump.LLMInfos, "Name")
	sortRows(dump.Settings, "Key")
	sortRows(dump.StatsDaily, "Date")
	sortRows(dump.StatsHourly, "Hour")
	sortRows(dump.StatsAPIKey, "APIKeyID")
	raw, _ := json.Marshal(dump)
	return digest(raw)
}

func (s *suite) verifyCrossDatabase(base *model.DBDump) error {
	if s.opts.crossBackup == "" {
		return errors.New("cross-database backup path is required")
	}
	defer db.Close()
	seed := cloneDump(base)
	seed.Groups[0].ActiveItemID = seed.GroupItems[0].ID
	seed.StatsTotal = []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 300, InputCost: 0.125}}}
	seed.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: seed.APIKeys[0].ID, StatsMetrics: model.StatsMetrics{InputToken: 300}}}
	seed.StatsHourly = nil
	for hour := 0; hour < 24; hour++ {
		seed.StatsHourly = append(seed.StatsHourly, model.StatsHourly{Hour: hour, Date: time.Now().Format("20060102"), StatsMetrics: model.StatsMetrics{InputToken: int64((hour + 1) * 100)}})
	}
	var encrypted []byte
	if s.opts.stage == "cross-import" {
		var err error
		encrypted, err = os.ReadFile(s.opts.crossBackup)
		if err != nil {
			return err
		}
		seed, err = op.DecryptBackup(encrypted, fixturePassword)
		if err != nil || seed.Product != "octopus-roam" || seed.Version != 1003 || len(seed.Channels) != 1 || seed.Channels[0].Name != fixtureChannel || len(seed.StatsHourly) != 24 || len(seed.StatsTotal) != 1 || seed.StatsTotal[0].InputToken != 300 {
			return errors.New("cross-database input is not the synthetic source export")
		}
	}
	if err := s.step("initialize-and-import", func() (any, error) {
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		_, err := op.DBImportIncremental(s.ctx, seed)
		return nil, err
	}); err != nil {
		return err
	}
	var exported *model.DBDump
	if err := s.step("actual-data-graph-cache-and-export", func() (any, error) {
		if err := s.verifyGraph(seed); err != nil {
			return nil, err
		}
		if _, err := s.jointCacheAndStatistics(seed); err != nil {
			return nil, err
		}
		var err error
		exported, err = op.DBExportAll(s.ctx)
		if err != nil {
			return nil, err
		}
		if s.opts.stage == "cross-import" && portableDumpHash(seed) != portableDumpHash(exported) {
			return nil, &assertionFailure{"cross-database restore changed exported persistent fields"}
		}
		if !reflect.DeepEqual(exported.StatsAPIKey, seed.StatsAPIKey) {
			return nil, &assertionFailure{"cross-database API-key statistics differ"}
		}
		return map[string]string{"persistent_content_sha256": portableDumpHash(exported), "encrypted_input_sha256": digest(encrypted)}, nil
	}); err != nil {
		return err
	}
	if s.opts.stage == "cross-export" {
		return s.step("source-database-encrypted-export", func() (any, error) {
			raw, err := op.EncryptBackup(exported, fixturePassword)
			if err == nil {
				err = writeExclusive(s.opts.crossBackup, raw)
			}
			return map[string]string{"encrypted_backup_sha256": digest(raw)}, err
		})
	}
	if err := s.step("repeat-cross-database-restore", func() (any, error) {
		_, err := op.DBImportIncremental(s.ctx, seed)
		return nil, err
	}); err != nil {
		return err
	}
	return s.jointFollowup(seed)
}
