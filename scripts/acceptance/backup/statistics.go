package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var businessKeys = []struct{ table, key string }{
	{"stats_totals", "id"}, {"stats_hourlies", "hour"}, {"stats_api_keys", "api_key_id"}, {"migration_records", "version"},
}

func sqlName(conn *gorm.DB, name string) string {
	var b strings.Builder
	conn.Dialector.QuoteTo(&b, name)
	return b.String()
}

func businessKeyMetadata(conn *gorm.DB) (map[string]any, error) {
	result := make(map[string]any)
	for _, key := range businessKeys {
		switch conn.Dialector.Name() {
		case "sqlite":
			var ddl string
			if err := conn.Raw("SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", key.table).Scan(&ddl).Error; err != nil {
				return nil, err
			}
			result[key.table] = map[string]any{"generated": strings.Contains(strings.ToUpper(ddl), "AUTOINCREMENT"), "ddl": ddl}
		case "mysql":
			var column struct {
				Type    string         `gorm:"column:column_type" json:"type"`
				Extra   string         `gorm:"column:extra" json:"extra"`
				Default sql.NullString `gorm:"column:column_default" json:"default"`
			}
			if err := conn.Raw(`SELECT column_type,extra,column_default FROM information_schema.columns
				WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, key.table, key.key).Scan(&column).Error; err != nil {
				return nil, err
			}
			result[key.table] = map[string]any{"generated": strings.Contains(strings.ToLower(column.Extra), "auto_increment"), "column": column}
		case "postgres":
			var column struct {
				Default  sql.NullString `gorm:"column:column_default" json:"default"`
				Identity string         `gorm:"column:is_identity" json:"identity"`
				Owned    sql.NullString `gorm:"column:owned_sequence" json:"owned_sequence"`
			}
			if err := conn.Raw(`SELECT column_default,is_identity,pg_get_serial_sequence(quote_ident(table_schema)||'.'||quote_ident(table_name),column_name) AS owned_sequence
				FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND column_name=?`, key.table, key.key).Scan(&column).Error; err != nil {
				return nil, err
			}
			result[key.table] = map[string]any{"generated": column.Default.Valid || column.Identity == "YES" || column.Owned.Valid, "column": column}
		}
	}
	return result, nil
}

func assertBusinessGeneration(metadata map[string]any, generated bool) error {
	for _, key := range businessKeys {
		if metadata[key.table].(map[string]any)["generated"] != generated {
			return &assertionFailure{fmt.Sprintf("%s generation differs from expected %t", key.table, generated)}
		}
	}
	return nil
}

func (s *suite) verifyStatistics() error {
	defer db.Close()
	if err := s.step("statistics-fresh-init", func() (any, error) {
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		metadata, err := businessKeyMetadata(db.GetDB())
		if err == nil {
			err = assertBusinessGeneration(metadata, false)
		}
		return metadata, err
	}); err != nil {
		return err
	}
	return s.statisticsCases()
}

func (s *suite) statisticsCases() error {
	// A statistics-only backup must still describe its API-key parent. This is
	// synthetic fixture data, never a guessed repair of a user's missing key.
	var apiKey model.APIKey
	if err := db.GetDB().First(&apiKey, 7).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		apiKey = statisticsAPIKey()
		if _, err := op.DBImportIncremental(s.ctx, &model.DBDump{Product: "octopus-roam", Version: 1003, APIKeys: []model.APIKey{apiKey}}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for _, count := range []int{1, 2, 24} {
		if err := s.step(fmt.Sprintf("C06-C07-%02d-hours-replay-cache-reconnect-export", count), func() (any, error) {
			input := &model.DBDump{Product: "octopus-roam", Version: 1003,
				APIKeys:     []model.APIKey{apiKey},
				StatsTotal:  []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 300}}},
				StatsAPIKey: []model.StatsAPIKey{{APIKeyID: 7, StatsMetrics: model.StatsMetrics{InputToken: 300}}}}
			for hour := 0; hour < count; hour++ {
				input.StatsHourly = append(input.StatsHourly, model.StatsHourly{Hour: hour, Date: time.Now().Format("20060102"), StatsMetrics: model.StatsMetrics{InputToken: int64(100 * (hour + 1))}})
			}
			var existing []model.StatsHourly
			if err := db.GetDB().Order("hour").Find(&existing).Error; err != nil {
				return nil, err
			}
			expectedByHour := make(map[int]model.StatsHourly)
			for _, row := range existing {
				expectedByHour[row.Hour] = row
			}
			for _, row := range input.StatsHourly {
				expectedByHour[row.Hour] = row
			}
			expected := make([]model.StatsHourly, 0, len(expectedByHour))
			for _, row := range expectedByHour {
				expected = append(expected, row)
			}
			sortRows(expected, "Hour")
			raw, err := json.Marshal(input)
			if err != nil {
				return nil, err
			}
			for range 2 {
				encrypted, err := op.EncryptBackup(cloneDump(input), fixturePassword)
				if err != nil {
					return nil, err
				}
				copy, err := op.DecryptBackup(encrypted, fixturePassword)
				if err != nil {
					return nil, err
				}
				if _, err := op.DBImportIncremental(s.ctx, copy); err != nil {
					return nil, err
				}
				if err := op.InitCache(); err != nil {
					return nil, err
				}
				if err := s.assertStatisticsHours(expected); err != nil {
					return nil, err
				}
			}
			if err := db.Close(); err != nil {
				return nil, err
			}
			if err := s.boot(s.dsn); err != nil {
				return nil, err
			}
			if err := s.assertStatisticsHours(expected); err != nil {
				return nil, err
			}
			return map[string]any{"input_sha256": digest(raw), "hours": input.StatsHourly, "replays": 2}, nil
		}); err != nil {
			return err
		}
	}
	if err := s.step("invalid-statistics-input-full-rejection", func() (any, error) {
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		inputs := []*model.DBDump{
			{StatsHourly: []model.StatsHourly{{Hour: -1}}}, {StatsHourly: []model.StatsHourly{{Hour: 24}}},
			{StatsHourly: []model.StatsHourly{{Hour: 0}, {Hour: 0}}}, {StatsTotal: []model.StatsTotal{{ID: 0}}},
			{StatsTotal: []model.StatsTotal{{ID: 2}}}, {StatsTotal: []model.StatsTotal{{ID: 1}, {ID: 1}}},
			{StatsAPIKey: []model.StatsAPIKey{{APIKeyID: 0}}}, {StatsAPIKey: []model.StatsAPIKey{{APIKeyID: 7}, {APIKeyID: 7}}},
		}
		for _, input := range inputs {
			input.Product, input.Version = "octopus-roam", 1003
			if _, err := op.DBImportIncremental(s.ctx, input); err == nil {
				return nil, &assertionFailure{"invalid statistics accepted"}
			}
			after, err := snapshot()
			if err != nil || after != before {
				return nil, &assertionFailure{"rejected statistics changed business rows"}
			}
		}
		return map[string]any{"cases": len(inputs), "preserved_sha256": before}, nil
	}); err != nil {
		return err
	}
	return s.step("ordinary-update-shutdown-restart-persistence", func() (any, error) {
		var hours []model.StatsHourly
		if err := db.GetDB().Order("hour").Find(&hours).Error; err != nil {
			return nil, err
		}
		hour := time.Now().Hour()
		metrics := model.StatsMetrics{InputToken: 13}
		if err := op.StatsHourlyUpdate(metrics); err != nil {
			return nil, err
		}
		if err := op.StatsDailyUpdate(s.ctx, metrics); err != nil {
			return nil, err
		}
		if err := op.StatsTotalUpdate(metrics); err != nil {
			return nil, err
		}
		if err := op.StatsAPIKeyUpdate(7, metrics); err != nil {
			return nil, err
		}
		hours[hour].InputToken += 13
		// This is the actual application's normal shutdown cache writer.
		if err := op.SaveCache(); err != nil {
			return nil, err
		}
		if err := db.Close(); err != nil {
			return nil, err
		}
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		if err := s.assertStatisticsHours(hours); err != nil {
			return nil, err
		}
		if op.StatsTotalGet().InputToken != 313 || op.StatsAPIKeyGet(7).InputToken != 313 {
			return nil, &assertionFailure{"ordinary total/API statistics failed to persist"}
		}
		return map[string]any{"hour": hour, "delta": 13, "total": 313, "api_key_id": 7}, nil
	})
}

func statisticsAPIKey() model.APIKey {
	return model.APIKey{ID: 7, Name: "b02-statistics-parent", APIKey: "synthetic-b02-statistics-parent", Enabled: true}
}

func (s *suite) assertStatisticsHours(expected []model.StatsHourly) error {
	var rows []model.StatsHourly
	if err := db.GetDB().Order("hour").Find(&rows).Error; err != nil {
		return err
	}
	if !reflect.DeepEqual(rows, expected) {
		return &assertionFailure{"persistent hourly values moved, changed or duplicated"}
	}
	visible := op.StatsHourlyGet()
	today := time.Now().Format("20060102")
	for _, row := range expected {
		view := row
		if row.Date != today {
			view = model.StatsHourly{Hour: row.Hour, Date: today}
		}
		if row.Hour < len(visible) && !reflect.DeepEqual(visible[row.Hour], view) {
			return &assertionFailure{"hourly cache changed values"}
		}
	}
	dump, err := op.DBExportAll(s.ctx)
	if err != nil {
		return err
	}
	sortRows(dump.StatsHourly, "Hour")
	if !reflect.DeepEqual(dump.StatsHourly, expected) {
		return &assertionFailure{"re-export changed hourly values"}
	}
	return nil
}

func statisticsSnapshot(conn *gorm.DB) (string, error) {
	rows := map[string]any{"stats_totals": &[]model.StatsTotal{}, "stats_hourlies": &[]model.StatsHourly{}, "stats_api_keys": &[]model.StatsAPIKey{}}
	for table, value := range rows {
		if err := conn.Table(table).Find(value).Error; err != nil {
			return "", err
		}
	}
	sortRows(*rows["stats_totals"].(*[]model.StatsTotal), "ID")
	sortRows(*rows["stats_hourlies"].(*[]model.StatsHourly), "Hour")
	sortRows(*rows["stats_api_keys"].(*[]model.StatsAPIKey), "APIKeyID")
	raw, err := json.Marshal(rows)
	return digest(raw), err
}

func (s *suite) prepareStatisticsUpgrade(dump *model.DBDump) error {
	conn, err := rawDB(s.opts.dialect, s.dsn)
	if err != nil {
		return err
	}
	pool, err := conn.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	return s.step("real-v012-schema-and-preservation-fixture", func() (details any, failure error) {
		phase := "legacy physical schema metadata"
		defer func() {
			if failure != nil {
				failure = errors.Join(&assertionFailure{phase}, failure)
			}
		}()
		metadata, err := businessKeyMetadata(conn)
		if err != nil {
			return nil, err
		}
		if err := assertBusinessGeneration(metadata, true); err != nil {
			return metadata, err
		}
		var maxVersion int
		if err := conn.Table("migration_records").Select("MAX(version)").Scan(&maxVersion).Error; err != nil || maxVersion != 15 {
			return nil, &assertionFailure{"legacy release migration baseline is not 15"}
		}
		phase = "legacy entity fixture"
		categories := make([]model.GroupCategory, 0, len(dump.GroupCategories))
		for _, category := range dump.GroupCategories {
			name, key, err := model.NormalizeGroupCategoryName(category.Name)
			if err != nil {
				return nil, err
			}
			categories = append(categories, model.GroupCategory{ID: category.ID, Name: name, NameKey: key})
		}
		for _, value := range []any{&dump.Channels, &dump.ChannelKeys, &dump.ChannelModels, &dump.ChannelGrants, &categories, &dump.Groups, &dump.GroupItems, &dump.APIKeys, &dump.WebhookEndpoints} {
			if err := conn.Omit(clause.Associations).Create(value).Error; err != nil {
				return nil, err
			}
		}
		apiKey := statisticsAPIKey()
		if err := conn.Create(&apiKey).Error; err != nil {
			return nil, err
		}
		// Preserve even legacy statistics that look suspicious but have legal
		// keys. Migration must never guess a shift, sum, or missing old value.
		phase = "legacy statistics fixture"
		if err := conn.Connection(func(c *gorm.DB) error {
			// Connection supplies a pinned, initialized statement. Start clean
			// statements for each model while retaining that same SQL session.
			c = c.Session(&gorm.Session{NewDB: true})
			if s.opts.dialect == "mysql" {
				var mode string
				if err := c.Raw("SELECT @@SESSION.sql_mode").Scan(&mode).Error; err != nil {
					return err
				}
				if err := c.Exec("SET SESSION sql_mode = ?", mode+",NO_AUTO_VALUE_ON_ZERO").Error; err != nil {
					return err
				}
				defer c.Exec("SET SESSION sql_mode = ?", mode)
			}
			for _, row := range []any{&model.StatsTotal{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 777}}, &model.StatsAPIKey{APIKeyID: 7, StatsMetrics: model.StatsMetrics{InputToken: 333}},
				&model.StatsHourly{Hour: 0, Date: "20261111", StatsMetrics: model.StatsMetrics{InputToken: 100}},
				&model.StatsHourly{Hour: 1, Date: "20261111", StatsMetrics: model.StatsMetrics{InputToken: 200}},
				&model.StatsHourly{Hour: 23, Date: "20261110", StatsMetrics: model.StatsMetrics{InputToken: 999}}} {
				if err := c.Save(row).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
		phase = "legacy secondary indexes"
		for _, key := range businessKeys {
			if err := conn.Exec("CREATE INDEX " + sqlName(conn, "b02_preserve_"+key.table) + " ON " + sqlName(conn, key.table) + " (" + sqlName(conn, key.key) + ")").Error; err != nil {
				return nil, err
			}
		}
		if s.opts.dialect == "postgres" {
			phase = "shared PostgreSQL sequence fixture"
			// Another object deliberately uses this owned sequence. The repair
			// must detach it rather than cascade-drop other defaults.
			var sequenceName string
			if err := conn.Raw("SELECT pg_get_serial_sequence('stats_hourlies','hour')").Scan(&sequenceName).Error; err != nil || sequenceName == "" {
				return nil, errors.New("legacy hourly sequence missing")
			}
			if err := conn.Exec("CREATE TABLE b02_shared_sequence_user (id bigint DEFAULT nextval(" + conn.Dialector.Explain("$1", sequenceName) + "::regclass))").Error; err != nil {
				return nil, err
			}
		}
		phase = "legacy statistics digest"
		stats, err := statisticsSnapshot(conn)
		if err != nil {
			return nil, err
		}
		phase = "legacy entity digest"
		entities, err := entitySnapshot(conn)
		if err != nil {
			return nil, err
		}
		phase = "legacy preservation identity"
		if err := conn.Exec("CREATE TABLE b02_statistics_upgrade_identity (stats_hash varchar(64), entity_hash varchar(64))").Error; err != nil {
			return nil, err
		}
		if err := conn.Exec("INSERT INTO b02_statistics_upgrade_identity VALUES (?,?)", stats, entities).Error; err != nil {
			return nil, err
		}
		return map[string]any{"old_metadata": metadata, "statistics_sha256": stats, "entity_sha256": entities, "migration_max": maxVersion}, nil
	})
}

func (s *suite) verifyStatisticsUpgrade(_ *model.DBDump) error {
	conn, err := rawDB(s.opts.dialect, s.dsn)
	if err != nil {
		return err
	}
	pool, err := conn.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	defer db.Close()
	var identity struct{ StatsHash, EntityHash string }
	if err := conn.Table("b02_statistics_upgrade_identity").Take(&identity).Error; err != nil || len(identity.StatsHash) != 64 || len(identity.EntityHash) != 64 {
		return &assertionFailure{"legacy statistics identity missing"}
	}
	// A third-key metadata fault fails after earlier DDL steps. MySQL's first
	// two steps survive; PostgreSQL/SQLite roll them back. Remove only this
	// synthetic fault before restarting and retrying the recorded migration.
	if err := s.step("migration17-failure-partial-ddl-and-retry", func() (any, error) {
		table := sqlName(conn, "stats_api_keys")
		if err := conn.Exec("ALTER TABLE " + table + " RENAME COLUMN api_key_id TO b02_fault_key").Error; err != nil {
			return nil, err
		}
		initErr := db.InitDB(s.opts.dialect, s.dsn, false)
		if initErr == nil || !strings.Contains(initErr.Error(), "migration 17") {
			return nil, &assertionFailure{"migration 17 did not fail at the synthetic third-key fault"}
		}
		_ = db.Close()
		var record migrate.MigrationRecord
		if err := conn.Where("version=17").Take(&record).Error; err != nil || record.Status != migrate.MigrationRecordStatusFailed {
			return nil, &assertionFailure{"migration failure not recorded"}
		}
		if err := conn.Exec("ALTER TABLE " + table + " RENAME COLUMN b02_fault_key TO api_key_id").Error; err != nil {
			return nil, err
		}
		metadata, err := businessKeyMetadata(conn)
		if err != nil {
			return nil, err
		}
		for _, key := range businessKeys[:2] {
			expectGenerated := s.opts.dialect != "mysql"
			if metadata[key.table].(map[string]any)["generated"] != expectGenerated {
				return nil, &assertionFailure{"DDL rollback/implicit commit differs from its database contract"}
			}
		}
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		return map[string]any{"failed_status": int(record.Status), "after_failure": metadata, "retry": "successful"}, nil
	}); err != nil {
		return err
	}
	if err := s.step("upgrade-preserves-rows-indexes-foreign-keys-records", func() (any, error) {
		stats, err := statisticsSnapshot(db.GetDB())
		if err != nil {
			return nil, err
		}
		entities, err := entitySnapshot(db.GetDB())
		if err != nil {
			return nil, err
		}
		if stats != identity.StatsHash || entities != identity.EntityHash {
			return nil, &assertionFailure{"upgrade changed legacy business rows"}
		}
		var records []migrate.MigrationRecord
		if err := db.GetDB().Order("version").Find(&records).Error; err != nil || len(records) != 15 {
			return nil, &assertionFailure{"upgrade lost or duplicated migration records"}
		}
		for i, record := range records {
			if record.Version != i+3 || record.Status != migrate.MigrationRecordStatusSuccess {
				return nil, &assertionFailure{"old migration history changed"}
			}
		}
		for _, key := range businessKeys {
			if !db.GetDB().Migrator().HasIndex(key.table, "b02_preserve_"+key.table) {
				return nil, &assertionFailure{"legacy secondary index lost"}
			}
		}
		metadata, err := businessKeyMetadata(db.GetDB())
		if err != nil {
			return nil, err
		}
		if err := assertBusinessGeneration(metadata, false); err != nil {
			return metadata, err
		}
		bad := model.GroupItem{ID: 9999999, GroupID: 9999999, ChannelGrantID: 9999999}
		if err := db.GetDB().Create(&bad).Error; err == nil {
			return nil, &assertionFailure{"related entity foreign keys no longer enforced"}
		}
		if s.opts.dialect == "postgres" {
			if err := db.GetDB().Exec("INSERT INTO b02_shared_sequence_user DEFAULT VALUES").Error; err != nil {
				return nil, &assertionFailure{"shared legacy sequence was removed"}
			}
			var shared int64
			if err := db.GetDB().Table("b02_shared_sequence_user").Select("id").Take(&shared).Error; err != nil || shared <= 0 {
				return nil, &assertionFailure{"shared sequence default no longer generates values"}
			}
		}
		if err := db.Close(); err != nil {
			return nil, err
		}
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		restarted, err := statisticsSnapshot(db.GetDB())
		if err != nil || restarted != stats {
			return nil, errors.New("restart changed preexisting hourly slots")
		}
		return map[string]any{"statistics_sha256": stats, "entity_sha256": entities, "metadata": metadata, "records": records}, nil
	}); err != nil {
		return err
	}
	// Keep the original old-date rows until the preservation assertions above
	// complete. Following imports exercise the same fixed keys after upgrade.
	return s.statisticsCases()
}
