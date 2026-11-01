package handlers

import (
	"net/http"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// Old authenticated management routes remain explicit tombstones. No request
// sample or retired rule value is read, parsed or written here.
func privacyRemoved(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(http.StatusGone, resp.ResponseStruct{
		Code:    http.StatusGone,
		Message: model.ErrSettingRetired.Error(),
		Data:    gin.H{"reason": "feature_removed"},
	})
}
