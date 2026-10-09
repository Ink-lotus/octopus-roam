package handlers

import (
	"net/http"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func getGroupRouteDefaults(c *gin.Context) {
	config, err := op.GroupRouteDefaultsGet()
	if err != nil {
		groupWriteError(c, err)
		return
	}
	resp.Success(c, config)
}

func setGroupRouteDefaults(c *gin.Context) {
	var request struct {
		RelayConfig     *model.GroupRelayConfig `json:"relay_config" binding:"required"`
		ApplyToExisting bool                    `json:"apply_to_existing"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	config, changes, err := op.GroupRouteDefaultsSet(*request.RelayConfig, request.ApplyToExisting, c.Request.Context())
	if err != nil {
		groupWriteError(c, err)
		return
	}
	for _, change := range changes {
		if routeConfigChanged(change.Before, change.After) {
			relay.ResetRouteStateForGroup(change.After)
		} else {
			relay.SetRouteConfig(change.After)
		}
		publishGroupSnapshot(change.After)
	}
	resp.Success(c, struct {
		RelayConfig   model.GroupRelayConfig `json:"relay_config"`
		AppliedGroups int                    `json:"applied_groups"`
	}{RelayConfig: config, AppliedGroups: len(changes)})
}
