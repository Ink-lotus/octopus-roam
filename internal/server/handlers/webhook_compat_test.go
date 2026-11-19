package handlers

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
)

const legacyWebhookTemplate = `{{if eq .Event "failure"}}{"text":{{.LastError | json}}}{{else if eq .Event "recovery"}}{"text":"recovered"}{{else if eq .Event "test"}}{"text":"test"}{{end}}`

func legacyWebhookFixture() model.WebhookEndpoint {
	return model.WebhookEndpoint{
		Name: "legacy fixture", Enabled: true, URL: "https://fixture.example/hook",
		Method: http.MethodPost, ContentType: "application/json", Headers: map[string]string{},
		BodyTemplate: legacyWebhookTemplate, TimeoutSeconds: 10,
	}
}

func TestLegacyWebhookHTTPCreateUpdateAndDisable(t *testing.T) {
	engine := gin.New()
	engine.POST("/create", createWebhookEndpoint)
	engine.POST("/update/:id", updateWebhookEndpoint)
	t.Run("create", func(t *testing.T) {
		created := configurationData[model.WebhookEndpoint](t, serveCooldownJSON(t, engine, http.MethodPost, "/create", legacyWebhookFixture()))
		t.Cleanup(func() { _ = op.WebhookEndpointDelete(context.Background(), created.ID) })
		if !created.Enabled || created.BodyTemplate != legacyWebhookTemplate {
			t.Fatal("create changed the legacy template or enabled state")
		}
	})

	// Seed a configuration saved by the old program, independently of current API validation.
	endpoint := legacyWebhookFixture()
	if err := op.WebhookEndpointCreate(t.Context(), &endpoint); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.WebhookEndpointDelete(context.Background(), endpoint.ID) })
	for _, test := range []struct {
		name    string
		enabled bool
	}{{"update", true}, {"disable", false}} {
		t.Run(test.name, func(t *testing.T) {
			endpoint.Name, endpoint.Enabled = "legacy edited "+test.name, test.enabled
			updated := configurationData[model.WebhookEndpoint](t, serveCooldownJSON(t, engine, http.MethodPost, fmt.Sprintf("/update/%d", endpoint.ID), endpoint))
			stored, err := op.WebhookEndpointGet(t.Context(), endpoint.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Enabled != test.enabled || stored.Enabled != test.enabled || stored.Name != endpoint.Name || stored.BodyTemplate != legacyWebhookTemplate {
				t.Fatal("update did not preserve the legacy template and requested configuration")
			}
		})
	}
}

func TestLegacyWebhookBackupHTTPRestore(t *testing.T) {
	engine := gin.New()
	engine.POST("/import", importDB)
	for _, version := range []int{1000, 1001, 1002, 1003, 1004, 1005} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("version-%d/enabled-%t", version, enabled), func(t *testing.T) {
				endpoint := legacyWebhookFixture()
				endpoint.ID, endpoint.Enabled = 890000+version*2, enabled
				if enabled {
					endpoint.ID++
				}
				t.Cleanup(func() { _ = op.WebhookEndpointDelete(context.Background(), endpoint.ID) })
				dump := &model.DBDump{Product: "octopus-roam", Version: version, WebhookEndpoints: []model.WebhookEndpoint{endpoint}}
				// Replay also exercises validation of the already-restored local snapshot.
				for range 2 {
					backupHTTPImport(t, engine, dump, http.StatusOK)
				}
				stored, err := op.WebhookEndpointGet(t.Context(), endpoint.ID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.Enabled != enabled || stored.Name != endpoint.Name || stored.BodyTemplate != legacyWebhookTemplate {
					t.Fatal("backup restore changed the legacy template or enabled state")
				}
			})
		}
	}
}
