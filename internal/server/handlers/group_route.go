package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

type groupRouteRequest struct {
	ItemID  int    `json:"item_id" binding:"required,gt=0"`
	Version string `json:"version" binding:"required"`
}

func selectGroupRouteItem(c *gin.Context) {
	mutateGroupRoute(c, relay.SelectRouteItem)
}

func releaseGroupRouteCooldown(c *gin.Context) {
	mutateGroupRoute(c, relay.ReleaseRouteCooldown)
}

// Use the same lock order as configuration writes: groupMutationMu -> routeMu.
// Neither action interrupts requests; the log entry point owns round interruption.
func mutateGroupRoute(c *gin.Context, apply func(model.Group, int, string) (relay.RouteState, error)) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, "invalid group id")
		return
	}
	var request groupRouteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	group, err := op.GroupGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	state, err := apply(group, request.ItemID, request.Version)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusConflict, resp.ResponseStruct{
			Code: http.StatusConflict, Message: err.Error(),
			Data: groupResponse{Group: group, Runtime: relay.RouteStateOf(group)},
		})
		return
	}
	resp.Success(c, groupResponse{Group: group, Runtime: state})
}
