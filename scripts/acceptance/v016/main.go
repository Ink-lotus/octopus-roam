// Generate synthetic v0.1.6 acceptance inputs without opening a database.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

const contractName = "v016-product-fixtures-1"
const legacyTemplate = `{{if eq .Event "failure"}}{"text":{{.LastError | json}}}{{else if eq .Event "recovery"}}{"text":"recovered"}{{else if eq .Event "test"}}{"text":"test"}{{end}}`
const rawGlobalFilter = `^v016-model-(a|b|preview|retired)$`
const rawChannelFilter = `^v016-model-`
const visualGlobalFilter = `^(?:(?=v016-model-)(?![\s\S]*preview))[\s\S]*$`
const visualChannelFilter = `^(?:(?=v016-model-)(?![\s\S]*retired))[\s\S]*$`

func archives() ([]model.StatsHourlyArchive, error) {
	var result []model.StatsHourlyArchive
	for i, source := range []string{"sqlite-migration-18", "mysql-migration-18", "postgres-migration-18"} {
		values := []any{int64(24 + i), "20261119", nil, int64(777), 0.1, math.Copysign(0, -1), int64(1), int64(2), int64(3)}
		types := []string{"bigint", "longtext", "bigint", "bigint", "double", "double", "bigint", "bigint", "bigint"}
		if i == 2 {
			types[1], types[4], types[5] = "text", "real", "double precision"
			values[4], values[5] = []byte{0x3d, 0xcc, 0xcc, 0xcd}, []byte{0x80, 0, 0, 0, 0, 0, 0, 0}
		}
		cells := make([]model.HourlyArchiveCell, len(values))
		for column, value := range values {
			var err error
			if i == 0 {
				cells[column], err = model.NewHourlyArchiveCell(model.HourlyArchiveColumns()[column], value)
			} else {
				cells[column], err = model.NewServerHourlyArchiveCell(source, model.HourlyArchiveColumns()[column], types[column], value)
			}
			if err != nil {
				return nil, err
			}
		}
		archive, err := model.NewHourlyArchive(source, cells)
		if err != nil {
			return nil, err
		}
		result = append(result, archive)
	}
	return result, nil
}

// Each historical input has its own feature boundary, rather than a relabelled
// 1006 payload. The old product creates the upgrade database through its API.
func fixture(version int, archiveMode string) (*model.DBDump, error) {
	if version < 1000 || version > 1006 {
		return nil, fmt.Errorf("unsupported fixture version")
	}
	follow := false
	d := &model.DBDump{
		Product: "octopus-roam", Version: version, ExportedAt: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		Channels: []model.Channel{{ID: 1, ChannelConfig: model.ChannelConfig{
			Name: "synthetic-v016-channel", Dialect: model.DialectGeneric, Enabled: true,
			BaseURL: "http://127.0.0.1:1", FollowGlobalAutoUpdate: &follow, AllowedProtocols: model.ProtocolOpenAIChatCompletion,
			MatchRegex: rawChannelFilter,
		}}},
		ChannelKeys: []model.ChannelKey{
			{ID: 1, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-a", Key: "synthetic-v016-upstream-1", Enabled: true}},
			{ID: 2, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-b", Key: "synthetic-v016-upstream-2", Enabled: true}},
		},
		ChannelModels: []model.ChannelModel{{ID: 1, ChannelID: 1, Name: "v016-model-a"}, {ID: 2, ChannelID: 1, Name: "v016-model-b"}},
		ChannelGrants: []model.ChannelGrant{
			{ID: 1, ChannelKeyID: 1, ChannelModelID: 1, Protocols: model.ProtocolOpenAIChatCompletion},
			{ID: 2, ChannelKeyID: 2, ChannelModelID: 1, Protocols: model.ProtocolOpenAIChatCompletion},
			{ID: 3, ChannelKeyID: 1, ChannelModelID: 2, Protocols: model.ProtocolOpenAIChatCompletion},
			{ID: 4, ChannelKeyID: 2, ChannelModelID: 2, Protocols: model.ProtocolOpenAIChatCompletion},
		},
		Groups: []model.Group{
			{ID: 1, Name: "v016-a", Mode: model.GroupModeManual, ModelMatchMode: model.GroupModelMatchModeExact, ActiveItemID: 1},
			{ID: 2, Name: "v016-b", Mode: model.GroupModeManual, ModelMatchMode: model.GroupModelMatchModeExact, ActiveItemID: 3},
			{ID: 3, Name: "v016-other-key", Mode: model.GroupModeManual, ModelMatchMode: model.GroupModelMatchModeExact, ActiveItemID: 5},
		},
		GroupItems: []model.GroupItem{
			{ID: 1, GroupID: 1, ChannelGrantID: 1, Priority: 1}, {ID: 2, GroupID: 1, ChannelGrantID: 2, Priority: 2},
			{ID: 3, GroupID: 2, ChannelGrantID: 3, Priority: 1}, {ID: 4, GroupID: 2, ChannelGrantID: 4, Priority: 2},
			{ID: 5, GroupID: 3, ChannelGrantID: 2, Priority: 1},
		},
		APIKeys: []model.APIKey{{ID: 1, Name: "synthetic-v016-client", APIKey: "synthetic-v016-client", Enabled: true, SupportedModels: []string{}}},
		Settings: []model.Setting{
			{Key: model.SettingKeyModelFilter, Value: rawGlobalFilter},
			{Key: model.SettingKeyProxyURL, Value: ""},
			{Key: model.SettingKeyChannelAutoUpdateEnabled, Value: "false"},
			{Key: model.SettingKeyChannelAutoAddGroups, Value: "false"},
			{Key: model.SettingKeyWebhookManualFailureThreshold, Value: "10"},
		},
		WebhookEndpoints: []model.WebhookEndpoint{{ID: 1, Name: "synthetic-v016-legacy", Enabled: true, URL: "http://127.0.0.1:1/hooks/legacy", Method: "POST", ContentType: "application/json", Headers: map[string]string{}, BodyTemplate: legacyTemplate, TimeoutSeconds: 3}},
		StatsTotal:       []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 77}}},
		StatsHourly:      []model.StatsHourly{{Hour: 0, Date: "20261120", StatsMetrics: model.StatsMetrics{InputToken: 7}}},
	}
	for i := range d.Groups {
		d.Groups[i].RelayConfig.ManualMaxAttempts = 1
		model.NormalizeGroupRelayConfigForMode(&d.Groups[i].RelayConfig, d.Groups[i].Mode)
	}
	if version >= 1001 {
		categoryID := 1
		d.GroupCategories = []model.BackupGroupCategory{{ID: 1, Name: "synthetic-v016-category"}}
		d.Groups[0].CategoryID = &categoryID
		d.Channels[0].CooldownRules = []model.CooldownRule{{ErrorCode: "429", Keyword: "v016-quota", CooldownSeconds: 6, Scope: model.CooldownScopeModel}}
	}
	if version >= 1002 {
		d.Channels[0].StripResponsesHistory = true
	}
	if version >= 1003 {
		d.Channels[0].CustomHeaderModelOnly = true
	}
	if version == 1006 {
		d.Channels[0].CooldownRules[0].Scope = model.CooldownScopeKey
		d.Channels[0].CooldownRules[0].NotifyWebhook = true
	}
	if archiveMode != "none" {
		if version < 1004 || (archiveMode != "format1" && archiveMode != "both") || (version == 1004 && archiveMode == "both") {
			return nil, fmt.Errorf("archive mode crosses its content boundary")
		}
		rows, err := archives()
		if err != nil {
			return nil, err
		}
		d.StatsHourlyArchives = rows
		if archiveMode == "format1" {
			d.StatsHourlyArchives = rows[:1]
		}
	}
	if issues := integrity.Inspect(d); len(issues) > 0 {
		return nil, fmt.Errorf("synthetic graph is invalid: %s", issues[0].Code)
	}
	return d, nil
}

func inputs() (map[string][]byte, error) {
	result := map[string][]byte{}
	for version := 1000; version <= 1006; version++ {
		mode := "none"
		if version == 1004 {
			mode = "format1"
		} else if version >= 1005 {
			mode = "both"
		}
		d, err := fixture(version, mode)
		if err != nil {
			return nil, err
		}
		raw, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return nil, err
		}
		result[fmt.Sprintf("dump-%d.json", version)] = append(raw, '\n')
	}
	for name, mode := range map[string]string{"no-archives-1004.json": "none", "format1-1006.json": "format1"} {
		version := 1004
		if mode == "format1" {
			version = 1006
		}
		d, err := fixture(version, mode)
		if err != nil {
			return nil, err
		}
		raw, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return nil, err
		}
		result[name] = append(raw, '\n')
	}
	for _, name := range []string{"key-only-1006.json", "notify-only-1006.json"} {
		d, err := fixture(1006, "both")
		if err != nil {
			return nil, err
		}
		if name == "key-only-1006.json" {
			d.Channels[0].CooldownRules[0].NotifyWebhook = false
		} else {
			d.Channels[0].CooldownRules[0].Scope = model.CooldownScopeModel
		}
		raw, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return nil, err
		}
		result[name] = append(raw, '\n')
	}
	return result, nil
}

func run(output string, check bool) error {
	files, err := inputs()
	if err != nil {
		return err
	}
	sums := map[string]string{}
	for name, raw := range files {
		hash := sha256.Sum256(raw)
		sums[name] = hex.EncodeToString(hash[:])
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"schema": 1, "contract": contractName, "synthetic": true, "files": sums,
		"raw_global_filter": rawGlobalFilter, "raw_channel_filter": rawChannelFilter,
		"visual_global_filter": visualGlobalFilter, "visual_channel_filter": visualChannelFilter,
		"expected_models_after_visual_filters": []string{"v016-model-a", "v016-model-b"},
	}, "", "  ")
	if err != nil {
		return err
	}
	files["manifest.json"] = append(manifest, '\n')
	if !check {
		if err := os.Mkdir(output, 0o700); err != nil {
			return fmt.Errorf("fixture output must be a new directory: %w", err)
		}
	}
	for name, raw := range files {
		path := filepath.Join(output, name)
		if check {
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != string(raw) {
				return fmt.Errorf("fixture differs from the frozen generator: %s", name)
			}
			continue
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(raw)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// inspectBackup authenticates a real encrypted HTTP export without opening a DB.
// Password and ciphertext arrive over stdin; plaintext is returned only to the
// acceptance parent process, which records hashes rather than credentials.
func inspectBackup(input io.Reader, output io.Writer) error {
	const limit = op.MaxBackupRequestBytes + 8192
	raw, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil || len(raw) > limit {
		return fmt.Errorf("invalid or oversized backup inspection request")
	}
	defer clear(raw)
	var request struct {
		Password string          `json:"password"`
		Backup   json.RawMessage `json:"backup"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("invalid backup inspection request")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("backup inspection requires one JSON object")
	}
	defer func() { request.Password = ""; clear(request.Backup) }()
	dump, err := op.DecryptBackup(request.Backup, request.Password)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(dump)
}

func main() {
	output := flag.String("output", "", "new fixture directory; with --check, read an existing directory")
	check := flag.Bool("check", false, "only verify exact synthetic fixture bytes; no application or database")
	inspect := flag.Bool("inspect-backup", false, "authenticate encrypted backup from stdin and return the inner dump; no database")
	flag.Parse()
	if *inspect {
		if *output != "" || *check || flag.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "--inspect-backup cannot be combined with fixture options or positional arguments")
			os.Exit(2)
		}
		if err := inspectBackup(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "--output is required; no positional arguments")
		os.Exit(2)
	}
	if err := run(*output, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("synthetic fixtures verified; actual product acceptance remains not_run")
}
