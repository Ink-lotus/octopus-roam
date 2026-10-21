package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Ink-lotus/octopus-roam/internal/conf"
	"github.com/Ink-lotus/octopus-roam/internal/server/middleware"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/Ink-lotus/octopus-roam/internal/server/router"
	"github.com/Ink-lotus/octopus-roam/internal/update"
	"github.com/gin-gonic/gin"
)

var runCoreUpdate = update.UpdateCore

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(updateFunc),
		)
}

func latest(c *gin.Context) {
	latestInfo, err := update.GetLatestInfo()
	if err != nil {
		resp.Error(c, http.StatusServiceUnavailable, "unable to check the Octopus Roam release source")
		return
	}
	resp.Success(c, *latestInfo)
}

func getNowVersion(c *gin.Context) {
	resp.Success(c, conf.Version)
}

func updateFunc(c *gin.Context) {
	var request struct {
		TargetVersion string `json:"target_version"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&request); err != nil || request.TargetVersion == "" {
		resp.Error(c, http.StatusBadRequest, update.ErrInvalidUpdateTarget.Error())
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		resp.Error(c, http.StatusBadRequest, update.ErrInvalidUpdateTarget.Error())
		return
	}
	receipt, err := runCoreUpdate(request.TargetVersion)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, update.ErrInvalidUpdateTarget):
			status = http.StatusBadRequest
		case errors.Is(err, update.ErrUpdateTargetChanged), errors.Is(err, update.ErrAlreadyLatest), errors.Is(err, update.ErrDevelopmentBuild), errors.Is(err, update.ErrNoIndependentRelease), errors.Is(err, update.ErrUnsupportedPlatform), errors.Is(err, update.ErrContainerUpdate):
			status = http.StatusConflict
		case errors.Is(err, update.ErrUpdateBusy):
			status = http.StatusTooManyRequests
		case errors.Is(err, update.ErrReleaseCheck):
			status = http.StatusServiceUnavailable
		case errors.Is(err, update.ErrReleaseVerification):
			status = http.StatusBadGateway
		}
		resp.Error(c, status, err.Error())
		return
	}
	resp.Success(c, receipt)
}
