package op

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestCustomHeaderModelOnlyBackupVersionsAndRoundTrip(t *testing.T) {
	for _, version := range []int{1000, 1001, 1002, 1003} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			id := 970000 + version
			headers := []model.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: "fixture-discovery/1"}}
			channel := model.Channel{ID: id, ChannelConfig: model.ChannelConfig{
				Name: t.Name(), BaseURL: "https://example.invalid", CustomHeader: headers,
				CustomHeaderModelOnly: version >= 1003, StripResponsesHistory: version >= 1002,
			}}
			dump := &model.DBDump{Product: dumpProduct, Version: version}
			if version >= 1001 {
				channel.CooldownRules = []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel}}
				dump.GroupCategories = []model.BackupGroupCategory{{ID: id, Name: t.Name()}}
				dump.Groups = []model.Group{{ID: id, Name: t.Name(), CategoryID: &id}}
			}
			dump.Channels = []model.Channel{channel}
			raw, err := EncryptBackup(dump, "fixture-header-backup-password")
			if err != nil {
				t.Fatal(err)
			}
			restored, err := DecryptBackup(raw, "fixture-header-backup-password")
			if err != nil || restored.Version != version {
				t.Fatalf("encrypted round trip changed content version: %v", err)
			}
			t.Cleanup(func() {
				if version >= 1001 {
					_ = GroupDel(id, context.Background())
					_, _ = GroupCategoryDelete(id, context.Background())
				}
				_ = ChannelDel(id, context.Background())
			})
			if _, err := DBImportIncremental(t.Context(), restored); err != nil {
				t.Fatalf("supported backup no longer imports: %v", err)
			}
			if err := InitCache(); err != nil {
				t.Fatal(err)
			}
			stored, err := ChannelDetailGet(id)
			if err != nil || stored.CustomHeaderModelOnly != channel.CustomHeaderModelOnly || stored.StripResponsesHistory != channel.StripResponsesHistory || !reflect.DeepEqual(stored.CustomHeader, headers) {
				t.Fatalf("backup lost channel header configuration: %v", err)
			}
			if version >= 1001 {
				group, err := GroupGet(id)
				if err != nil || group.CategoryID == nil || *group.CategoryID != id || !reflect.DeepEqual(stored.CooldownRules, channel.CooldownRules) {
					t.Fatalf("older backup lost its existing categories or rules: %v", err)
				}
			}
			// 增量恢复遇到已有渠道时，不能覆盖用户后来保存的总开关。
			stored.CustomHeaderModelOnly = !channel.CustomHeaderModelOnly
			if _, err := ChannelUpdateWithOptions(&stored, model.ChannelSaveOptions{CustomHeaderModelOnlyPresent: true}, t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := DBImportIncremental(t.Context(), restored); err != nil {
				t.Fatal(err)
			}
			if err := InitCache(); err != nil {
				t.Fatal(err)
			}
			stored, err = ChannelDetailGet(id)
			if err != nil || stored.CustomHeaderModelOnly == channel.CustomHeaderModelOnly {
				t.Fatalf("incremental import replaced the existing switch: %v", err)
			}
		})
	}
}

func TestCustomHeaderModelOnlyRejectsOlderBackupDeclarations(t *testing.T) {
	for _, version := range []int{1000, 1001, 1002} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			id := 980000 + version
			dump := &model.DBDump{Product: dumpProduct, Version: version, Channels: []model.Channel{{
				ID: id, ChannelConfig: model.ChannelConfig{Name: t.Name(), BaseURL: "https://example.invalid", CustomHeaderModelOnly: true},
			}}}
			if _, err := DBImportIncremental(t.Context(), dump); err == nil || !strings.Contains(err.Error(), "custom_header_model_only") {
				t.Fatalf("older content version accepted a model-only header switch: %v", err)
			}
			var count int64
			if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", id).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("rejected backup wrote data: count=%d err=%v", count, err)
			}
		})
	}
}
