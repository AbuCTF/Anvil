package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// QuoteExtension exposes the next timer purchase before the irreversible
// charge. It reads the same state and configuration used by the write path.
func (h *EconomyHandler) QuoteExtension(c *gin.Context) {
	teamID, ok := h.econContext(c)
	if !ok {
		return
	}
	var status, difficulty string
	var used int
	var expires time.Time
	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT e.status, e.extensions_used, COALESCE(e.expires_at, NOW()), c.difficulty::text
		FROM economy_challenge_state e
		JOIN challenges c ON c.id = e.challenge_id
		WHERE e.team_id = $1 AND c.slug = $2`, teamID, c.Param("slug")).
		Scan(&status, &used, &expires, &difficulty)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (status != "open" || !expires.After(time.Now()))) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "challenge is not open"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to quote extension"})
		return
	}
	if used >= h.config.Economy.MaxExtensions {
		c.JSON(http.StatusConflict, gin.H{"error": "no extensions remaining"})
		return
	}
	cost := bandParam(h.config.Economy.ExtCostFracs, used) * launchCost(h.config.Economy, difficulty)
	added := time.Duration(float64(bandTimer(h.config.Economy, difficulty)) * h.config.Economy.ExtAddStepsFrac)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"cost":                 cost,
		"added_seconds":        int(added.Seconds()),
		"extensions_used":      used,
		"extensions_remaining": h.config.Economy.MaxExtensions - used,
	})
}
