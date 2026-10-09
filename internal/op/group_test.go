package op

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "octopus-roam-op-test")
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

func TestDBImportLegacyGroupDefaultsModelMatchMode(t *testing.T) {
	name := t.Name()
	dump := &model.DBDump{
		Version: 5,
		Groups: []model.Group{{
			Name:        name,
			Mode:        model.GroupModeManual,
			RelayConfig: model.DefaultGroupRelayConfig(),
		}},
	}
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatalf("导入缺少匹配模式的旧备份失败: %v", err)
	}
	t.Cleanup(func() { db.GetDB().Where("name = ?", name).Delete(&model.Group{}) })
	if dump.Groups[0].ModelMatchMode != model.GroupModelMatchModeKeyword {
		t.Fatalf("旧备份归一化匹配模式 = %q, 期望 %q", dump.Groups[0].ModelMatchMode, model.GroupModelMatchModeKeyword)
	}
	var stored model.Group
	if err := db.GetDB().Where("name = ?", name).First(&stored).Error; err != nil {
		t.Fatalf("读取导入分组失败: %v", err)
	}
	if stored.ModelMatchMode != model.GroupModelMatchModeKeyword {
		t.Fatalf("旧备份导入后的数据库匹配模式 = %q, 期望 %q", stored.ModelMatchMode, model.GroupModelMatchModeKeyword)
	}
}

func TestGroupModelMatchModeCreateUpdateValidation(t *testing.T) {
	exact := model.GroupModelMatchModeExact
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:           t.Name() + "-exact",
		ModelMatchMode: exact,
	}, t.Context())
	if err != nil {
		t.Fatalf("创建 exact 分组失败: %v", err)
	}
	if group.ModelMatchMode != exact {
		t.Fatalf("创建后的匹配模式 = %q, 期望 %q", group.ModelMatchMode, exact)
	}
	updated, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{}, t.Context())
	if err != nil {
		t.Fatalf("省略匹配模式的更新失败: %v", err)
	}
	if updated.ModelMatchMode != exact {
		t.Fatalf("省略字段后匹配模式 = %q, 期望保留 %q", updated.ModelMatchMode, exact)
	}
	unknown := model.GroupModelMatchMode("prefix")
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{ModelMatchMode: &unknown}, t.Context()); err == nil {
		t.Fatal("更新接受了未知匹配模式")
	}
	if _, err := GroupCreate(&model.GroupCreateRequest{Name: t.Name() + "-unknown", ModelMatchMode: unknown}, t.Context()); err == nil {
		t.Fatal("创建接受了未知匹配模式")
	}
}

func TestGroupGrantCandidatesHideUnavailableAndRetainExistingMembers(t *testing.T) {
	name := t.Name()
	detail, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    name,
			BaseURL: "http://127.0.0.1",
			Enabled: true,
		},
		Keys:   []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}},
		Models: []string{"model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "model", KeyName: "key", Protocols: model.ProtocolOpenAIResponse}},
	}, t.Context())
	if err != nil {
		t.Fatalf("创建测试渠道失败: %v", err)
	}
	t.Cleanup(func() {
		_ = ChannelDel(detail.ID, t.Context())
	})

	grantID := 0
	for _, candidate := range ChannelGrantCandidates() {
		if candidate.ChannelID == detail.ID && candidate.ModelName == "model" && candidate.KeyName == "key" {
			grantID = candidate.ID
			break
		}
	}
	if grantID == 0 {
		t.Fatal("测试渠道未产生授权候选")
	}
	if !containsGroupGrantCandidate(GroupGrantCandidates(), grantID) {
		t.Fatal("启用渠道和凭据未出现在分组候选")
	}

	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  name + "-group",
		Items: []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, t.Context())
	if err != nil {
		t.Fatalf("创建测试分组失败: %v", err)
	}
	itemID := group.Items[0].ID
	t.Cleanup(func() { _ = GroupDel(group.ID, t.Context()) })

	if err := ChannelEnabled(detail.ID, false, t.Context()); err != nil {
		t.Fatalf("禁用渠道失败: %v", err)
	}
	if containsGroupGrantCandidate(GroupGrantCandidates(), grantID) {
		t.Fatal("禁用渠道仍出现在分组候选")
	}
	snapshot, err := GroupGet(group.ID)
	if err != nil {
		t.Fatalf("读取禁用渠道分组失败: %v", err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].ChannelGrantID != grantID || snapshot.Items[0].Available {
		t.Fatalf("禁用渠道后的分组成员不符合预期: %+v", snapshot.Items)
	}

	requested := []model.GroupItemInput{{ChannelGrantID: grantID}}
	updated, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{Items: &requested}, t.Context())
	if err != nil {
		t.Fatalf("保存含失效渠道成员的分组失败: %v", err)
	}
	if len(updated.Items) != 1 || updated.Items[0].ID != itemID || updated.Items[0].ChannelGrantID != grantID {
		t.Fatalf("保存后失效成员主键未保留: %+v", updated.Items)
	}

	if err := ChannelEnabled(detail.ID, true, t.Context()); err != nil {
		t.Fatalf("重新启用渠道失败: %v", err)
	}
	if !containsGroupGrantCandidate(GroupGrantCandidates(), grantID) {
		t.Fatal("重新启用渠道后候选未恢复")
	}

	loadedDetail, err := ChannelDetailGet(detail.ID)
	if err != nil {
		t.Fatalf("读取测试渠道详情失败: %v", err)
	}
	loadedDetail.Keys[0].Enabled = false
	if _, err := ChannelUpdate(&loadedDetail, t.Context()); err != nil {
		t.Fatalf("禁用测试凭据失败: %v", err)
	}
	if containsGroupGrantCandidate(GroupGrantCandidates(), grantID) {
		t.Fatal("禁用凭据仍出现在分组候选")
	}
	snapshot, err = GroupGet(group.ID)
	if err != nil {
		t.Fatalf("读取禁用凭据分组失败: %v", err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].ID != itemID || snapshot.Items[0].Available {
		t.Fatalf("禁用凭据后的已有成员不符合预期: %+v", snapshot.Items)
	}

	loadedDetail.Keys[0].Enabled = true
	if _, err := ChannelUpdate(&loadedDetail, t.Context()); err != nil {
		t.Fatalf("重新启用测试凭据失败: %v", err)
	}
	if !containsGroupGrantCandidate(GroupGrantCandidates(), grantID) {
		t.Fatal("重新启用凭据后候选未恢复")
	}
}

func containsGroupGrantCandidate(candidates []model.ChannelGrantCandidate, grantID int) bool {
	for _, candidate := range candidates {
		if candidate.ID == grantID {
			return true
		}
	}
	return false
}
