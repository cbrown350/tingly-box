package protocol

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gin-gonic/gin"
	"github.com/openai/openai-go/v3"
	"google.golang.org/genai"
)

// Error-origin marking. Every error response on the protocol surface
// carries X-Tingly-Error so clients can tell whether the failure was
// produced by the gateway itself or relayed from the upstream provider —
// in particular, whether a 401 means "your gateway token" (gateway) or
// "your provider key" (upstream).
const (
	HeaderErrorOrigin = "X-Tingly-Error"
	OriginGateway     = "gateway"
	OriginUpstream    = "upstream"
)

// ctxKeyClientStyle stores the inbound API style (which protocol the
// *client* speaks) on the gin context, set per route by WithClientStyle.
const ctxKeyClientStyle = "tingly.client_style"

// WithClientStyle returns a middleware that records which protocol the
// client on this route speaks, so error writers can answer in the
// client's own error shape.
func WithClientStyle(style APIStyle) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctxKeyClientStyle, style)
		c.Next()
	}
}

// ClientStyleOf returns the inbound API style recorded by WithClientStyle.
func ClientStyleOf(c *gin.Context) (APIStyle, bool) {
	v, ok := c.Get(ctxKeyClientStyle)
	if !ok {
		return "", false
	}
	style, ok := v.(APIStyle)
	return style, ok
}

// MarkGatewayError stamps the response as a gateway-produced error. The
// shared error senders call it; the header is buffered by the failover
// gate like any other header, so it survives retries.
func MarkGatewayError(c *gin.Context) {
	c.Header(HeaderErrorOrigin, OriginGateway)
}

// UpstreamError is the provider failure extracted from a vendor SDK
// error: the HTTP status the provider returned, the unmodified response
// body when the SDK kept it, and the parsed type/message for rebuilding
// the error in another protocol's shape.
type UpstreamError struct {
	Status  int
	Vendor  APIStyle
	RawJSON []byte
	Type    string
	Message string
}

// ExtractUpstreamError pulls the upstream HTTP failure out of err.
// It reports false when err carries no upstream response (transport
// failures, gateway-internal errors) — those are the gateway's own
// errors to shape.
func ExtractUpstreamError(err error) (*UpstreamError, bool) {
	if err == nil {
		return nil, false
	}

	var oaiErr *openai.Error
	if errors.As(err, &oaiErr) && oaiErr.StatusCode >= 400 {
		// The openai SDK stores only the inner error object (its client
		// unwraps the {"error": ...} envelope before unmarshalling), so
		// rebuild the envelope for byte-level passthrough.
		var raw []byte
		if inner := []byte(oaiErr.RawJSON()); len(inner) > 0 && json.Valid(inner) {
			raw = append([]byte(`{"error":`), inner...)
			raw = append(raw, '}')
		}
		return &UpstreamError{
			Status:  oaiErr.StatusCode,
			Vendor:  APIStyleOpenAI,
			RawJSON: raw,
			Type:    oaiErr.Type,
			Message: oaiErr.Message,
		}, true
	}

	var antErr *anthropic.Error
	if errors.As(err, &antErr) && antErr.StatusCode >= 400 {
		up := &UpstreamError{
			Status:  antErr.StatusCode,
			Vendor:  APIStyleAnthropic,
			RawJSON: []byte(antErr.RawJSON()),
			Type:    string(antErr.Type()),
		}
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(up.RawJSON, &envelope) == nil {
			up.Message = envelope.Error.Message
		}
		return up, true
	}

	var genaiErr genai.APIError
	if errors.As(err, &genaiErr) && genaiErr.Code >= 400 {
		return &UpstreamError{
			Status:  genaiErr.Code,
			Vendor:  APIStyleGoogle,
			Type:    genaiErr.Status,
			Message: genaiErr.Message,
		}, true
	}

	return nil, false
}

// WriteUpstreamError relays an upstream provider failure to the client
// and reports whether it did. The status is always the provider's own
// (the failover orchestrator keys its retry decision on it, exactly as
// with the previous wrapped responses). The body is the provider's
// unmodified bytes when the client speaks the provider's protocol, and
// a rebuilt error in the client's shape otherwise; either way the
// response is marked X-Tingly-Error: upstream. Returns false when err
// carries no upstream response, leaving the caller to send its own
// gateway-shaped error.
func WriteUpstreamError(c *gin.Context, err error) bool {
	up, ok := ExtractUpstreamError(err)
	if !ok {
		return false
	}

	c.Header(HeaderErrorOrigin, OriginUpstream)
	style, _ := ClientStyleOf(c)

	if style == up.Vendor && len(up.RawJSON) > 0 {
		c.Data(up.Status, "application/json", up.RawJSON)
		return true
	}

	message := up.Message
	if message == "" {
		message = err.Error()
	}

	switch style {
	case APIStyleAnthropic:
		c.JSON(up.Status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    anthropicErrorType(up),
				"message": message,
			},
		})
	default:
		// OpenAI-style clients, and routes with no recorded style (the
		// pre-existing generic shape is OpenAI-compatible).
		c.JSON(up.Status, ErrorResponse{
			Error: ErrorDetail{
				Message: message,
				Type:    errorTypeForStatus(up.Status, up.Type),
			},
		})
	}
	return true
}

// anthropicErrorType picks the error.type for an Anthropic-shaped
// response: the upstream's own type when the upstream already speaks
// Anthropic, else the canonical type for the status code.
func anthropicErrorType(up *UpstreamError) string {
	if up.Vendor == APIStyleAnthropic && up.Type != "" {
		return up.Type
	}
	return errorTypeForStatus(up.Status, "")
}

// errorTypeForStatus maps an upstream HTTP status to the canonical error
// type name shared by the Anthropic/OpenAI error taxonomies. A non-empty
// vendorType from a same-taxonomy vendor wins.
func errorTypeForStatus(status int, vendorType string) string {
	if vendorType != "" {
		return vendorType
	}
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}
