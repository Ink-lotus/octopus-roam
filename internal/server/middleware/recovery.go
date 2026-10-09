package middleware

import (
	"io"
	"net/http"

	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// Recovery keeps Gin's panic and broken-connection handling while redacting
// its diagnostic request dump, which otherwise only masks Authorization.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		redactor := privacy.FromContext(c.Request.Context())
		redactor.AddHeaders(c.Request.Header)
		redactor.AddURL(c.Request.URL.String())
		c.Request = c.Request.WithContext(privacy.WithRedactor(c.Request.Context(), redactor))
		gin.CustomRecoveryWithWriter(redactedLogWriter{output: gin.DefaultErrorWriter, redactor: redactor}, func(c *gin.Context, _ any) {
			resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		})(c)
	}
}

type redactedLogWriter struct {
	output   io.Writer
	redactor *privacy.Redactor
}

func (w redactedLogWriter) Write(raw []byte) (int, error) {
	safe := w.redactor.Text(string(raw))
	n, err := io.WriteString(w.output, safe)
	if err != nil {
		return 0, err
	}
	if n != len(safe) {
		return 0, io.ErrShortWrite
	}
	return len(raw), nil
}
