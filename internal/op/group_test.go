package op

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "octopus-op-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := db.InitDB("sqlite", filepath.Join(dir, "test.db"), false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := InitCache(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = db.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestGroupWritesValidateModeAndRetainInactiveConfig(t *testing.T) {
	invalidMode := model.GroupMode("invalid")
	if _, err := GroupCreate(&model.GroupCreateRequest{Name: t.Name() + "invalid", Mode: invalidMode}, t.Context()); err == nil {
		t.Fatal("op 写入了非法模式")
	}
	config := model.DefaultGroupRelayConfig()
	config.MemberMaxAttempts, config.MemberCooldownSeconds = -2, -5
	config.MemberAffinitySeconds = 300
	group, err := GroupCreate(&model.GroupCreateRequest{Name: t.Name(), Mode: model.GroupModeManual, RelayConfig: config}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if group.RelayConfig.MemberCooldownSeconds != -5 || group.RelayConfig.MemberAffinitySeconds != 0 {
		t.Fatal("手动配置保存未正确保留非生效参数并清零 affinity")
	}
	partial := model.GroupRelayConfig{ManualMaxAttempts: 3}
	group, err = GroupUpdate(group.ID, &model.GroupUpdateRequest{RelayConfig: &partial}, t.Context())
	if err != nil || group.RelayConfig.MemberCooldownSeconds != -5 || group.RelayConfig.MemberMaxAttempts != -2 {
		t.Fatal("更新手动策略丢失了非当前模式的已有参数")
	}
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{Mode: &invalidMode}, t.Context()); err == nil {
		t.Fatal("更新写入了非法模式")
	}
	mode := model.GroupModeFailover
	group, err = GroupUpdate(group.ID, &model.GroupUpdateRequest{Mode: &mode}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if group.Mode != mode || group.RelayConfig.MemberCooldownSeconds < 1 || group.RelayConfig.MemberMaxAttempts < 1 {
		t.Fatal("切换模式未归一化历史配置")
	}
	config = group.RelayConfig
	config.MemberCooldownSeconds = -1
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{RelayConfig: &config}, t.Context()); err == nil {
		t.Fatal("故障转移模式接受非法冷却")
	}
	var persisted model.Group
	if err := db.GetDB().First(&persisted, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.RelayConfig.MemberAffinitySeconds != 0 || persisted.RelayConfig.MemberCooldownSeconds < 1 {
		t.Fatal("失败更新污染了数据库或 affinity 未清零")
	}
}
