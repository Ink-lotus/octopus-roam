package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
)

func TestChannelSaveAddsMatchingModelsToExistingGroups(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, option := range []string{"enabled", "disabled", "omitted"} {
			t.Run(operation+"/"+option, func(t *testing.T) {
				name := fmt.Sprintf("save-match-%d", channelEventTestID.Add(1))
				exact, err := op.GroupCreate(&model.GroupCreateRequest{Name: name, ModelMatchMode: model.GroupModelMatchModeExact}, t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = op.GroupDel(exact.ID, t.Context()) })
				keyword, err := op.GroupCreate(&model.GroupCreateRequest{Name: name + "-keyword", ModelMatchMode: model.GroupModelMatchModeKeyword}, t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = op.GroupDel(keyword.ID, t.Context()) })
				groupCount := len(op.GroupList())
				request := struct {
					model.ChannelDetail
					AutoAddGroups *bool `json:"auto_add_groups,omitempty"`
				}{ChannelDetail: model.ChannelDetail{
					ChannelConfig: model.ChannelConfig{Name: name, Enabled: true, BaseURL: "http://127.0.0.1"},
					Keys: []model.ChannelKeyConfig{
						{Name: "enabled", Key: "fixture-enabled-key", Enabled: true},
						{Name: "empty", Enabled: true},
					},
					Models: []string{name, name + "-suffix", name + "-keyword-v1"},
				}}
				if operation == "update" {
					request.Keys = append(request.Keys, model.ChannelKeyConfig{Name: "disabled", Key: "fixture-disabled-key", Enabled: false})
				}
				for _, modelName := range request.Models {
					for _, key := range request.Keys {
						request.Grants = append(request.Grants, model.ChannelGrantConfig{ModelName: modelName, KeyName: key.Name, Protocols: model.ProtocolOpenAIResponse})
					}
				}
				if option != "omitted" {
					enabled := option == "enabled"
					request.AutoAddGroups = &enabled
				}
				if operation == "update" {
					created, err := op.ChannelCreate(&request.ChannelDetail, t.Context())
					if err != nil {
						t.Fatal(err)
					}
					request.ID = created.ID
				}
				t.Cleanup(func() {
					if request.ID != 0 {
						_ = op.ChannelDel(request.ID, t.Context())
					}
				})
				subscriber := subscribeGroupEventForTest(t)
				engine := gin.New()
				engine.POST("/create", createChannel)
				engine.POST("/update", updateChannel)
				itemIDs := make(map[int]int)
				for attempt := 0; attempt < 2; attempt++ {
					body, err := json.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					path := "/" + operation
					if attempt > 0 {
						path = "/update"
					}
					httpRequest := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					httpRequest.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, httpRequest)
					if response.Code != http.StatusOK {
						t.Fatalf("save returned %d: %s", response.Code, response.Body.String())
					}
					var saved struct{ Data model.ChannelDetail }
					if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
						t.Fatal(err)
					}
					request.ChannelDetail = saved.Data
					if len(op.GroupList()) != groupCount {
						t.Fatal("save created a group")
					}
					for groupID, modelName := range map[int]string{exact.ID: name, keyword.ID: name + "-keyword-v1"} {
						group, err := op.GroupGet(groupID)
						if err != nil {
							t.Fatal(err)
						}
						if option != "enabled" {
							if len(group.Items) != 0 {
								t.Fatalf("save without auto-add changed group: %+v", group.Items)
							}
							continue
						}
						if len(group.Items) != 1 || group.Items[0].ModelName != modelName || group.Items[0].KeyName != "enabled" || !group.Items[0].Available {
							t.Fatalf("saved models were not matched correctly: %+v", group.Items)
						}
						if attempt > 0 && group.Items[0].ID != itemIDs[groupID] {
							t.Fatal("repeated save replaced an existing member")
						}
						itemIDs[groupID] = group.Items[0].ID
						var stored model.Group
						if err := db.GetDB().Preload("Items").First(&stored, groupID).Error; err != nil || len(stored.Items) != 1 || stored.Items[0].ID != itemIDs[groupID] {
							t.Fatalf("group cache differs from storage: %+v err=%v", stored, err)
						}
					}
					if option == "enabled" {
						seen := make(map[int]bool)
						for range 2 {
							select {
							case event := <-subscriber:
								snapshot, ok := event.Data.(groupResponse)
								if event.Name != "changed" || !ok || itemIDs[snapshot.ID] == 0 || len(snapshot.Items) != 1 || snapshot.Items[0].ID != itemIDs[snapshot.ID] {
									t.Fatalf("save published an incorrect group snapshot: %+v", event)
								}
								seen[snapshot.ID] = true
							default:
								t.Fatal("save did not publish updated groups")
							}
						}
						if len(seen) != 2 {
							t.Fatal("save did not publish both changed groups")
						}
					}
				}
			})
		}
	}
}
