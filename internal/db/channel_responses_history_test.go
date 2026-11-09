package db

import (
	"path/filepath"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestInitDBAddsResponsesHistoryColumnDefaultOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "responses-history.db")
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	defer Close()

	if !GetDB().Migrator().HasColumn(&model.Channel{}, "strip_responses_history") {
		t.Fatal("channel table is missing strip_responses_history")
	}
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "responses-history-default", BaseURL: "https://example.invalid"}}
	if err := GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	var stored model.Channel
	if err := GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.StripResponsesHistory {
		t.Fatal("new channel enabled Responses cleanup by default")
	}
}
