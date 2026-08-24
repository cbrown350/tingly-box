// Package sharing implements CRUD HTTP endpoints for shared API tokens.
// It is intentionally free of any internal/server import — error responses
// go through the shared internal/apierr package.
package sharing

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tingly-dev/tingly-box/internal/apierr"
	"github.com/tingly-dev/tingly-box/internal/db"
)

// --- handler ----------------------------------------------------------------

// Handler handles API-token HTTP requests.
type Handler struct {
	store *db.APITokenStore
}

// NewHandler creates a Handler backed by the given store.
func NewHandler(store *db.APITokenStore) *Handler {
	return &Handler{store: store}
}

// --- helpers ----------------------------------------------------------------

func recordToInfo(r *db.APITokenRecord) APITokenInfo {
	return APITokenInfo{
		TokenID:     r.TokenID,
		UserID:      r.UserID,
		TeamID:      r.TeamID,
		DisplayName: r.DisplayName,
		Enabled:     r.Enabled,
		LastUsedAt:  r.LastUsedAt,
		CreatedAt:   r.CreatedAt,
		CreatedBy:   r.CreatedBy,
	}
}

func generateRandomToken() (string, error) {
	b := make([]byte, 24) // 24 bytes → 48 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- handlers ---------------------------------------------------------------

// Create handles POST /tokens — creates a new shared API token.
func (h *Handler) Create(c *gin.Context) {
	var req TokenCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.SendBadRequest(c, err)
		return
	}

	// Each token gets its own unique user_id for data isolation.
	userUUID := uuid.New().String()

	randomToken, err := generateRandomToken()
	if err != nil {
		apierr.SendInternalErr(c, err, "failed to generate token")
		return
	}
	tokenString := "tb-share-" + randomToken

	teamID := req.TeamID
	if teamID == "" {
		teamID = db.DefaultTeamID
	}
	record, err := h.store.CreateTokenForTeam(userUUID, tokenString, teamID, req.DisplayName, "admin", nil)
	if err != nil {
		apierr.SendStoreError(c, err)
		return
	}

	c.JSON(http.StatusCreated, TokenCreateResponse{
		Token:       tokenString,
		TokenID:     record.TokenID,
		UserID:      record.UserID,
		TeamID:      record.TeamID,
		DisplayName: record.DisplayName,
		CreatedAt:   record.CreatedAt,
	})
}

// List handles GET /tokens — lists tokens with optional filters.
func (h *Handler) List(c *gin.Context) {
	userUUID := c.Query("user_id")
	teamID := c.Query("team_id")

	var enabled *bool
	if s := c.Query("enabled"); s != "" {
		if b, err := strconv.ParseBool(s); err == nil {
			enabled = &b
		}
	}

	limit := 100
	if s := c.Query("limit"); s != "" {
		if l, err := strconv.Atoi(s); err == nil && l > 0 {
			limit = l
		}
	}

	offset := 0
	if s := c.Query("offset"); s != "" {
		if o, err := strconv.Atoi(s); err == nil && o >= 0 {
			offset = o
		}
	}

	records, total, err := h.store.ListTokensForTeam(userUUID, teamID, enabled, limit, offset)
	if err != nil {
		apierr.SendInternalErr(c, err, "failed to list tokens")
		return
	}

	tokens := make([]APITokenInfo, len(records))
	for i := range records {
		tokens[i] = recordToInfo(&records[i])
	}
	c.JSON(http.StatusOK, TokenListResponse{Tokens: tokens, Total: total})
}

// MoveToTeam handles PUT /tokens/:token_id/team.
func (h *Handler) MoveToTeam(c *gin.Context) {
	tokenID := c.Param("token_id")
	if tokenID == "" {
		apierr.SendRequired(c, "token_id")
		return
	}
	var req TokenMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.SendBadRequest(c, err)
		return
	}
	if err := h.store.MoveTokenToTeam(tokenID, req.TeamID); err != nil {
		apierr.SendStoreError(c, err)
		return
	}
	record, err := h.store.GetToken(tokenID)
	if err != nil {
		apierr.SendStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, recordToInfo(record))
}

// Get handles GET /tokens/:token_id.
func (h *Handler) Get(c *gin.Context) {
	tokenID := c.Param("token_id")
	if tokenID == "" {
		apierr.SendRequired(c, "token_id")
		return
	}

	record, err := h.store.GetToken(tokenID)
	if err != nil {
		apierr.SendStoreError(c, err)
		return
	}

	c.JSON(http.StatusOK, recordToInfo(record))
}

// Delete handles DELETE /tokens/:token_id.
func (h *Handler) Delete(c *gin.Context) {
	tokenID := c.Param("token_id")
	if tokenID == "" {
		apierr.SendRequired(c, "token_id")
		return
	}

	if err := h.store.DeleteToken(tokenID); err != nil {
		apierr.SendStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Enable handles PUT /tokens/:token_id/enable.
func (h *Handler) Enable(c *gin.Context) {
	h.setEnabled(c, true)
}

// Disable handles PUT /tokens/:token_id/disable.
func (h *Handler) Disable(c *gin.Context) {
	h.setEnabled(c, false)
}

func (h *Handler) setEnabled(c *gin.Context, enabled bool) {
	tokenID := c.Param("token_id")
	if tokenID == "" {
		apierr.SendRequired(c, "token_id")
		return
	}

	if err := h.store.SetTokenEnabled(tokenID, enabled); err != nil {
		apierr.SendStoreError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Regenerate handles POST /tokens/:token_id/regenerate — keeps the same
// token_id but issues a new token string.
func (h *Handler) Regenerate(c *gin.Context) {
	tokenID := c.Param("token_id")
	if tokenID == "" {
		apierr.SendRequired(c, "token_id")
		return
	}

	record, err := h.store.GetToken(tokenID)
	if err != nil {
		apierr.SendStoreError(c, err)
		return
	}

	randomToken, err := generateRandomToken()
	if err != nil {
		apierr.SendInternalErr(c, err, "failed to generate token")
		return
	}
	newTokenString := "tb-share-" + randomToken

	if err := h.store.UpdateTokenString(tokenID, newTokenString); err != nil {
		apierr.SendStoreError(c, err)
		return
	}

	c.JSON(http.StatusOK, TokenCreateResponse{
		Token:       newTokenString,
		TokenID:     record.TokenID,
		UserID:      record.UserID,
		TeamID:      record.TeamID,
		DisplayName: record.DisplayName,
		CreatedAt:   record.CreatedAt,
	})
}
