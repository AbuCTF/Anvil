package handlers

import (
	"math"
	"net/http"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
)

// conversionQuote mirrors convertPointsToCredits without changing balances.
// Keeping the arithmetic server-side means the UI always reflects deployed
// economy tuning, including quotes that cross a diminishing-rate block.
func conversionQuote(points float64, blocks int, cfg config.EconomyConfig) float64 {
	if points <= 0 {
		return 0
	}
	blockSize := cfg.P2CBlock
	if blockSize <= 0 {
		blockSize = 50
	}
	remaining := points
	credits := 0.0
	for remaining > 0 {
		chunk := math.Min(blockSize, remaining)
		rate := cfg.P2CBase * math.Pow(cfg.P2CRateDecay, float64(blocks))
		if rate < cfg.P2CMinRate {
			rate = cfg.P2CMinRate
		}
		credits += chunk * rate
		remaining -= chunk
		blocks++
	}
	return credits
}

func (h *EconomyHandler) QuoteConvert(c *gin.Context) {
	teamID, ok := h.econContext(c)
	if !ok {
		return
	}
	var req struct {
		Points float64 `json:"points" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Points <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "points must be positive"})
		return
	}
	var available float64
	var blocks int
	if err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE((SELECT points FROM economy_team_score WHERE team_id = $1), 0),
		        COALESCE((SELECT p2c_blocks FROM economy_team_score WHERE team_id = $1), 0)`, teamID).
		Scan(&available, &blocks); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to quote conversion"})
		return
	}
	if req.Points > available {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":            "you do not have enough points to convert",
			"available_points": available,
		})
		return
	}
	credits := conversionQuote(req.Points, blocks, h.config.Economy)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"points":         req.Points,
		"credits":        credits,
		"effective_rate": credits / req.Points,
		"blocks_used":    blocks,
	})
}
