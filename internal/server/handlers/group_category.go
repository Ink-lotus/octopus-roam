package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/server/middleware"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/Ink-lotus/octopus-roam/internal/server/router"
	"github.com/gin-gonic/gin"
)

var groupCategoryRoutes *router.GroupRouter

func init() {
	groupCategoryRoutes = router.NewGroupRouter("/api/v1/group-category").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(router.NewRoute("/list", http.MethodGet).Handle(getGroupCategoryList)).
		AddRoute(router.NewRoute("/create", http.MethodPost).Handle(createGroupCategory)).
		AddRoute(router.NewRoute("/update/:id", http.MethodPost).Handle(updateGroupCategory)).
		AddRoute(router.NewRoute("/delete/:id", http.MethodDelete).Handle(deleteGroupCategory))
}

func publishGroupCategories() {
	publishGroupEvent(groupEvent{Name: "categories", Data: op.GroupCategoryList()})
}

func getGroupCategoryList(c *gin.Context) {
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	resp.Success(c, op.GroupCategoryList())
}

func bindGroupCategoryName(c *gin.Context) (string, bool) {
	var input struct {
		Name *string `json:"name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		resp.Error(c, http.StatusBadRequest, "category request must contain only a string name")
		return "", false
	}
	if err := decoder.Decode(new(any)); err != io.EOF || input.Name == nil {
		resp.Error(c, http.StatusBadRequest, "category request must contain one object with a string name")
		return "", false
	}
	return *input.Name, true
}

func groupCategoryID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, "invalid category id")
		return 0, false
	}
	return id, true
}

func groupCategoryError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, model.ErrInvalidGroupCategory):
		status = http.StatusBadRequest
	case errors.Is(err, op.ErrGroupCategoryNotFound):
		status = http.StatusNotFound
	case errors.Is(err, op.ErrGroupCategoryExists):
		status = http.StatusConflict
	}
	resp.Error(c, status, err.Error())
}

func createGroupCategory(c *gin.Context) {
	name, ok := bindGroupCategoryName(c)
	if !ok {
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	category, err := op.GroupCategoryCreate(name, c.Request.Context())
	if err != nil {
		groupCategoryError(c, err)
		return
	}
	publishGroupCategories()
	resp.Success(c, category)
}

func updateGroupCategory(c *gin.Context) {
	id, ok := groupCategoryID(c)
	if !ok {
		return
	}
	name, ok := bindGroupCategoryName(c)
	if !ok {
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	category, err := op.GroupCategoryUpdate(id, name, c.Request.Context())
	if err != nil {
		groupCategoryError(c, err)
		return
	}
	publishGroupCategories()
	resp.Success(c, category)
}

func deleteGroupCategory(c *gin.Context) {
	id, ok := groupCategoryID(c)
	if !ok {
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	result, err := op.GroupCategoryDelete(id, c.Request.Context())
	if err != nil {
		groupCategoryError(c, err)
		return
	}
	publishGroupCategories()
	for _, group := range result.Groups {
		publishGroupSnapshot(group)
	}
	resp.Success(c, result)
}
