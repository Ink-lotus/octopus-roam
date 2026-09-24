package handlers

import (
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/runtime").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(getRuntime),
		).
		AddRoute(
			router.NewRoute("/stream", http.MethodGet).
				Handle(streamRuntime),
		)
}

// getRuntime 返回每个分组最新 Relay 遥测的只读汇总。
func getRuntime(c *gin.Context) {
	resp.Success(c, relay.RuntimeSnapshot())
}

// streamRuntime 在连接建立时及每次 Relay 遥测更新后发送完整汇总。
func streamRuntime(c *gin.Context) {
	prepareSSE(c)
	snapshot, updates := relay.OpenRuntimeStream()
	defer relay.CloseRuntimeStream(updates)
	if err := sse.Encode(c.Writer, sse.Event{Event: "runtime", Data: snapshot}); err != nil {
		return
	}
	c.Writer.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case snapshot, ok := <-updates:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: "runtime", Data: snapshot}); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
