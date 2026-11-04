package op

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

var categoryFixtureSequence atomic.Int64

func newGroupCategoryFixture(t *testing.T) (model.Group, model.GroupCategory, int) {
	t.Helper()
	name := fmt.Sprintf("w08-op-%d", categoryFixtureSequence.Add(1))
	category, err := GroupCategoryCreate(name, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = GroupCategoryDelete(category.ID, context.Background()) })
	detail, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: name, Enabled: true, BaseURL: "https://fixture.example",
			CooldownRules: []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 120, Scope: model.CooldownScopeModel}, {Keyword: "quota", CooldownSeconds: 180, Scope: model.CooldownScopeChannel}}},
		Keys:   []model.ChannelKeyConfig{{Name: "a", Key: "fixture-key-a", Enabled: true}, {Name: "b", Key: "fixture-key-b", Enabled: true}},
		Models: []string{"fixture-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "fixture-model", KeyName: "a", Protocols: model.ProtocolAll}, {ModelName: "fixture-model", KeyName: "b", Protocols: model.ProtocolAll}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ChannelDel(detail.ID, context.Background()) })
	items := []model.GroupItemInput{}
	for _, candidate := range ChannelGrantCandidates() {
		if candidate.ChannelID == detail.ID {
			items = append(items, model.GroupItemInput{ChannelGrantID: candidate.ID})
		}
	}
	slices.SortFunc(items, func(a, b model.GroupItemInput) int { return b.ChannelGrantID - a.ChannelGrantID })
	config := model.DefaultGroupRelayConfig()
	config.ManualMaxAttempts = 7
	group, err := GroupCreate(&model.GroupCreateRequest{Name: name, CategoryID: &category.ID, Mode: model.GroupModeManual,
		ModelMatchMode: model.GroupModelMatchModeExact, RelayConfig: config, Items: items}, t.Context())
	if err != nil || len(group.Items) != 2 {
		t.Fatalf("create fixture group: %v", err)
	}
	t.Cleanup(func() { _ = GroupDel(group.ID, context.Background()) })
	selected := group.Items[1].ID
	group, err = GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &selected}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return *group, *category, detail.ID
}

func assignCategory(id *int) *model.GroupUpdateRequest {
	return &model.GroupUpdateRequest{CategoryID: model.GroupCategoryReference{Present: true, ID: id}}
}

func TestGroupCategoryCRUDAndNormalizedNames(t *testing.T) {
	before := GroupCategoryList()
	name := fmt.Sprintf("W08 Empty %d", categoryFixtureSequence.Add(1))
	category, err := GroupCategoryCreate("  "+name+"  ", t.Context())
	if err != nil || category.Name != name {
		t.Fatalf("empty category create: %v", err)
	}
	t.Cleanup(func() { _, _ = GroupCategoryDelete(category.ID, context.Background()) })
	created := GroupCategoryList()
	if created.Version == before.Version || !slices.ContainsFunc(created.Items, func(item model.GroupCategory) bool { return item.ID == category.ID }) {
		t.Fatal("empty category missing from versioned list")
	}
	if _, err := GroupCategoryCreate(strings.ToLower(name), t.Context()); !errors.Is(err, ErrGroupCategoryExists) {
		t.Fatalf("duplicate normalized name accepted: %v", err)
	}
	if GroupCategoryList().Version != created.Version {
		t.Fatal("rejected create advanced list version")
	}
	renamed, err := GroupCategoryUpdate(category.ID, " "+name+" Renamed ", t.Context())
	if err != nil || renamed.ID != category.ID || renamed.Name != name+" Renamed" {
		t.Fatalf("rename changed stable ID: %v", err)
	}
	version := GroupCategoryList().Version
	if version == created.Version {
		t.Fatal("rename did not advance list version")
	}
	if _, err := GroupCategoryUpdate(category.ID, renamed.Name, t.Context()); err != nil || GroupCategoryList().Version != version {
		t.Fatalf("identical rename was not idempotent: %v", err)
	}
	for _, invalid := range []string{"", "  ", strings.Repeat("类", 65), "a\x00b", "a\nb"} {
		if _, err := GroupCategoryUpdate(category.ID, invalid, t.Context()); !errors.Is(err, model.ErrInvalidGroupCategory) {
			t.Fatalf("invalid rename accepted: %v", err)
		}
	}
	deleted, err := GroupCategoryDelete(category.ID, t.Context())
	if err != nil || deleted.ID != category.ID || deleted.UncategorizedGroupIDs == nil || len(deleted.UncategorizedGroupIDs) != 0 {
		t.Fatalf("empty category deletion: %+v, %v", deleted, err)
	}
	if _, err := GroupCategoryDelete(category.ID, t.Context()); !errors.Is(err, ErrGroupCategoryNotFound) {
		t.Fatalf("missing category delete: %v", err)
	}
	if _, err := GroupCategoryUpdate(category.ID, name, t.Context()); !errors.Is(err, ErrGroupCategoryNotFound) {
		t.Fatalf("missing category update: %v", err)
	}
}

func TestGroupCategoryThreeStatesAndCacheCopies(t *testing.T) {
	group, category, _ := newGroupCategoryFixture(t)
	unchanged, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{}, t.Context())
	if err != nil || !equalCategoryID(unchanged.CategoryID, group.CategoryID) || unchanged.MetadataVersion != group.MetadataVersion {
		t.Fatalf("omitted assignment changed category or version: %v", err)
	}
	for _, id := range []int{0, -1, 999999999} {
		if _, err := GroupUpdate(group.ID, assignCategory(&id), t.Context()); !errors.Is(err, model.ErrInvalidGroupCategory) {
			t.Fatalf("invalid update category accepted: %v", err)
		}
		if _, err := GroupCreate(&model.GroupCreateRequest{Name: fmt.Sprintf("invalid-category-%d", id), CategoryID: &id}, t.Context()); !errors.Is(err, model.ErrInvalidGroupCategory) {
			t.Fatalf("invalid create category accepted: %v", err)
		}
	}
	cached, _ := GroupGet(group.ID)
	if cached.MetadataVersion != group.MetadataVersion || !equalCategoryID(cached.CategoryID, group.CategoryID) {
		t.Fatal("rejected update changed cached assignment")
	}
	*cached.CategoryID = 999999
	cached.Items[0].Priority = 999999
	list := GroupCategoryList()
	for i := range list.Items {
		list.Items[i].Name = "mutated copy"
	}
	cached, _ = GroupGet(group.ID)
	if *cached.CategoryID != category.ID || cached.Items[0].Priority != group.Items[0].Priority || slices.ContainsFunc(GroupCategoryList().Items, func(c model.GroupCategory) bool { return c.Name == "mutated copy" }) {
		t.Fatal("a read copy mutated cache storage")
	}
	cleared, err := GroupUpdate(group.ID, assignCategory(nil), t.Context())
	if err != nil || cleared.CategoryID != nil || cleared.MetadataVersion == group.MetadataVersion {
		t.Fatalf("null did not clear assignment: %v", err)
	}
	restored, err := GroupUpdate(group.ID, assignCategory(&category.ID), t.Context())
	if err != nil || restored.CategoryID == nil || *restored.CategoryID != category.ID || restored.MetadataVersion == cleared.MetadataVersion {
		t.Fatalf("positive ID did not assign category: %v", err)
	}
	if restored.ActiveItemID != group.ActiveItemID || !reflect.DeepEqual(restored.Items, group.Items) || restored.RelayConfig != group.RelayConfig || restored.ModelMatchMode != group.ModelMatchMode {
		t.Fatal("category update changed routing data")
	}
	var stored model.Group
	if err := db.GetDB().First(&stored, group.ID).Error; err != nil || stored.CategoryID == nil || *stored.CategoryID != category.ID {
		t.Fatalf("assignment not persisted: %v", err)
	}
	unassigned, err := GroupCreate(&model.GroupCreateRequest{Name: fmt.Sprintf("unassigned-%d", category.ID)}, t.Context())
	if err != nil || unassigned.CategoryID != nil || unassigned.MetadataVersion == "" {
		t.Fatalf("new group defaults: %v", err)
	}
	t.Cleanup(func() { _ = GroupDel(unassigned.ID, context.Background()) })
}

func TestGroupCategoryDeleteAtomicity(t *testing.T) {
	for _, stage := range []string{"clear", "delete"} {
		t.Run(stage, func(t *testing.T) {
			group, category, _ := newGroupCategoryFixture(t)
			before := GroupCategoryList()
			trigger := fmt.Sprintf(`CREATE TRIGGER w08_category_failure BEFORE DELETE ON group_categories WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'fixture category deletion failure'); END`, category.ID)
			if stage == "clear" {
				trigger = fmt.Sprintf(`CREATE TRIGGER w08_category_failure BEFORE UPDATE OF category_id ON groups WHEN OLD.category_id = %d AND NEW.category_id IS NULL BEGIN SELECT RAISE(ABORT, 'fixture assignment failure'); END`, category.ID)
			}
			if err := db.GetDB().Exec(trigger).Error; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.GetDB().Exec(`DROP TRIGGER IF EXISTS w08_category_failure`) })
			if _, err := GroupCategoryDelete(category.ID, t.Context()); err == nil {
				t.Fatal("injected deletion failure was ignored")
			}
			var persisted model.Group
			if err := db.GetDB().Preload("Items").First(&persisted, group.ID).Error; err != nil || !equalCategoryID(persisted.CategoryID, &category.ID) || len(persisted.Items) != 2 || persisted.ActiveItemID != group.ActiveItemID {
				t.Fatalf("failed transaction changed group: %v", err)
			}
			cached, _ := GroupGet(group.ID)
			if !reflect.DeepEqual(cached, group) || !reflect.DeepEqual(GroupCategoryList(), before) {
				t.Fatal("failed transaction changed caches or versions")
			}
			if err := db.GetDB().Exec(`DROP TRIGGER w08_category_failure`).Error; err != nil {
				t.Fatal(err)
			}
			result, err := GroupCategoryDelete(category.ID, t.Context())
			if err != nil || !slices.Equal(result.UncategorizedGroupIDs, []int{group.ID}) || len(result.Groups) != 1 || result.Groups[0].CategoryID != nil {
				t.Fatalf("committed deletion missing changed group: %+v, %v", result, err)
			}
			cached, _ = GroupGet(group.ID)
			if cached.CategoryID != nil || cached.MetadataVersion == group.MetadataVersion || cached.ActiveItemID != group.ActiveItemID || !reflect.DeepEqual(cached.Items, group.Items) || cached.RelayConfig != group.RelayConfig {
				t.Fatal("category deletion changed members, selection or configuration")
			}
			if err := db.GetDB().First(&persisted, group.ID).Error; err != nil || persisted.CategoryID != nil {
				t.Fatalf("category deletion did not persist NULL: %v", err)
			}
		})
	}
}

func TestGroupCategoryMetadataSurvivesCacheReloads(t *testing.T) {
	group, category, channelID := newGroupCategoryFixture(t)
	before := GroupCategoryList()
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	if err := ChannelEnabled(channelID, false, t.Context()); err != nil {
		t.Fatal(err)
	}
	oldDefaults, err := GroupRouteDefaultsGet()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _, _ = GroupRouteDefaultsSet(oldDefaults, false, context.Background()) })
	if _, _, err := GroupRouteDefaultsSet(oldDefaults, true, t.Context()); err != nil {
		t.Fatal(err)
	}
	cached, _ := GroupGet(group.ID)
	if !equalCategoryID(cached.CategoryID, &category.ID) || cached.MetadataVersion != group.MetadataVersion || !reflect.DeepEqual(GroupCategoryList(), before) {
		t.Fatal("non-category reload lost assignment or advanced metadata version")
	}
}

func TestGroupCategoryConcurrentAssignmentDeletionAndCopies(t *testing.T) {
	group, category, _ := newGroupCategoryFixture(t)
	var wg sync.WaitGroup
	var versions sync.Map
	errCh := make(chan error, 4)
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 24 {
				switch worker {
				case 0:
					id := &category.ID
					if i%2 == 0 {
						id = nil
					}
					if _, err := GroupUpdate(group.ID, assignCategory(id), t.Context()); err != nil {
						errCh <- err
						return
					}
				case 1:
					if _, err := GroupCategoryUpdate(category.ID, fmt.Sprintf("fixture-concurrent-%d-%d", category.ID, i), t.Context()); err != nil {
						errCh <- err
						return
					}
				default:
					value, err := GroupGet(group.ID)
					if err != nil {
						errCh <- err
						return
					}
					id := 0
					if value.CategoryID != nil {
						id = *value.CategoryID
						*value.CategoryID = -1 // 读副本不能破坏正在保存的关联。
					}
					if prior, loaded := versions.LoadOrStore(value.MetadataVersion, id); loaded && prior.(int) != id {
						errCh <- fmt.Errorf("same metadata version described two assignments")
						return
					}
					list := GroupCategoryList()
					for j := range list.Items {
						list.Items[j].Name = "read-copy"
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := GroupUpdate(group.ID, assignCategory(&category.ID), t.Context())
			if err != nil && !errors.Is(err, model.ErrInvalidGroupCategory) {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if _, err := GroupCategoryDelete(category.ID, t.Context()); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	current, err := GroupGet(group.ID)
	if err != nil || current.CategoryID != nil || current.ActiveItemID != group.ActiveItemID || !reflect.DeepEqual(current.Items, group.Items) {
		t.Fatalf("concurrent delete left an orphan or changed members: %v", err)
	}
	var invalid int64
	if err := db.GetDB().Table("groups").Where("category_id IS NOT NULL AND category_id NOT IN (SELECT id FROM group_categories)").Count(&invalid).Error; err != nil || invalid != 0 {
		t.Fatalf("concurrent writes left orphan assignments: %d, %v", invalid, err)
	}
}
