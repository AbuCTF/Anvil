package middleware

import (
	"net/http"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	gameTeamIDKey    = "game_team_id"
	gameTeamIsNOPKey = "game_team_is_nop"
)

// TeamTokenAuth reads X-Team-Token, resolves the game team, and sets
// game_team_id and game_team_is_nop in the gin context.
// Returns 401 on a missing or unrecognised token.
func TeamTokenAuth(db *database.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := c.GetHeader("X-Team-Token")
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "X-Team-Token required"})
			return
		}
		teamID, isNOP, found, err := game.TeamForToken(c.Request.Context(), db, tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if !found {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid team token"})
			return
		}
		c.Set(gameTeamIDKey, teamID)
		c.Set(gameTeamIsNOPKey, isNOP)
		c.Next()
	}
}

// GetGameTeamID returns the game team UUID placed in context by TeamTokenAuth.
func GetGameTeamID(c *gin.Context) *uuid.UUID {
	v, ok := c.Get(gameTeamIDKey)
	if !ok {
		return nil
	}
	id, ok := v.(uuid.UUID)
	if !ok {
		return nil
	}
	return &id
}

// GetGameTeamIsNOP returns true when the team resolved by TeamTokenAuth is a
// NOP (organiser) team.
func GetGameTeamIsNOP(c *gin.Context) bool {
	v, _ := c.Get(gameTeamIsNOPKey)
	nop, _ := v.(bool)
	return nop
}
