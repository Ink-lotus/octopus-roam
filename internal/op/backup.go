package op

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	dumpProduct   = "octopus-roam"
	dbDumpVersion = 1000
)

// 只接受逐表审计过的上游格式。同步上游时不得带入其常量或扩大白名单。
var upstreamDumpVersions = map[int]struct{}{0: {}, 5: {}}
var roamDumpVersions = map[int]struct{}{dbDumpVersion: {}}

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
		return fmt.Errorf("unsupported backup: product %q is not octopus-roam", dump.Product)
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
	return dump, nil
}

// 所有表来自同一快照，避免刷新/删除在导出期间产生不完整的关联数据。
func exportBackupSnapshot(conn *gorm.DB) (*model.DBDump, error) {
	d := &model.DBDump{
		Product:    dumpProduct,
		Version:    dbDumpVersion,
		ExportedAt: time.Now().UTC(),
	}

	if err := conn.Find(&d.Channels).Error; err != nil {
		return nil, fmt.Errorf("export channels: %w", err)
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

	return d, nil
}

func DBImportIncremental(ctx context.Context, dump *model.DBDump) (*model.DBImportResult, error) {
	if err := validateDumpOrigin(dump); err != nil {
		return nil, err
	}
	if err := validatePrivacyImport(dump.Settings); err != nil {
		return nil, err
	}
	for i := range dump.Groups {
		if dump.Groups[i].Mode == "" {
			dump.Groups[i].Mode = model.GroupModeManual
		}
		if err := model.ValidateGroupMode(dump.Groups[i].Mode); err != nil {
			return nil, err
		}
		dump.Groups[i].ModelMatchMode = model.NormalizeGroupModelMatchMode(dump.Groups[i].ModelMatchMode)
		if err := model.ValidateGroupModelMatchMode(dump.Groups[i].ModelMatchMode); err != nil {
			return nil, err
		}
		model.NormalizeGroupRelayConfigForMode(&dump.Groups[i].RelayConfig, dump.Groups[i].Mode)
	}
	var importedDefaults *string
	for i := range dump.Settings {
		if isChannelAutomationSetting(dump.Settings[i].Key) {
			if err := dump.Settings[i].Validate(); err != nil {
				return nil, err
			}
		}
		if dump.Settings[i].Key != model.SettingKeyGroupRouteDefaults {
			continue
		}
		if importedDefaults != nil {
			return nil, fmt.Errorf("duplicate group route defaults in backup")
		}
		config, err := parseGroupRouteDefaults(dump.Settings[i].Value)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, err
		}
		dump.Settings[i].Value = string(raw)
		importedDefaults = &dump.Settings[i].Value
	}

	// 导入也是写入边界, 渠道配置须与接口提交走同一套规范化: 手改过的备份文件同样可能带空白,
	// 空串或缺省字段, 不在此收敛则读侧要为每个字段各自兜底。
	for i := range dump.Channels {
		config, err := normalizeChannelConfig(dump.Channels[i].ChannelConfig)
		if err != nil {
			return nil, fmt.Errorf("import channel %d: %w", dump.Channels[i].ID, err)
		}
		dump.Channels[i].ChannelConfig = config
	}

	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	privacyWriteMu.Lock()
	defer privacyWriteMu.Unlock()
	conn := db.GetDB().WithContext(ctx)
	res := &model.DBImportResult{RowsAffected: map[string]int64{}}
	channels := make([]backupChannelRow, len(dump.Channels))
	for i, row := range dump.Channels {
		channels[i] = backupChannelRow{Channel: row, Enabled: row.Enabled}
	}
	keys := make([]backupChannelKeyRow, len(dump.ChannelKeys))
	for i, row := range dump.ChannelKeys {
		keys[i] = backupChannelKeyRow{ChannelKey: row, Enabled: row.Enabled}
	}
	apiKeys := make([]backupAPIKeyRow, len(dump.APIKeys))
	for i, row := range dump.APIKeys {
		apiKeys[i] = backupAPIKeyRow{APIKey: row, Enabled: row.Enabled}
	}
	var privacyUpdate *privacyImport
	err := conn.Transaction(func(tx *gorm.DB) error {
		var err error
		privacyUpdate, err = preparePrivacyImport(tx, dump.Settings)
		if err != nil {
			return err
		}
		// base tables
		if n, err := createDoNothing(tx, channels); err != nil {
			return fmt.Errorf("import channels: %w", err)
		} else {
			res.RowsAffected["channels"] = n
		}
		// 渠道按主键冲突跳过, 统计需单独覆盖; 凭据与模型走整行覆盖, 统计随行一并导入。
		for _, channel := range dump.Channels {
			if err := tx.Model(&model.Channel{}).
				Where("id = ?", channel.ID).
				Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
				Updates(&channel).Error; err != nil {
				return fmt.Errorf("import channel stats: %w", err)
			}
		}
		if n, err := createDoNothing(tx, dump.Groups); err != nil {
			return fmt.Errorf("import groups: %w", err)
		} else {
			res.RowsAffected["groups"] = n
		}
		if n, err := createUpsertAll(tx, keys, []clause.Column{{Name: "id"}}); err != nil {
			return fmt.Errorf("import channel_keys: %w", err)
		} else {
			res.RowsAffected["channel_keys"] = n
		}
		if n, err := createUpsertAll(tx, dump.ChannelModels, []clause.Column{{Name: "id"}}); err != nil {
			return fmt.Errorf("import channel_models: %w", err)
		} else {
			res.RowsAffected["channel_models"] = n
		}
		if n, err := createUpsertAll(tx, dump.ChannelGrants, []clause.Column{{Name: "id"}}); err != nil {
			return fmt.Errorf("import channel_grants: %w", err)
		} else {
			res.RowsAffected["channel_grants"] = n
		}
		if n, err := createDoNothing(tx, dump.GroupItems); err != nil {
			return fmt.Errorf("import group_items: %w", err)
		} else {
			res.RowsAffected["group_items"] = n
		}
		if n, err := createUpsertAll(tx, dump.LLMInfos, []clause.Column{{Name: "name"}}); err != nil {
			return fmt.Errorf("import llm_infos: %w", err)
		} else {
			res.RowsAffected["llm_infos"] = n
		}
		if n, err := createDoNothing(tx, apiKeys); err != nil {
			return fmt.Errorf("import api_keys: %w", err)
		} else {
			res.RowsAffected["api_keys"] = n
		}
		settings := make([]model.Setting, 0, len(dump.Settings))
		for _, setting := range dump.Settings {
			if !isPrivacySetting(setting.Key) {
				settings = append(settings, setting)
			}
		}
		if n, err := createUpsertSettings(tx, settings); err != nil {
			return fmt.Errorf("import settings: %w", err)
		} else {
			res.RowsAffected["settings"] = n
		}
		if n, err := privacyUpdate.write(tx); err != nil {
			return fmt.Errorf("import privacy settings: %w", err)
		} else {
			res.RowsAffected["settings"] += n
		}
		if n, err := createUpsertAll(tx, dump.WebhookEndpoints, []clause.Column{{Name: "id"}}); err != nil {
			return fmt.Errorf("import webhook_endpoints: %w", err)
		} else {
			res.RowsAffected["webhook_endpoints"] = n
		}

		if n, err := createUpsertAll(tx, dump.StatsTotal, []clause.Column{{Name: "id"}}); err != nil {
			return fmt.Errorf("import stats_total: %w", err)
		} else {
			res.RowsAffected["stats_total"] = n
		}
		if n, err := createUpsertAll(tx, dump.StatsDaily, []clause.Column{{Name: "date"}}); err != nil {
			return fmt.Errorf("import stats_daily: %w", err)
		} else {
			res.RowsAffected["stats_daily"] = n
		}
		if n, err := createUpsertAll(tx, dump.StatsHourly, []clause.Column{{Name: "hour"}}); err != nil {
			return fmt.Errorf("import stats_hourly: %w", err)
		} else {
			res.RowsAffected["stats_hourly"] = n
		}
		if n, err := createUpsertAll(tx, dump.StatsAPIKey, []clause.Column{{Name: "api_key_id"}}); err != nil {
			return fmt.Errorf("import stats_api_key: %w", err)
		} else {
			res.RowsAffected["stats_api_key"] = n
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBackupWrite, err)
	}
	if importedDefaults != nil {
		settingCache.Set(model.SettingKeyGroupRouteDefaults, *importedDefaults)
	}
	privacyUpdate.publish()
	return res, nil
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
