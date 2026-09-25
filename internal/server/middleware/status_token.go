package middleware

import (
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

const statusTokenIDKey = "status_token_id"

// StatusToken authenticates read-only status routes. Tokens are accepted in
// the standard Bearer header or in a dedicated header for SSE clients.
func StatusToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("X-Octopus-Status-Token")
		if raw == "" {
			authorization := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
				raw = strings.TrimSpace(authorization[len("bearer "):])
			}
		}
		token, err := op.StatusTokenAuthenticate(c.Request.Context(), raw)
		if err != nil {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		c.Set(statusTokenIDKey, token.ID)
		c.Next()
	}
}

func StatusTokenID(c *gin.Context) uint {
	value, ok := c.Get(statusTokenIDKey)
	if !ok {
		return 0
	}
	if id, ok := value.(uint); ok {
		return id
	}
	return 0
}
