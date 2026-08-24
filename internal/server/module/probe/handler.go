package probe

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tingly-dev/tingly-box/internal/apierr"
	"github.com/tingly-dev/tingly-box/internal/probe"
	"github.com/tingly-dev/tingly-box/internal/protocol"
	"github.com/tingly-dev/tingly-box/internal/typ"
)

// Handler exposes the probe HTTP endpoints. It carries the E2E and
// lightweight services; adaptive can be hung off the same struct when that
// strategy is decoupled from *Server.
type Handler struct {
	e2e   *probe.E2EProber
	light *probe.LightProber
}

// NewHandler builds a Handler around the given probe services.
func NewHandler(e2e *probe.E2EProber, light *probe.LightProber) *Handler {
	return &Handler{e2e: e2e, light: light}
}

// E2EResponse is the JSON envelope returned by POST /probe.
type E2EResponse struct {
	Success bool                `json:"success"`
	Error   *apierr.ErrorDetail `json:"error,omitempty"`
	Data    *probe.E2EData      `json:"data,omitempty"`
}

// LightweightResponse is the JSON envelope returned by POST /probe/lightweight.
type LightweightResponse struct {
	Success bool                                `json:"success"`
	Error   *apierr.ErrorDetail                 `json:"error,omitempty"`
	Data    *probe.LightweightProbeResponseData `json:"data,omitempty"`
}

// CurlResponse is the JSON envelope returned by POST /probe/curl.
type CurlResponse struct {
	Success bool                `json:"success"`
	Error   *apierr.ErrorDetail `json:"error,omitempty"`
	Data    *probe.CurlData     `json:"data,omitempty"`
}

// HandleE2EProbe handles SDK-level end-to-end probes (unified endpoint for
// rules, saved providers, and unsaved provider configs).
func (h *Handler) HandleE2EProbe(c *gin.Context) {
	var req probe.E2ERequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, E2EResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: "Invalid request body: " + err.Error(),
				Type:    apierr.TypeInvalidRequest,
			},
		})
		return
	}

	if err := probe.ValidateE2ERequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, E2EResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: err.Error(),
				Type:    apierr.TypeValidation,
			},
		})
		return
	}

	ctx := c.Request.Context()

	// Probe handles all probe shapes (non-stream/stream × plain/tool); the
	// stream-vs-non-stream decision is made inside the SDK helpers from
	// req.ResolveAxes().
	data, err := h.e2e.Probe(ctx, &req)

	if err != nil {
		c.JSON(http.StatusOK, E2EResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: err.Error(),
				Type:    "probe_error",
			},
		})
		return
	}

	// Stamp the request axes onto the result (including the cached-hit path,
	// whose result carries no axes of its own). Consumers reopening a stored
	// result restore the exact control state that produced it from this echo.
	stream, tool := req.ResolveAxes()
	if data != nil {
		data.Stream = stream
		data.Tool = tool
		data.Direct = req.Direct
		data.Protocol = req.Protocol
		data.Thinking = req.Thinking
		data.Vision = req.Vision
	}

	// LatencyMs is owned by the SDK probe (pure upstream round-trip time) — do
	// not overwrite it here.
	c.JSON(http.StatusOK, E2EResponse{Success: true, Data: data})
}

// HandleCurlProbe constructs the curl equivalent of a probe request without
// executing it. Same request body and validation as POST /probe; secrets are
// returned as placeholder env vars ($TB_API_KEY / $UPSTREAM_API_KEY).
func (h *Handler) HandleCurlProbe(c *gin.Context) {
	var req probe.E2ERequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, CurlResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: "Invalid request body: " + err.Error(),
				Type:    apierr.TypeInvalidRequest,
			},
		})
		return
	}

	if err := probe.ValidateE2ERequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, CurlResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: err.Error(),
				Type:    apierr.TypeValidation,
			},
		})
		return
	}

	data, err := h.e2e.BuildCurl(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusOK, CurlResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: err.Error(),
				Type:    "probe_error",
			},
		})
		return
	}

	c.JSON(http.StatusOK, CurlResponse{Success: true, Data: data})
}

// HandleLightweightProbe handles the optional "Test Connection" probe used
// when adding API keys. Always returns 200 with success=true once a request
// passes validation — per-endpoint results in Data are informational only
// and never block onboarding.
func (h *Handler) HandleLightweightProbe(c *gin.Context) {
	var req probe.LightweightProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, LightweightResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: "Invalid request body: " + err.Error(),
				Type:    apierr.TypeInvalidRequest,
			},
		})
		return
	}

	if req.APIBase == "" || req.APIStyle == "" || req.Token == "" {
		c.JSON(http.StatusBadRequest, LightweightResponse{
			Success: false,
			Error: &apierr.ErrorDetail{
				Message: "api_base, api_style, and token are required",
				Type:    apierr.TypeValidation,
			},
		})
		return
	}

	provider := &typ.Provider{
		Name:     req.Name,
		APIBase:  req.APIBase,
		APIStyle: protocol.APIStyle(req.APIStyle),
		Token:    req.Token,
		Enabled:  true,
	}
	if req.AuthType != "" {
		provider.AuthType = typ.AuthType(req.AuthType)
	}

	data := h.light.Probe(c.Request.Context(), provider)
	c.JSON(http.StatusOK, LightweightResponse{Success: true, Data: data})
}
