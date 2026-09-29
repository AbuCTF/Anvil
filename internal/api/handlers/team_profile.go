package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type publicTeamMember struct {
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name,omitempty"`
}

// TeamProfile returns the public, scoreboard-safe view of a ranked team. It
// deliberately excludes join codes, credit balances, open challenges,
// instances, submissions, and every other field that could reveal strategy.
func (h *ScoreboardHandler) TeamProfile(c *gin.Context) {
	cancel := limitScoreboardRequest(c)
	defer cancel()
	if !h.scoreboardAvailable(c) {
		return
	}
	teamID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	teamRanked, economyMode, _, err := h.boardMode(c.Request.Context())
	if err != nil {
		h.respondQueryError(c, "failed to read scoreboard mode", err)
		return
	}
	if !teamRanked {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}

	privateView := c.GetString("role") == "admin"
	if !privateView {
		if viewerID, ok := c.Get("user_id"); ok {
			if id, valid := viewerID.(uuid.UUID); valid {
				if err := h.db.Pool.QueryRow(c.Request.Context(),
					`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND team_id = $2)`,
					id, teamID,
				).Scan(&privateView); err != nil {
					h.respondQueryError(c, "team profile viewer query", err)
					return
				}
			}
		}
	}
	cacheKey := "team-profile:public:" + teamID.String()
	if privateView {
		cacheKey = "team-profile:private:" + teamID.String()
		c.Set(scoreboardPrivateCacheKey, true)
	}
	if h.serveCachedJSON(c, cacheKey) {
		return
	}
	if !h.beginCacheFill(c, cacheKey) {
		return
	}
	defer h.finishCacheFill(cacheKey)

	tx, ok := h.beginReadSnapshot(c)
	if !ok {
		return
	}
	defer tx.Rollback(c.Request.Context())

	var name string
	var totalScore, rank int
	var lastSolve *time.Time
	rankQuery := `WITH ` + teamRankedCTE + `
		SELECT name, total_score, rank, last_solve FROM ranked WHERE id = $1`
	if economyMode {
		rankQuery = `WITH ` + teamEconomyRankedCTE + `
			SELECT name, ROUND(points)::int, rank, last_solve FROM ranked WHERE id = $1`
	}
	err = tx.QueryRow(c.Request.Context(), rankQuery, teamID).
		Scan(&name, &totalScore, &rank, &lastSolve)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if err != nil {
		h.respondQueryError(c, "team profile rank query", err)
		return
	}

	memberRows, err := tx.Query(c.Request.Context(), `
		SELECT username, display_name
		FROM users
		WHERE team_id = $1 AND status = 'active' AND role NOT IN ('admin', 'author')
		ORDER BY COALESCE(NULLIF(display_name, ''), username), username
	`, teamID)
	if err != nil {
		h.respondQueryError(c, "team profile members query", err)
		return
	}
	members := []publicTeamMember{}
	for memberRows.Next() {
		var member publicTeamMember
		if err := memberRows.Scan(&member.Username, &member.DisplayName); err != nil {
			memberRows.Close()
			h.logger.Error("team profile member scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		members = append(members, member)
	}
	if err := memberRows.Err(); err != nil {
		memberRows.Close()
		h.respondQueryError(c, "team profile member rows", err)
		return
	}
	memberRows.Close()

	challengeRows, err := tx.Query(c.Request.Context(), `
		WITH published AS (
			SELECT c.id, c.name, c.slug, c.difficulty, c.base_points,
			       COALESCE(cat.name, 'Uncategorized') AS category,
			       COALESCE(cat.color, '#94a3b8') AS category_color,
			       COALESCE(cat.sort_order, 999) AS category_order
			FROM challenges c
			LEFT JOIN categories cat ON cat.id = c.category_id
			WHERE c.status = 'published'
			  AND (c.release_date IS NULL OR c.release_date <= NOW())
		),
		flag_totals AS (
			SELECT f.challenge_id, COUNT(*)::int AS total_flags
			FROM flags f JOIN published p ON p.id = f.challenge_id
			GROUP BY f.challenge_id
		),
		public_teams AS (
			SELECT t.id FROM teams t WHERE `+publicTeamSQL("t")+`
		),
		first_team_flags AS (
			SELECT DISTINCT ON (u.team_id, s.flag_id)
			       u.team_id, s.flag_id, f.challenge_id, s.points_awarded, s.solved_at
			FROM solves s
			JOIN users u ON u.id = s.user_id
			JOIN public_teams pt ON pt.id = u.team_id
			JOIN flags f ON f.id = s.flag_id
			JOIN published p ON p.id = f.challenge_id
			ORDER BY u.team_id, s.flag_id, s.solved_at, s.id
		),
		team_progress AS (
			SELECT challenge_id, COUNT(*)::int AS solved_flags,
			       COALESCE(SUM(points_awarded), 0)::int AS awarded_points,
			       MAX(solved_at) AS completed_at
			FROM first_team_flags
			WHERE team_id = $1
			GROUP BY challenge_id
		),
		completion_candidates AS (
			SELECT team_id, challenge_id, MAX(solved_at) AS completed_at
			FROM first_team_flags ftf
			JOIN flag_totals totals USING (challenge_id)
			GROUP BY team_id, challenge_id, totals.total_flags
			HAVING totals.total_flags > 0 AND COUNT(*) >= totals.total_flags
		),
		ranked_completions AS MATERIALIZED (
			SELECT team_id, challenge_id, completed_at,
			       ROW_NUMBER() OVER (
			           PARTITION BY challenge_id ORDER BY completed_at, team_id
			       )::int AS blood_rank
			FROM completion_candidates
		)
		SELECT p.name, p.slug, p.category, p.category_color, p.difficulty::text,
		       p.base_points,
		       CASE WHEN $2
		            THEN ROUND(CASE WHEN COALESCE(ecs.holds_solve, false)
		                            THEN COALESCE(ecs.current_value, 0) ELSE 0 END)::int
		            ELSE COALESCE(tp.awarded_points, 0) END,
		       COALESCE(tp.solved_flags, 0), COALESCE(ft.total_flags, 0),
		       CASE WHEN $2 THEN COALESCE(ecs.status = 'solved' OR ecs.frac >= 1, false)
		            ELSE COALESCE(ft.total_flags, 0) > 0
		             AND COALESCE(tp.solved_flags, 0) >= COALESCE(ft.total_flags, 0) END,
		       CASE WHEN $2 AND COALESCE(ecs.status = 'solved' OR ecs.frac >= 1, false)
		            THEN COALESCE(tp.completed_at, (
		                SELECT MIN(epe.created_at)
		                FROM economy_point_events epe
		                WHERE epe.team_id = $1 AND epe.challenge_id = p.id
		            ))
		            WHEN NOT $2
		             AND COALESCE(ft.total_flags, 0) > 0
		             AND COALESCE(tp.solved_flags, 0) >= COALESCE(ft.total_flags, 0)
		            THEN tp.completed_at END,
		       CASE WHEN rc.blood_rank BETWEEN 1 AND 3 THEN rc.blood_rank ELSE 0 END
		FROM published p
		LEFT JOIN flag_totals ft ON ft.challenge_id = p.id
		LEFT JOIN team_progress tp ON tp.challenge_id = p.id
		LEFT JOIN economy_challenge_state ecs
		       ON ecs.team_id = $1 AND ecs.challenge_id = p.id
		LEFT JOIN ranked_completions rc
		       ON rc.team_id = $1 AND rc.challenge_id = p.id
		ORDER BY p.category_order, p.category, p.base_points, p.name
	`, teamID, economyMode)
	if err != nil {
		h.respondQueryError(c, "team profile challenge progress query", err)
		return
	}
	challenges := []profileChallenge{}
	for challengeRows.Next() {
		var challenge profileChallenge
		var solvedAt *time.Time
		if err := challengeRows.Scan(
			&challenge.Name, &challenge.Slug, &challenge.Category, &challenge.CategoryColor,
			&challenge.Difficulty, &challenge.Points, &challenge.AwardedPoints,
			&challenge.SolvedFlags, &challenge.TotalFlags, &challenge.Solved,
			&solvedAt, &challenge.BloodRank,
		); err != nil {
			challengeRows.Close()
			h.logger.Error("team profile challenge scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if solvedAt != nil {
			unix := solvedAt.Unix()
			challenge.SolvedAt = &unix
		}
		if !privateView && !challenge.Solved {
			challenge.AwardedPoints = 0
			challenge.SolvedFlags = 0
			challenge.SolvedAt = nil
		}
		challenges = append(challenges, challenge)
	}
	if err := challengeRows.Err(); err != nil {
		challengeRows.Close()
		h.respondQueryError(c, "team profile challenge progress rows", err)
		return
	}
	challengeRows.Close()

	challengesSolved := 0
	solves := []profileSolve{}
	for _, challenge := range challenges {
		if !challenge.Solved {
			continue
		}
		challengesSolved++
		if challenge.SolvedAt != nil {
			solves = append(solves, profileSolve{
				Name:          challenge.Name,
				Slug:          challenge.Slug,
				Category:      &challenge.Category,
				CategoryColor: &challenge.CategoryColor,
				Points:        challenge.AwardedPoints,
				SolvedAt:      *challenge.SolvedAt,
			})
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.respondQueryError(c, "failed to commit team profile snapshot", err)
		return
	}

	var lastSolveAt *int64
	if lastSolve != nil {
		unix := lastSolve.Unix()
		lastSolveAt = &unix
	}
	h.respondCacheableJSON(c, cacheKey, 2*time.Second, gin.H{
		"team": gin.H{
			"id":                teamID.String(),
			"name":              name,
			"total_score":       totalScore,
			"challenges_solved": challengesSolved,
			"global_rank":       rank,
			"last_solve_at":     lastSolveAt,
			"members":           members,
		},
		"economy":    economyMode,
		"solves":     solves,
		"challenges": challenges,
	})
}
