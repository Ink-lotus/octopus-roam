package op

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

func TestChannelSaveRollsBackWhenGroupAppendFails(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			name := fmt.Sprintf("save-rollback-%d", channelRefreshFixtureID.Add(1))
			group, err := GroupCreate(&model.GroupCreateRequest{Name: name, ModelMatchMode: model.GroupModelMatchModeExact}, t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = GroupDel(group.ID, t.Context()) })
			detail := model.ChannelDetail{
				ChannelConfig: model.ChannelConfig{Name: name, Enabled: true, BaseURL: "http://127.0.0.1"},
				Keys:          []model.ChannelKeyConfig{{Name: "key", Key: "fixture-key", Enabled: true}},
				Models:        []string{"old-model"},
				Grants:        []model.ChannelGrantConfig{{ModelName: "old-model", KeyName: "key", Protocols: model.ProtocolOpenAIResponse}},
			}
			if operation == "update" {
				created, err := ChannelCreate(&detail, t.Context())
				if err != nil {
					t.Fatal(err)
				}
				detail = *created
				t.Cleanup(func() { _ = ChannelDel(created.ID, t.Context()) })
				detail.Name += "-changed"
			}
			detail.Models = append(detail.Models, name)
			detail.Grants = append(detail.Grants, model.ChannelGrantConfig{ModelName: name, KeyName: "key", Protocols: model.ProtocolOpenAIResponse})
			cacheCount := channelCache.Len()
			var storedCount int64
			if err := db.GetDB().Model(&model.Channel{}).Count(&storedCount).Error; err != nil {
				t.Fatal(err)
			}
			callback := "test:fail-channel-save-group"
			if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "group_items" {
					tx.AddError(errors.New("fixture group append failure"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.GetDB().Callback().Create().Remove(callback) })
			save := ChannelCreateWithOptions
			if operation == "update" {
				save = ChannelUpdateWithOptions
			}
			if saved, err := save(&detail, model.ChannelSaveOptions{AutoAddGroups: true}, t.Context()); err == nil || saved != nil {
				t.Fatalf("save accepted failed group append: saved=%+v err=%v", saved, err)
			}
			var afterCount int64
			if err := db.GetDB().Model(&model.Channel{}).Count(&afterCount).Error; err != nil || afterCount != storedCount || channelCache.Len() != cacheCount {
				t.Fatalf("failed save changed channels: count=%d err=%v", afterCount, err)
			}
			var newModelCount int64
			if err := db.GetDB().Model(&model.ChannelModel{}).Where("name = ?", name).Count(&newModelCount).Error; err != nil || newModelCount != 0 {
				t.Fatalf("failed save retained a new model: count=%d err=%v", newModelCount, err)
			}
			if operation == "update" {
				cached, err := ChannelDetailGet(detail.ID)
				if err != nil || cached.Name != name || !slices.Equal(cached.Models, []string{"old-model"}) || len(cached.Grants) != 1 {
					t.Fatalf("failed update changed channel cache: %+v err=%v", cached, err)
				}
				var stored model.Channel
				if err := db.GetDB().First(&stored, detail.ID).Error; err != nil || stored.Name != name {
					t.Fatalf("failed update changed stored channel: %+v err=%v", stored, err)
				}
			}
			updated, err := GroupGet(group.ID)
			if err != nil || len(updated.Items) != 0 {
				t.Fatalf("failed save changed group: %+v err=%v", updated, err)
			}
		})
	}
}
