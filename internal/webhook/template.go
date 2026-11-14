package webhook

import (
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/webhook/config"
)

const (
	DefaultTimeoutSeconds = config.DefaultTimeoutSeconds
	MaxTimeoutSeconds     = config.MaxTimeoutSeconds
	DefaultBodyTemplate   = config.DefaultBodyTemplate
)

type Event = config.Event

type SendResult struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

func NormalizeAndValidate(endpoint *model.WebhookEndpoint) error {
	return config.NormalizeAndValidate(endpoint)
}

func Render(bodyTemplate string, event Event) ([]byte, error) {
	return config.Render(bodyTemplate, event)
}

func ValidateRenderedJSON(contentType string, body []byte) error {
	return config.ValidateRenderedJSON(contentType, body)
}
