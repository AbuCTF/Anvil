package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type hillTeamCredential struct {
	TeamID      string `json:"team_id"`
	Holder      string `json:"holder"`
	BearerToken string `json:"bearer_token"`
}

func hillHolderSlug(teamID, challengeID uuid.UUID) string {
	digest := sha256.Sum256([]byte("hill-v2:" + challengeID.String() + ":" + teamID.String()))
	return "team-" + hex.EncodeToString(digest[:8])
}

func (h *GameInternalHandler) HillCredentials(c *gin.Context) {
	hillID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hill ID"})
		return
	}
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer tx.Rollback(ctx)

	var challengeID uuid.UUID
	var resetToken string
	err = tx.QueryRow(ctx,
		`SELECT challenge_id, COALESCE(reset_secret, '') FROM game_koth_hills
		 WHERE id = $1 AND enabled = TRUE AND controller_protocol = 'hill-v2-http'`, hillID).
		Scan(&challengeID, &resetToken)
	if errors.Is(err, pgx.ErrNoRows) || resetToken == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "hill credentials unavailable"})
		return
	}
	if err != nil {
		h.logger.Error("internal: load hill credentials", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	rows, err := tx.Query(ctx,
		`SELECT t.id FROM teams t JOIN game_teams gt ON gt.id = t.id
		 WHERE gt.status = 'active' AND gt.is_nop = FALSE ORDER BY t.id FOR SHARE OF t`)
	if err != nil {
		h.logger.Error("internal: list hill teams", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	var teamIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		teamIDs = append(teamIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if len(teamIDs) == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "no active teams are commissioned"})
		return
	}

	credentials := make([]hillTeamCredential, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		holder := hillHolderSlug(teamID, challengeID)
		var storedHolder, bearer string
		queryErr := tx.QueryRow(ctx,
			`SELECT COALESCE(holder_slug, ''), COALESCE(bearer_token, '') FROM koth_entries
			 WHERE team_id = $1 AND challenge_id = $2`, teamID, challengeID).Scan(&storedHolder, &bearer)
		exists := queryErr == nil
		if queryErr != nil && !errors.Is(queryErr, pgx.ErrNoRows) {
			h.logger.Error("internal: load team hill credential", zap.Error(queryErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		needsUpdate := storedHolder != holder || bearer == ""
		if bearer == "" {
			bearer, err = generateOpaqueToken("hill_")
			if err != nil {
				h.logger.Error("internal: generate team hill credential", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		if !exists {
			_, err = tx.Exec(ctx,
				`INSERT INTO koth_entries (team_id, challenge_id, holder_slug, bearer_token)
				 VALUES ($1, $2, $3, $4)`, teamID, challengeID, holder, bearer)
		} else if needsUpdate {
			_, err = tx.Exec(ctx,
				`UPDATE koth_entries SET holder_slug = $3, bearer_token = $4
				 WHERE team_id = $1 AND challenge_id = $2`, teamID, challengeID, holder, bearer)
		}
		if err != nil {
			h.logger.Error("internal: save team hill credential")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		credentials = append(credentials, hillTeamCredential{TeamID: teamID.String(), Holder: holder, BearerToken: bearer})
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("internal: commit hill credentials", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"protocol": "hill-v2-http", "challenge_id": challengeID, "reset_token": resetToken, "teams": credentials,
	})
}
