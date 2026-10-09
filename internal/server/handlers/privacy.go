package handlers

import (
	"errors"
	"io"
	"net/http"

	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func getPrivacyRules(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	view, err := op.PrivacyRulesGet()
	if err != nil {
		privacyError(c, err)
		return
	}
	resp.Success(c, view)
}

func readPrivacyRequest(c *gin.Context) ([]byte, bool) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, privacy.MaxRequestBytes)
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		privacyError(c, err)
		return nil, false
	}
	return data, true
}

func mutatePrivacyRules(c *gin.Context) {
	data, ok := readPrivacyRequest(c)
	if !ok {
		return
	}
	mutation, err := op.ParsePrivacyMutation(data)
	if err != nil {
		privacyError(c, err)
		return
	}
	view, err := op.PrivacyRulesMutate(c.Request.Context(), mutation)
	if err != nil {
		privacyError(c, err)
		return
	}
	resp.Success(c, view)
}

func previewPrivacy(c *gin.Context) {
	data, ok := readPrivacyRequest(c)
	if !ok {
		return
	}
	request, err := privacy.ParsePreviewRequest(data)
	if err != nil {
		privacyError(c, err)
		return
	}
	result, err := op.PrivacyPreview(request)
	if err != nil {
		privacyError(c, err)
		return
	}
	resp.Success(c, result)
}

func privacyError(c *gin.Context, err error) {
	status, message := http.StatusInternalServerError, "privacy operation could not be completed"
	var data any
	var validation *privacy.ValidationError
	var limit *http.MaxBytesError
	switch {
	case errors.Is(err, op.ErrPrivacyConflict):
		status, message = http.StatusConflict, op.ErrPrivacyConflict.Error()
	case errors.Is(err, op.ErrPrivacyNotFound):
		status, message = http.StatusNotFound, op.ErrPrivacyNotFound.Error()
	case errors.As(err, &limit):
		status, message = http.StatusRequestEntityTooLarge, "privacy request exceeds 192 KiB"
	case errors.As(err, &validation):
		status, message, data = http.StatusBadRequest, validation.Error(), validation
		if validation.Code == "request_limit" {
			status = http.StatusRequestEntityTooLarge
		}
		if validation.Code == "not_found" {
			status = http.StatusNotFound
		}
	}
	// Messages contain only validation metadata, never samples or DB errors.
	// Avoid the ordinary error redactor so user rules cannot obscure field IDs.
	c.AbortWithStatusJSON(status, resp.ResponseStruct{Code: status, Message: message, Data: data})
}
