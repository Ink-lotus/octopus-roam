package op

import (
	"context"
	"fmt"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func WebhookEndpointList(ctx context.Context) ([]model.WebhookEndpoint, error) {
	var endpoints []model.WebhookEndpoint
	if err := db.GetDB().WithContext(ctx).Order("id ASC").Find(&endpoints).Error; err != nil {
		return nil, fmt.Errorf("failed to list webhook endpoints: %w", err)
	}
	if endpoints == nil {
		endpoints = []model.WebhookEndpoint{}
	}
	return endpoints, nil
}

func WebhookEndpointGet(ctx context.Context, id int) (model.WebhookEndpoint, error) {
	var endpoint model.WebhookEndpoint
	if err := db.GetDB().WithContext(ctx).First(&endpoint, id).Error; err != nil {
		return model.WebhookEndpoint{}, fmt.Errorf("webhook endpoint not found: %w", err)
	}
	return endpoint, nil
}

func WebhookEndpointCreate(ctx context.Context, endpoint *model.WebhookEndpoint) error {
	if err := db.GetDB().WithContext(ctx).Create(endpoint).Error; err != nil {
		return fmt.Errorf("failed to create webhook endpoint: %w", err)
	}
	return nil
}

func WebhookEndpointUpdate(ctx context.Context, id int, endpoint *model.WebhookEndpoint) error {
	result := db.GetDB().WithContext(ctx).Model(&model.WebhookEndpoint{}).Where("id = ?", id).
		Select("Name", "Enabled", "URL", "Method", "ContentType", "Headers", "BodyTemplate", "TimeoutSeconds").
		Updates(endpoint)
	if result.Error != nil {
		return fmt.Errorf("failed to update webhook endpoint: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("webhook endpoint not found")
	}
	return nil
}

func WebhookEndpointDelete(ctx context.Context, id int) error {
	result := db.GetDB().WithContext(ctx).Delete(&model.WebhookEndpoint{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete webhook endpoint: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("webhook endpoint not found")
	}
	return nil
}

func WebhookEndpointListEnabled(ctx context.Context) ([]model.WebhookEndpoint, error) {
	var endpoints []model.WebhookEndpoint
	if err := db.GetDB().WithContext(ctx).Where("enabled = ?", true).Order("id ASC").Find(&endpoints).Error; err != nil {
		return nil, fmt.Errorf("failed to list enabled webhook endpoints: %w", err)
	}
	return endpoints, nil
}
