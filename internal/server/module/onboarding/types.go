package onboarding

import "github.com/tingly-dev/tingly-box/internal/apierr"

// TokenCandidate is a possible API token found in the input. The extractor
// stays vendor-agnostic — it just reports what it saw and where it came
// from. The user picks which one (if any) to use.
type TokenCandidate struct {
	Value   string `json:"value"`
	Preview string `json:"preview"`
	Source  string `json:"source"` // bearer | x-api-key | env:NAME | json:api_key | key_prefix
}

// ExtractRequest is the body for POST /api/v1/onboarding/extract.
type ExtractRequest struct {
	Input string `json:"input"`
}

// ExtractData is the inner payload returned by the extractor. It is a flat
// list of detected URLs and tokens — provider matching, if any, is done on
// the client side after the user picks values.
type ExtractData struct {
	URLs   []string         `json:"urls"`
	Tokens []TokenCandidate `json:"tokens"`
}

// ExtractResponse mirrors the rest of the v1 envelope shape used elsewhere.
type ExtractResponse struct {
	Success bool         `json:"success"`
	Data    *ExtractData `json:"data,omitempty"`
	Error   *ErrorDetail `json:"error,omitempty"`
}

// ErrorDetail is the shared error envelope from internal/apierr, re-exported
// so this package's response models keep their swagger identity.
type ErrorDetail = apierr.ErrorDetail
