package middleware

import (
	"net/http"

	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// Recovery retains Gin's panic and broken-connection handling, including the
// existing diagnostic scope, and returns the ordinary internal-error response.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(gin.DefaultErrorWriter, func(c *gin.Context, _ any) {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
	})
}
