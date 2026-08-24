// Package apierr provides the shared {"error": {"message", "type", "code"}}
// JSON error-response shape used by the management API handlers, together
// with the common error-type values and send/abort helpers. It depends only
// on gin, so any layer (server modules, middleware, protocol handlers) can
// use it without import cycles.
package apierr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tingly-dev/tingly-box/internal/errkind"
)

// ErrorResponse is the wire envelope: {"error": {...}}.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries the error payload.
type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// Common values for ErrorDetail.Type.
const (
	TypeInvalidRequest = "invalid_request_error"
	TypeValidation     = "validation_error"
	TypeNotFound       = "not_found_error"
	TypeConflict       = "conflict_error"
	TypeInternal       = "internal_error"
	TypeAPI            = "api_error"
)

// Send writes a {"error": {"message", "type"}} JSON response.
func Send(c *gin.Context, status int, err error, errType string) {
	SendMsg(c, status, err.Error(), errType)
}

// SendMsg is Send for call sites that have a message string rather than an
// error value.
func SendMsg(c *gin.Context, status int, msg, errType string) {
	c.JSON(status, ErrorResponse{Error: ErrorDetail{Message: msg, Type: errType}})
}

// Abort sends the error response, registers the error on the gin context so
// logging middleware captures it, and aborts the handler chain. Meant for
// middleware rejections (auth failures, route guards).
func Abort(c *gin.Context, status int, msg, errType string) {
	c.Error(fmt.Errorf("%s: %s", errType, msg)).SetType(gin.ErrorTypePublic) //nolint:errcheck
	SendMsg(c, status, msg, errType)
	c.Abort()
}

// SendBadRequest sends a 400 invalid_request_error — the standard response
// for a request that failed to bind or validate.
func SendBadRequest(c *gin.Context, err error) {
	Send(c, http.StatusBadRequest, err, TypeInvalidRequest)
}

// SendBadRequestMsg is SendBadRequest with a message string.
func SendBadRequestMsg(c *gin.Context, msg string) {
	SendMsg(c, http.StatusBadRequest, msg, TypeInvalidRequest)
}

// SendRequired sends the canonical 400 "<name> is required" response for a
// missing parameter or field.
func SendRequired(c *gin.Context, name string) {
	SendBadRequestMsg(c, name+" is required")
}

// SendNotFound sends a 404 not_found_error.
func SendNotFound(c *gin.Context, err error) {
	Send(c, http.StatusNotFound, err, TypeNotFound)
}

// SendNotFoundMsg is SendNotFound with a message string.
func SendNotFoundMsg(c *gin.Context, msg string) {
	SendMsg(c, http.StatusNotFound, msg, TypeNotFound)
}

// SendInternal sends a 500 internal_error.
func SendInternal(c *gin.Context, err error) {
	Send(c, http.StatusInternalServerError, err, TypeInternal)
}

// SendInternalMsg is SendInternal with a message string.
func SendInternalMsg(c *gin.Context, msg string) {
	SendMsg(c, http.StatusInternalServerError, msg, TypeInternal)
}

// SendStoreError maps a store-layer error to an HTTP response by its
// errkind mark: ErrInvalid → 400 invalid_request_error, ErrNotFound →
// 404 not_found_error, ErrConflict → 409 conflict_error — the store's
// message is shown for all three, so stores must keep marked messages
// caller-safe. An unmarked error is an internal failure: it is registered
// on the gin context for the logging middleware and answered with a
// generic 500, so driver internals never reach the client.
func SendStoreError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errkind.ErrInvalid):
		Send(c, http.StatusBadRequest, err, TypeInvalidRequest)
	case errors.Is(err, errkind.ErrNotFound):
		Send(c, http.StatusNotFound, err, TypeNotFound)
	case errors.Is(err, errkind.ErrConflict):
		Send(c, http.StatusConflict, err, TypeConflict)
	default:
		c.Error(err).SetType(gin.ErrorTypePrivate) //nolint:errcheck
		SendMsg(c, http.StatusInternalServerError, "internal error", TypeInternal)
	}
}
