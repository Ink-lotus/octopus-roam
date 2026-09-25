package op

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

const statusTokenPrefix = "oct-status-"

var ErrStatusTokenInvalid = errors.New("status token is invalid or expired")

var (
	statusTokenWatchMu  sync.Mutex
	statusTokenWatchers = make(map[uint]map[chan struct{}]struct{})
)

// StatusTokenCreate creates a high-entropy token and returns the plaintext once.
// Only the hash is persisted in the database.
func StatusTokenCreate(ctx context.Context, name string, expiresAt int64) (model.StatusToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Status token"
	}
	if expiresAt < 0 || (expiresAt > 0 && expiresAt <= time.Now().Unix()) {
		return model.StatusToken{}, "", fmt.Errorf("expires_at must be zero or a future Unix timestamp")
	}

	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return model.StatusToken{}, "", fmt.Errorf("generate status token: %w", err)
	}
	plaintext := statusTokenPrefix + base64.RawURLEncoding.EncodeToString(randomBytes)
	now := time.Now().UTC()
	token := model.StatusToken{
		Name:      name,
		TokenHash: hashStatusToken(plaintext),
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	if err := db.GetDB().WithContext(ctx).Create(&token).Error; err != nil {
		return model.StatusToken{}, "", fmt.Errorf("create status token: %w", err)
	}
	return token, plaintext, nil
}

func StatusTokenList(ctx context.Context) ([]model.StatusToken, error) {
	var tokens []model.StatusToken
	if err := db.GetDB().WithContext(ctx).Order("id ASC").Find(&tokens).Error; err != nil {
		return nil, fmt.Errorf("list status tokens: %w", err)
	}
	return tokens, nil
}

// StatusTokenRevoke invalidates a token while retaining its metadata for audit and backups.
func StatusTokenRevoke(ctx context.Context, id uint) error {
	var token model.StatusToken
	query := db.GetDB().WithContext(ctx).First(&token, id)
	if errors.Is(query.Error, gorm.ErrRecordNotFound) {
		return gorm.ErrRecordNotFound
	}
	if query.Error != nil {
		return fmt.Errorf("find status token: %w", query.Error)
	}
	if token.RevokedAt != 0 {
		return nil
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.StatusToken{}).
		Where("id = ? AND revoked_at = 0", id).
		Update("revoked_at", time.Now().Unix()).Error; err != nil {
		return fmt.Errorf("revoke status token: %w", err)
	}
	notifyStatusTokenRevoked(id)
	return nil
}

// OpenStatusTokenRevocationStream returns a channel that closes when this token
// is revoked. It lets an existing SSE connection stop without waiting for its
// next status event.
func OpenStatusTokenRevocationStream(id uint) chan struct{} {
	stream := make(chan struct{})
	statusTokenWatchMu.Lock()
	watchers := statusTokenWatchers[id]
	if watchers == nil {
		watchers = make(map[chan struct{}]struct{})
		statusTokenWatchers[id] = watchers
	}
	watchers[stream] = struct{}{}
	statusTokenWatchMu.Unlock()
	return stream
}

func CloseStatusTokenRevocationStream(id uint, stream chan struct{}) {
	statusTokenWatchMu.Lock()
	defer statusTokenWatchMu.Unlock()
	if watchers := statusTokenWatchers[id]; watchers != nil {
		if _, ok := watchers[stream]; ok {
			delete(watchers, stream)
			close(stream)
		}
		if len(watchers) == 0 {
			delete(statusTokenWatchers, id)
		}
	}
}

func notifyStatusTokenRevoked(id uint) {
	statusTokenWatchMu.Lock()
	defer statusTokenWatchMu.Unlock()
	for stream := range statusTokenWatchers[id] {
		close(stream)
	}
	delete(statusTokenWatchers, id)
}

// StatusTokenAuthenticate resolves a plaintext token without exposing its hash to callers.
func StatusTokenAuthenticate(ctx context.Context, plaintext string) (model.StatusToken, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return model.StatusToken{}, ErrStatusTokenInvalid
	}
	var token model.StatusToken
	if err := db.GetDB().WithContext(ctx).
		Where("token_hash = ? AND revoked_at = 0", hashStatusToken(plaintext)).
		First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.StatusToken{}, ErrStatusTokenInvalid
		}
		return model.StatusToken{}, fmt.Errorf("authenticate status token: %w", err)
	}
	if token.ExpiresAt > 0 && token.ExpiresAt <= time.Now().Unix() {
		return model.StatusToken{}, ErrStatusTokenInvalid
	}
	return token, nil
}

func StatusTokenValid(ctx context.Context, id uint) bool {
	var token model.StatusToken
	if err := db.GetDB().WithContext(ctx).First(&token, id).Error; err != nil {
		return false
	}
	return token.RevokedAt == 0 && (token.ExpiresAt == 0 || token.ExpiresAt > time.Now().Unix())
}

func hashStatusToken(plaintext string) string {
	digest := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(digest[:])
}
