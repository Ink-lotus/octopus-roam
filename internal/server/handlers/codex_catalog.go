package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/codexcatalog"
	"github.com/Ink-lotus/octopus-roam/internal/conf"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/rhttp"
	"github.com/Ink-lotus/octopus-roam/internal/server/middleware"
	"github.com/Ink-lotus/octopus-roam/internal/server/router"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

var catalogService atomic.Pointer[codexcatalog.Service]

func init() {
	router.NewGroupRouter("/codex").
		Use(catalogHeaders, middleware.APIKeyAuth()).
		AddRoute(router.NewRoute("/models", http.MethodGet).
			// Recheck auth after a possibly queued fetch, so revocation during that
			// wait is enforced by the same middleware as all other gateway requests.
			Handle(loadCodexCatalog, middleware.APIKeyAuth(), respondCodexCatalog))
}

// InitCodexCatalog binds restart-only runtime configuration and clears old cache/tasks.
func InitCodexCatalog() {
	var service *codexcatalog.Service
	if conf.AppConfig.CodexCatalog.Enabled {
		useProxy := conf.AppConfig.CodexCatalog.UseProxy
		service = codexcatalog.New(func() (http.RoundTripper, error) {
			var client *http.Client
			var err error
			if useProxy {
				proxyURL, readErr := op.SettingGetString(model.SettingKeyProxyURL)
				if readErr != nil {
					return nil, errors.New("catalog proxy unavailable")
				}
				parsed, parseErr := url.Parse(proxyURL)
				if parseErr != nil || parsed.Hostname() == "" {
					return nil, errors.New("catalog proxy unavailable")
				}
				client, err = rhttp.ProxyForURL(proxyURL)
			} else {
				client, err = rhttp.Direct()
			}
			if err != nil {
				return nil, errors.New("catalog transport unavailable")
			}
			return client.Transport, nil
		})
	}
	if old := catalogService.Swap(service); old != nil {
		old.Close()
	}
}

func CloseCodexCatalog() {
	if old := catalogService.Swap(nil); old != nil {
		old.Close()
	}
}

func catalogHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Next()
}

type catalogRequest struct {
	result  codexcatalog.Result
	started time.Time
}

func loadCodexCatalog(c *gin.Context) {
	started := time.Now()
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	versions := query["client_version"]
	if err != nil || len(versions) != 1 || !codexcatalog.ValidVersion(versions[0]) {
		catalogError(c, &codexcatalog.Error{Status: 400, Code: "invalid_client_version"})
		return
	}
	service := catalogService.Load()
	if service == nil {
		catalogError(c, &codexcatalog.Error{Status: 503, Code: "catalog_disabled"})
		return
	}
	result, err := service.Get(c.Request.Context(), versions[0])
	if err != nil {
		var classified *codexcatalog.Error
		if errors.As(err, &classified) {
			log.Warn("Codex catalog unavailable", "client_version", versions[0],
				"code", classified.Code, "status", classified.Status, "elapsed_ms", time.Since(started).Milliseconds())
		}
		catalogError(c, err)
		return
	}
	c.Set("codex_catalog_response", catalogRequest{result, started})
}

func respondCodexCatalog(c *gin.Context) {
	value, exists := c.Get("codex_catalog_response")
	request, valid := value.(catalogRequest)
	if !exists || !valid {
		catalogError(c, errors.New("catalog response missing"))
		return
	}
	allowed, _ := c.Get("supported_models")
	names, _ := allowed.([]string)
	snapshot := request.result.Snapshot
	body, count, err := snapshot.Filter(op.GroupListModel(), names)
	if err != nil {
		catalogError(c, err)
		return
	}
	log.Info("Codex catalog", "client_version", snapshot.Version(), "tag", "rust-v"+snapshot.Version(),
		"source", snapshot.URL(), "sha256", snapshot.SHA256(), "cache", request.result.Cache,
		"models", snapshot.ModelCount(), "visible_models", count, "elapsed_ms", time.Since(request.started).Milliseconds())
	c.Data(http.StatusOK, "application/json", body)
}

func catalogError(c *gin.Context, err error) {
	classified := &codexcatalog.Error{Status: 500, Code: "catalog_internal_error"}
	var known *codexcatalog.Error
	if errors.As(err, &known) {
		classified = known
	}
	c.AbortWithStatusJSON(classified.Status, gin.H{"error": gin.H{
		"code": classified.Code, "message": classified.Code,
	}})
}
