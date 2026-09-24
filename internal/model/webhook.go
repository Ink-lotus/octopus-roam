package model

// WebhookEndpoint 是一个可配置的通用 HTTP 通知目标。
type WebhookEndpoint struct {
	ID             int               `json:"id" gorm:"primaryKey"`
	Name           string            `json:"name" gorm:"not null"`
	Enabled        bool              `json:"enabled" gorm:"not null"`
	URL            string            `json:"url" gorm:"not null"`
	Method         string            `json:"method" gorm:"not null;default:POST"`
	ContentType    string            `json:"content_type" gorm:"not null;default:application/json"`
	Headers        map[string]string `json:"headers" gorm:"serializer:json;not null"`
	BodyTemplate   string            `json:"body_template" gorm:"type:text;not null"`
	TimeoutSeconds int               `json:"timeout_seconds" gorm:"not null;default:10"`
}
