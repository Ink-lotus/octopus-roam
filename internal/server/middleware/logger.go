package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func Logger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(formatRequestLog)
}

func formatRequestLog(params gin.LogFormatterParams) string {
	return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %q\n%s",
		params.TimeStamp.Format("2006/01/02 - 15:04:05"), params.StatusCode,
		params.Latency, params.ClientIP, params.Method,
		params.Path, params.ErrorMessage)
}
