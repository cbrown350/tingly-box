package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/gin-gonic/gin"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newErrorWriterContext(t *testing.T, style APIStyle) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/test", nil)
	if style != "" {
		WithClientStyle(style)(c)
	}
	return c, rec
}

func newAnthropicUpstreamError(t *testing.T, status int, raw string) error {
	t.Helper()
	var e anthropic.Error
	require.NoError(t, e.UnmarshalJSON([]byte(raw)))
	e.StatusCode = status
	return fmt.Errorf("upstream call failed: %w", &e)
}

func newOpenAIUpstreamError(t *testing.T, status int, raw string) error {
	t.Helper()
	var e openai.Error
	require.NoError(t, e.UnmarshalJSON([]byte(raw)))
	e.StatusCode = status
	return fmt.Errorf("upstream call failed: %w", &e)
}

// Same client and upstream protocol: the upstream body passes through
// byte-for-byte with the upstream's status.
func TestWriteUpstreamError_PassthroughSameStyle(t *testing.T) {
	raw := `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`
	err := newAnthropicUpstreamError(t, http.StatusTooManyRequests, raw)

	c, rec := newErrorWriterContext(t, APIStyleAnthropic)
	require.True(t, WriteUpstreamError(c, err))

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, OriginUpstream, rec.Header().Get(HeaderErrorOrigin))
	assert.Equal(t, raw, rec.Body.String())
}

// Same style for an OpenAI upstream: the SDK keeps only the inner error
// object, so passthrough must rebuild the {"error": ...} envelope.
func TestWriteUpstreamError_OpenAIEnvelopeRebuilt(t *testing.T) {
	inner := `{"message":"bad key","type":"invalid_request_error","code":"invalid_api_key"}`
	err := newOpenAIUpstreamError(t, http.StatusUnauthorized, inner)

	c, rec := newErrorWriterContext(t, APIStyleOpenAI)
	require.True(t, WriteUpstreamError(c, err))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":`+inner+`}`, rec.Body.String())
}

// Cross-protocol: the body is rebuilt in the client's shape, the type is
// mapped from the status, the upstream message survives verbatim.
func TestWriteUpstreamError_CrossStyleRebuild(t *testing.T) {
	raw := `{"message":"quota exceeded","type":"insufficient_quota","code":"insufficient_quota"}`
	err := newOpenAIUpstreamError(t, http.StatusTooManyRequests, raw)

	c, rec := newErrorWriterContext(t, APIStyleAnthropic)
	require.True(t, WriteUpstreamError(c, err))

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, OriginUpstream, rec.Header().Get(HeaderErrorOrigin))

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "error", resp["type"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, "rate_limit_error", errObj["type"])
	assert.Equal(t, "quota exceeded", errObj["message"])
}

// A transport-level failure carries no upstream response: WriteUpstreamError
// declines and the caller keeps its gateway-shaped path.
func TestWriteUpstreamError_NoUpstreamInfo(t *testing.T) {
	c, rec := newErrorWriterContext(t, APIStyleAnthropic)
	assert.False(t, WriteUpstreamError(c, errors.New("dial tcp: connection refused")))
	assert.Zero(t, rec.Body.Len())
}

// The written status is always the upstream's own — the failover
// orchestrator keys retry-vs-terminal on it, so passthrough must not
// shift it (429 stays retryable, 401 stays terminal).
func TestWriteUpstreamError_StatusPreservedForFailover(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		raw := `{"type":"error","error":{"type":"api_error","message":"x"}}`
		err := newAnthropicUpstreamError(t, status, raw)
		c, rec := newErrorWriterContext(t, APIStyleOpenAI)
		require.True(t, WriteUpstreamError(c, err))
		assert.Equal(t, status, rec.Code)
		assert.Equal(t, status, UpstreamStatus(err, http.StatusInternalServerError))
	}
}
