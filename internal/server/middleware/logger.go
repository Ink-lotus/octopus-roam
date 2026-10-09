package middleware

import (
	"fmt"

	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/gin-gonic/gin"
)

func Logger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(formatRequestLog)
}

func formatRequestLog(params gin.LogFormatterParams) string {
	redactor := privacy.FromContext(params.Request.Context())
	redactor.AddHeaders(params.Request.Header)
	redactor.AddURL(params.Request.URL.String())
	return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %q\n%s",
		params.TimeStamp.Format("2006/01/02 - 15:04:05"), params.StatusCode,
		params.Latency, params.ClientIP, params.Method,
		redactor.Text(params.Path), redactor.Text(params.ErrorMessage))
}
