package op

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/db/sequence"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	dumpProduct   = "octopus-roam"
	dbDumpVersion = 1004
)

// 只接受逐表审计过的上游格式。同步上游时不得带入其常量或扩大白名单。
var upstreamDumpVersions = map[int]struct{}{0: {}, 5: {}}
var roamDumpVersions = map[int]struct{}{1000: {}, 1001: {}, 1002: {}, 1003: {}, dbDumpVersion: {}, 1005: {}}

var ErrBackupWrite = errors.New("backup could not be imported; no changes were applied")

func validateDumpOrigin(dump *model.DBDump) error {
	if dump == nil {
		return fmt.Errorf("empty dump")
	}
	switch dump.Product {
	case dumpProduct:
		if _, ok := roamDumpVersions[dump.Version]; ok {
			return nil
		}
		return fmt.Errorf("unsupported octopus-roam backup version %d", dump.Version)
	case "":
		if _, ok := upstreamDumpVersions[dump.Version]; ok {
			return nil
		}
		if dump.Version == 6 {
			return fmt.Errorf("unsupported backup: version 6 without product marker is an unsupported pre-release format; open the original database with a current build and re-export")
		}
		return fmt.Errorf("unsupported backup: version %d has no product marker (accepted upstream versions: 0, 5)", dump.Version)
	default:
		return fmt.Errorf("unsupported backup: product is not octopus-roam")
	}
}

// DBExportAll 导出完整数据库内容，包括所有统计数据。
func DBExportAll(ctx context.Context) (*model.DBDump, error) {
	var dump *model.DBDump
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		dump, err = exportBackupSnapshot(tx)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if err := validateStatsPrimaryKeys(dump.StatsTotal, dump.StatsHourly, dump.StatsAPIKey); err != nil {
		return nil, err
	}
	if err := validateHourlyArchives(dump); err != nil {
		return nil, err
	}
	return dump, nil
}

// 所有表来自同一快照，避免刷新/删除在导出期间产生不完整的关联数据。
func exportBackupSnapshot(conn *gorm.DB) (*model.DBDump, error) {
	d := &model.DBDump{
		Product:         dumpProduct,
		Version:         dbDumpVersion,
		ExportedAt:      time.Now().UTC(),
		GroupCategories: []model.BackupGroupCategory{},
	}
	var categories []model.GroupCategory
	if err := conn.Order("id").Find(&categories).Error; err != nil {
		return nil, fmt.Errorf("export group_categories: %w", err)
	}
	for _, category := range categories {
		d.GroupCategories = append(d.GroupCategories, model.BackupGroupCategory{ID: category.ID, Name: category.Name})
	}

	if err := conn.Find(&d.Channels).Error; err != nil {
		return nil, fmt.Errorf("export channels: %w", err)
	}
	for i := range d.Channels {
		rules, err := model.NormalizeCooldownRules(d.Channels[i].CooldownRules)
		if err != nil {
			return nil, fmt.Errorf("export channel %d cooldown rules: %w", d.Channels[i].ID, err)
		}
		d.Channels[i].CooldownRules = rules
	}
	if err := conn.Find(&d.Groups).Error; err != nil {
		return nil, fmt.Errorf("export groups: %w", err)
	}
	if err := conn.Find(&d.ChannelKeys).Error; err != nil {
		return nil, fmt.Errorf("export channel_keys: %w", err)
	}
	if err := conn.Find(&d.ChannelModels).Error; err != nil {
		return nil, fmt.Errorf("export channel_models: %w", err)
	}
	if err := conn.Find(&d.ChannelGrants).Error; err != nil {
		return nil, fmt.Errorf("export channel_grants: %w", err)
	}
	if err := conn.Find(&d.GroupItems).Error; err != nil {
		return nil, fmt.Errorf("export group_items: %w", err)
	}
	if err := conn.Find(&d.LLMInfos).Error; err != nil {
		return nil, fmt.Errorf("export llm_infos: %w", err)
	}
	if err := conn.Find(&d.APIKeys).Error; err != nil {
		return nil, fmt.Errorf("export api_keys: %w", err)
	}
	if err := conn.Find(&d.Settings).Error; err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}
	d.Settings = withoutRetiredSettings(d.Settings)
	if err := conn.Find(&d.WebhookEndpoints).Error; err != nil {
		return nil, fmt.Errorf("export webhook_endpoints: %w", err)
	}

	if err := conn.Find(&d.StatsTotal).Error; err != nil {
		return nil, fmt.Errorf("export stats_total: %w", err)
	}
	if err := conn.Find(&d.StatsDaily).Error; err != nil {
		return nil, fmt.Errorf("export stats_daily: %w", err)
	}
	if err := conn.Find(&d.StatsHourly).Error; err != nil {
		return nil, fmt.Errorf("export stats_hourly: %w", err)
	}
	if err := conn.Find(&d.StatsAPIKey).Error; err != nil {
		return nil, fmt.Errorf("export stats_api_key: %w", err)
	}

	if err := conn.Order("id").Find(&d.StatsHourlyArchives).Error; err != nil {
		return nil, fmt.Errorf("export stats_hourly_archives: %w", err)
	}
	for _, archive := range d.StatsHourlyArchives {
		if archive.Format == 2 {
			d.Version = 1005
		}
	}
	return d, nil
}

func DBImportIncremental(ctx context.Context, dump *model.DBDump) (*model.DBImportResult, error) {
	return DBImportIncrementalAndPublish(ctx, dump, nil)
}

// publish runs after commit and complete cache publication, while channel/group
// writes are still excluded. It may read op snapshots and publish Relay/SSE, but
// must not enter op writes or acquire handler groupMutationMu.
func DBImportIncrementalAndPublish(ctx context.Context, source *model.DBDump, publish func()) (*model.DBImportResult, error) {
	if err := validateDumpOrigin(source); err != nil {
		return nil, &backupError{kind: ErrBackupInput, table: "backup", reason: err.Error()}
	}
	dump := cloneBackupInput(source)
	if err := validateHourlyArchives(dump); err != nil {
		return nil, &backupError{kind: ErrBackupInput, table: "stats_hourly_archives", reason: err.Error()}
	}
	if err := validateStatsPrimaryKeys(dump.StatsTotal, dump.StatsHourly, dump.StatsAPIKey); err != nil {
		return nil, &backupError{kind: ErrBackupInput, table: "statistics", reason: err.Error()}
	}
	if err := validateBackupFeatureFields(dump); err != nil {
		return nil, &backupError{kind: ErrBackupInput, table: "backup", reason: safeBackupFeatureError(err), cause: err}
	}
	dump.Settings = withoutRetiredSettings(dump.Settings)
	if err := validateBackupSettings(dump.Settings); err != nil {
		return nil, backupProblem(ErrBackupInput, "settings", 0, "duplicate setting key")
	}
	for i := range dump.Settings {
		setting := &dump.Settings[i]
		if setting.Key == "" || setting.Validate() != nil {
			return nil, backupProblem(ErrBackupInput, "settings", 0, "invalid setting value")
		}
		if setting.Key == model.SettingKeyGroupRouteDefaults {
			config, err := parseGroupRouteDefaults(setting.Value)
			if err != nil {
				return nil, backupProblem(ErrBackupInput, "settings", 0, "invalid group route defaults")
			}
			raw, _ := json.Marshal(config)
			setting.Value = string(raw)
		}
	}
	if err := normalizeBackupRows(dump); err != nil {
		return nil, err
	}
	if err := validateBackupGraph(dump); err != nil {
		return nil, err
	}
	conn := db.GetDB().WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
	if err := validateBackupKeys(conn, dump); err != nil {
		return nil, err
	}

	var cooldownNotify func()
	defer runCooldownNotification(&cooldownNotify)
	channelRefreshCommitMu.Lock()
	defer channelRefreshCommitMu.Unlock()
	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	backupAuxMu.Lock()
	defer backupAuxMu.Unlock()
	backupStatsMu.Lock()
	defer backupStatsMu.Unlock()
	if backupUnavailable.Load() {
		return nil, ErrBackupCommitted
	}

	result := &model.DBImportResult{RowsAffected: map[string]int64{}}
	options := &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	if conn.Dialector.Name() == "mysql" {
		options.Isolation = sql.LevelRepeatableRead
	}
	var final *model.DBDump
	err := conn.Transaction(func(tx *gorm.DB) error {
		if err := lockBackupTables(tx); err != nil {
			return err
		}
		if err := validateBackupGraph(dump); err != nil {
			return err
		}
		// Local validation and the final graph check use the same locked transaction.
		local, err := exportBackupSnapshot(tx)
		if err != nil {
			return backupDatabaseError("local_snapshot", 0, err)
		}
		if issues := integrity.Inspect(local); len(issues) > 0 {
			i := issues[0]
			return backupProblem(ErrBackupConflict, i.Table, i.ID, i.Code+"; audit the existing database before importing")
		}
		if err := normalizeBackupRows(local); err != nil {
			var problem *backupError
			if errors.As(err, &problem) {
				return backupProblem(ErrBackupConflict, problem.table, problem.id, "invalid persisted configuration: "+problem.reason)
			}
			return backupProblem(ErrBackupConflict, "local_snapshot", 0, "invalid persisted configuration; audit before importing")
		}
		mapping, categories, err := planBackupCategories(local.GroupCategories, dump.GroupCategories)
		if err != nil {
			return backupDatabaseError("group_categories", 0, err)
		}
		dump.Groups, err = mapBackupGroupCategories(dump.Groups, mapping)
		if err != nil {
			return backupProblem(ErrBackupInput, "groups", 0, "invalid category mapping")
		}
		plans, err := planBackupWrites(tx, dump, local)
		if err != nil {
			return err
		}
		if issues := integrity.Inspect(expectedBackupGraph(local, dump, categories)); len(issues) > 0 {
			i := issues[0]
			return backupProblem(ErrBackupConflict, i.Table, i.ID, i.Code)
		}
		if err := flushBackupStats(tx, dump); err != nil {
			return backupDatabaseError("statistics", 0, err)
		}
		for _, category := range categories {
			if err := tx.Omit(clause.Associations).Create(&category).Error; err != nil {
				return backupDatabaseError("group_categories", category.ID, err)
			}
		}
		result.RowsAffected["group_categories"] = int64(len(categories))
		if err := writeBackupRows(tx, plans, result); err != nil {
			return err
		}
		if err := sequence.Sync(tx); err != nil {
			return backupDatabaseError("entity_sequences", 0, err)
		}
		final, err = exportBackupSnapshot(tx)
		if err != nil {
			return backupDatabaseError("final_snapshot", 0, err)
		}
		if issues := integrity.Inspect(final); len(issues) > 0 {
			i := issues[0]
			return backupProblem(ErrBackupWrite, i.Table, i.ID, "final graph failed validation: "+i.Code)
		}
		if err := normalizeBackupRows(final); err != nil {
			return backupProblem(ErrBackupWrite, "final_snapshot", 0, "final configuration failed validation")
		}
		if err := verifyBackupWrites(plans, final, categories); err != nil {
			return err
		}
		return nil
	}, options)
	if err != nil {
		if errors.Is(err, ErrBackupInput) || errors.Is(err, ErrBackupConflict) || errors.Is(err, ErrBackupWrite) {
			return nil, err
		}
		return nil, backupDatabaseError("transaction", 0, err)
	}
	cooldownNotify, err = publishBackupCache(final)
	if err != nil {
		return nil, ErrBackupCommitted
	}
	finishBackupPublication(publish)
	return result, nil
}

func cloneBackupInput(source *model.DBDump) *model.DBDump {
	dump := *source
	v := reflect.ValueOf(&dump).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.CanSet() && field.Kind() == reflect.Slice {
			copy := reflect.MakeSlice(field.Type(), field.Len(), field.Len())
			reflect.Copy(copy, field)
			field.Set(copy)
		}
	}
	return &dump
}

// Feature validators historically include user supplied text in nested errors.
// Preserve useful version/field diagnostics without exposing those values.
func safeBackupFeatureError(err error) string {
	message := err.Error()
	for _, field := range []string{"platform categories", "group_categories", "cooldown_rules", "strip_responses_history", "custom_header_model_only"} {
		if strings.Contains(message, field) {
			return "invalid or unsupported " + field
		}
	}
	return "invalid backup feature configuration"
}

func validateBackupFeatureFields(dump *model.DBDump) error {
	// 按功能首次出现的内容版本校验，后续版本升级不能使已有备份的功能失效。
	hasRulesAndCategories := dump.Product == dumpProduct && dump.Version >= 1001
	hasResponsesHistory := dump.Product == dumpProduct && dump.Version >= 1002
	hasModelOnlyHeaders := dump.Product == dumpProduct && dump.Version >= 1003
	if hasRulesAndCategories && dump.HasExplicitNullCooldownRules() {
		return fmt.Errorf("cooldown_rules cannot be null")
	}
	if hasRulesAndCategories && dump.HasExplicitNullGroupCategories() {
		return fmt.Errorf("group_categories cannot be null")
	}
	for i := range dump.Channels {
		rules, err := model.NormalizeCooldownRules(dump.Channels[i].CooldownRules)
		if err != nil {
			return fmt.Errorf("import channel %d: %w", dump.Channels[i].ID, err)
		}
		if !hasRulesAndCategories && len(rules) > 0 {
			return fmt.Errorf("backup version %d cannot contain cooldown_rules", dump.Version)
		}
		if !hasResponsesHistory && dump.Channels[i].StripResponsesHistory {
			return fmt.Errorf("backup version %d cannot contain strip_responses_history", dump.Version)
		}
		if !hasModelOnlyHeaders && dump.Channels[i].CustomHeaderModelOnly {
			return fmt.Errorf("backup version %d cannot contain custom_header_model_only", dump.Version)
		}
		dump.Channels[i].CooldownRules = rules
	}
	if !hasRulesAndCategories {
		if len(dump.GroupCategories) > 0 || dump.HasNonNullGroupCategoryReference() {
			return fmt.Errorf("backup version %d cannot contain platform categories", dump.Version)
		}
		return nil
	}
	return validateBackupCategories(dump)
}

func validateBackupSettings(settings []model.Setting) error {
	seen := make(map[model.SettingKey]bool, len(settings))
	for _, setting := range settings {
		if seen[setting.Key] {
			return fmt.Errorf("duplicate setting key in backup: %s", setting.Key)
		}
		seen[setting.Key] = true
	}
	return nil
}

// batchSize 控制每次 INSERT 的最大行数。
// 单行字段数较多（如 stats_hourly 含 9 个字段），若一次插入过多行会超过数据库绑定参数上限（SQLite/PostgreSQL 为 65535），按行数分批写入可规避该限制。
const batchSize = 2000

// 备份保存的是明确状态，不能让普通创建接口的 default:true 覆盖 false。
// 仅为导入覆盖该列的 GORM 元数据，其余字段（含 JSON serializer）沿用模型。
type backupChannelRow struct {
	model.Channel
	Enabled bool `gorm:"column:enabled"`
}

func (backupChannelRow) TableName() string { return "channels" }

type backupChannelKeyRow struct {
	model.ChannelKey
	Enabled bool `gorm:"column:enabled"`
}

func (backupChannelKeyRow) TableName() string { return "channel_keys" }

type backupAPIKeyRow struct {
	model.APIKey
	Enabled bool `gorm:"column:enabled"`
}

func (backupAPIKeyRow) TableName() string { return "api_keys" }

func createDoNothing[T any](tx *gorm.DB, rows []T) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, batchSize)
	return result.RowsAffected, result.Error
}

func createUpsertAll[T any](tx *gorm.DB, rows []T, columns []clause.Column) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   columns,
		UpdateAll: true,
	}).CreateInBatches(&rows, batchSize)
	return result.RowsAffected, result.Error
}

func createUpsertSettings(tx *gorm.DB, rows []model.Setting) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&rows)
	return result.RowsAffected, result.Error
}
