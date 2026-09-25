package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func init() {
	router.NewGroupRouter("/api/v1/status-token").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createStatusToken),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listStatusTokens),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodDelete).
				Handle(revokeStatusToken),
		)
}

type statusTokenCreateRequest struct {
	Name      string          `json:"name"`
	ExpiresAt json.RawMessage `json:"expires_at"`
}

type statusTokenMetadata struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	RevokedAt int64  `json:"revoked_at"`
}

type statusTokenCreateResponse struct {
	Token string              `json:"token"`
	Meta  statusTokenMetadata `json:"metadata"`
}

func createStatusToken(c *gin.Context) {
	var request statusTokenCreateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	expiresAt, err := parseStatusTokenExpiry(request.ExpiresAt)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	token, plaintext, err := op.StatusTokenCreate(c.Request.Context(), request.Name, expiresAt)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, statusTokenCreateResponse{Token: plaintext, Meta: statusTokenMetadataOf(token)})
}

func parseStatusTokenExpiry(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var timestamp int64
	if err := json.Unmarshal(raw, &timestamp); err == nil {
		return timestamp, nil
	}
	var formatted string
	if err := json.Unmarshal(raw, &formatted); err != nil {
		return 0, errors.New("expires_at must be a Unix timestamp or RFC3339 time")
	}
	parsed, err := time.Parse(time.RFC3339, formatted)
	if err != nil {
		return 0, errors.New("expires_at must be a Unix timestamp or RFC3339 time")
	}
	return parsed.Unix(), nil
}

func listStatusTokens(c *gin.Context) {
	tokens, err := op.StatusTokenList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	metadata := make([]statusTokenMetadata, len(tokens))
	for i, token := range tokens {
		metadata[i] = statusTokenMetadataOf(token)
	}
	resp.Success(c, metadata)
}

func revokeStatusToken(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid status token id")
		return
	}
	if err := op.StatusTokenRevoke(c.Request.Context(), uint(id)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Error(c, http.StatusNotFound, resp.ErrResourceNotFound)
			return
		}
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func statusTokenMetadataOf(token model.StatusToken) statusTokenMetadata {
	return statusTokenMetadata{
		ID:        token.ID,
		Name:      token.Name,
		CreatedAt: token.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt: token.ExpiresAt,
		RevokedAt: token.RevokedAt,
	}
}
