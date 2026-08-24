package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tingly-dev/tingly-box/internal/apierr"
)

// TokenResponse represents the token response
type TokenResponse struct {
	Token string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	Type  string `json:"type" example:"Bearer"`
}

// GenerateToken handles token generation requests
func (h *WebHandler) GenerateToken(c *gin.Context) {
	var req struct {
		ClientID string `json:"client_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.SendBadRequestMsg(c, "Invalid request body: "+err.Error())
		return
	}

	token, err := h.deps.JWTManager.GenerateToken(req.ClientID)
	if err != nil {
		apierr.SendInternalErr(c, err, "failed to generate token")
		return
	}

	token = "tingly-box-" + token
	err = h.deps.Config.SetModelToken(token)
	if err != nil {
		apierr.SendInternalErr(c, err, "failed to save token")
		return
	}

	response := struct {
		Success bool          `json:"success"`
		Data    TokenResponse `json:"data"`
	}{
		Success: true,
		Data:    TokenResponse{Token: token, Type: "Bearer"},
	}

	c.JSON(http.StatusOK, response)
}

// GetToken handles token retrieval requests - generates a token if it doesn't exist
func (h *WebHandler) GetToken(c *gin.Context) {
	globalConfig := h.deps.Config

	// Check if token already exists
	if globalConfig != nil && globalConfig.HasModelToken() {
		token := globalConfig.GetModelToken()
		c.JSON(http.StatusOK, gin.H{
			"token": token,
			"type":  "Bearer",
		})
		return
	}

	// Generate a new token if it doesn't exist
	// Use a default client ID for automatic token generation
	clientID := "auto-generated"
	token, err := h.deps.JWTManager.GenerateToken(clientID)
	if err != nil {
		apierr.SendInternalErr(c, err, "failed to generate token")
		return
	}

	// Save the token to config
	token = "tingly-box-" + token
	err = globalConfig.SetModelToken(token)
	if err != nil {
		apierr.SendInternalErr(c, err, "failed to save token")
		return
	}

	response := struct {
		Success bool          `json:"success"`
		Data    TokenResponse `json:"data"`
	}{
		Success: true,
		Data:    TokenResponse{Token: token, Type: "Bearer"},
	}

	c.JSON(http.StatusOK, response)
}
