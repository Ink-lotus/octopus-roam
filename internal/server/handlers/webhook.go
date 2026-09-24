package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/webhook"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/webhook").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listWebhookEndpoints),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createWebhookEndpoint),
		).
		AddRoute(
			router.NewRoute("/update/:id", http.MethodPost).
				Handle(updateWebhookEndpoint),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteWebhookEndpoint),
		).
		AddRoute(
			router.NewRoute("/test/:id", http.MethodPost).
				Handle(testWebhookEndpoint),
		)
}

func listWebhookEndpoints(c *gin.Context) {
	endpoints, err := op.WebhookEndpointList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range endpoints {
		endpoints[i] = webhook.MaskEndpoint(endpoints[i])
	}
	resp.Success(c, endpoints)
}

func createWebhookEndpoint(c *gin.Context) {
	var endpoint model.WebhookEndpoint
	if err := c.ShouldBindJSON(&endpoint); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := webhook.NormalizeAndValidate(&endpoint); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	endpoint.ID = 0
	if err := op.WebhookEndpointCreate(c.Request.Context(), &endpoint); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, webhook.MaskEndpoint(endpoint))
}

func updateWebhookEndpoint(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		resp.Error(c, http.StatusBadRequest, "invalid webhook endpoint id")
		return
	}
	current, err := op.WebhookEndpointGet(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	var endpoint model.WebhookEndpoint
	if err := c.ShouldBindJSON(&endpoint); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	webhook.PreserveMaskedSecrets(&endpoint, current)
	if err := webhook.NormalizeAndValidate(&endpoint); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	endpoint.ID = id
	if err := op.WebhookEndpointUpdate(c.Request.Context(), id, &endpoint); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, webhook.MaskEndpoint(endpoint))
}

func deleteWebhookEndpoint(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		resp.Error(c, http.StatusBadRequest, "invalid webhook endpoint id")
		return
	}
	if err := op.WebhookEndpointDelete(c.Request.Context(), id); err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func testWebhookEndpoint(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		resp.Error(c, http.StatusBadRequest, "invalid webhook endpoint id")
		return
	}
	endpoint, err := op.WebhookEndpointGet(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	result := webhook.SendTest(c.Request.Context(), endpoint)
	resp.Success(c, result)
}
