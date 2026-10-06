package op

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestChannelAllowedProtocolsApplyToGrantsAndGroups(t *testing.T) {
	name := fmt.Sprintf("%s-%d", t.Name(), channelRefreshFixtureID.Add(1))
	detail, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    name,
			BaseURL: "http://127.0.0.1",
			Enabled: true,
		},
		Keys:   []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}},
		Models: []string{"model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "model", KeyName: "key", Protocols: model.ProtocolAll}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var grantID int
	for _, grant := range channelGrantCache.GetAll() {
		if grant.ChannelModel != nil {
			continue
		}
		channelModel, ok := channelModelCache.Get(grant.ChannelModelID)
		if !ok || channelModel.ChannelID != detail.ID {
			continue
		}
		grantID = grant.ID
		break
	}
	if grantID == 0 {
		t.Fatal("grant not found")
	}
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  name + "-group",
		Items: []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}

	detail.AllowedProtocols = model.ProtocolOpenAIResponse
	updated, err := ChannelUpdate(detail, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Grants) != 1 || updated.Grants[0].Protocols != model.ProtocolOpenAIResponse {
		t.Fatalf("updated grants = %+v", updated.Grants)
	}
	candidates := ChannelGrantCandidates()
	var candidate *model.ChannelGrantCandidate
	for i := range candidates {
		if candidates[i].ID == grantID {
			candidate = &candidates[i]
			break
		}
	}
	if candidate == nil || candidate.Protocols != model.ProtocolOpenAIResponse {
		t.Fatalf("candidate = %+v", candidate)
	}
	snapshot, err := GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].Protocols != model.ProtocolOpenAIResponse || !snapshot.Items[0].Available {
		t.Fatalf("group snapshot = %+v", snapshot.Items)
	}

	detail.AllowedProtocols = model.ProtocolAnthropicMessage
	detail.Grants[0].Protocols = model.ProtocolOpenAIResponse
	updated, err = ChannelUpdate(detail, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Grants) != 0 {
		t.Fatalf("disabled grant was retained: %+v", updated.Grants)
	}
	if _, err := ChannelGrantGet(grantID); err == nil {
		t.Fatal("disabled grant remains usable")
	}
	snapshot, err = GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 0 || snapshot.ActiveItemID != 0 {
		t.Fatalf("group member was not removed with disabled grant: %+v", snapshot)
	}
}

func TestLegacyChannelAllowedProtocolsDefaultToAll(t *testing.T) {
	name := fmt.Sprintf("%s-%d", t.Name(), channelRefreshFixtureID.Add(1))
	if _, err := DBImportIncremental(t.Context(), &model.DBDump{
		Version: 5,
		Channels: []model.Channel{{
			ID: 900000 + int(channelRefreshFixtureID.Load()),
			ChannelConfig: model.ChannelConfig{
				Name:    name,
				BaseURL: "http://127.0.0.1",
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var channel model.Channel
	if err := db.GetDB().Where("name = ?", name).First(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if channel.AllowedProtocols != model.ProtocolAll {
		t.Fatalf("legacy channel allowed protocols = %d, want %d", channel.AllowedProtocols, model.ProtocolAll)
	}
}

func TestRefreshChatOnlyChannelUsesOpenAIModelList(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, `{"data":[{"id":"chat-model"}]}`
	})
	name := fmt.Sprintf("%s-%d", t.Name(), channelRefreshFixtureID.Add(1))
	detail, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:             name,
			BaseURL:          server.URL,
			AllowedProtocols: model.ProtocolOpenAIChatCompletion,
		},
		Keys:   []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}},
		Models: []string{"old-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "old-model", KeyName: "key", Protocols: model.ProtocolOpenAIChatCompletion}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := RefreshChannelModelsWithClient(t.Context(), detail.ID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("chat-only refresh failed: result=%+v err=%v", result, err)
	}
	updated, err := ChannelDetailGet(detail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Models) != 1 || updated.Models[0] != "chat-model" || len(updated.Grants) != 1 || updated.Grants[0].Protocols != model.ProtocolOpenAIChatCompletion {
		t.Fatalf("chat-only refresh result = %+v", updated)
	}
}

func TestRefreshChatOnlyChannelUsesChatPathForModelList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"chat-path-model"}]}`))
	}))
	defer server.Close()

	detail, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:                     fmt.Sprintf("%s-%d", t.Name(), channelRefreshFixtureID.Add(1)),
			BaseURL:                  server.URL,
			AllowedProtocols:         model.ProtocolOpenAIChatCompletion,
			OpenAIChatCompletionPath: "/custom/v1/chat/completions",
			OpenAIResponsePath:       "/v1/responses",
		},
		Keys: []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}

	result, err := RefreshChannelModelsWithClient(t.Context(), detail.ID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success || result.ZeroModels {
		t.Fatalf("chat-only custom path refresh = %+v, err = %v", result, err)
	}
	updated, err := ChannelDetailGet(detail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(updated.Models, []string{"chat-path-model"}) {
		t.Fatalf("chat-only custom path models = %v", updated.Models)
	}
}
