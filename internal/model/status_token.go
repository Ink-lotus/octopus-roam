package model

import "time"

// StatusToken is the persisted metadata for a read-only status token.
// TokenHash is never returned by the API, but is included in database backups
// so an imported token remains usable without storing the plaintext token.
type StatusToken struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	TokenHash string    `gorm:"uniqueIndex;not null" json:"token_hash,omitempty"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	ExpiresAt int64     `gorm:"not null;default:0;index" json:"expires_at"`
	RevokedAt int64     `gorm:"not null;default:0;index" json:"revoked_at"`
}
