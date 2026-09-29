package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func adminEntityAudit(ctx context.Context, db *database.DB, entityType string, entityID uuid.UUID) ([]gin.H, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT a.id, a.user_id, COALESCE(u.username, ''), a.action,
		       COALESCE(a.old_values, 'null'::jsonb), COALESCE(a.new_values, 'null'::jsonb),
		       COALESCE(a.ip_address::text, ''), LEFT(COALESCE(a.user_agent, ''), 500), a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.entity_type = $1 AND a.entity_id = $2
		ORDER BY a.created_at DESC LIMIT 500`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []gin.H{}
	for rows.Next() {
		var id, username, action, ipAddress, userAgent string
		var actorID *uuid.UUID
		var oldRaw, newRaw json.RawMessage
		var createdAt time.Time
		if err := rows.Scan(&id, &actorID, &username, &action, &oldRaw, &newRaw, &ipAddress, &userAgent, &createdAt); err != nil {
			return nil, err
		}
		var oldValues, newValues any
		_ = json.Unmarshal(oldRaw, &oldValues)
		_ = json.Unmarshal(newRaw, &newValues)
		entries = append(entries, gin.H{
			"id": id, "actor_id": actorID, "actor": username, "action": action,
			"old_values": oldValues, "new_values": newValues, "ip_address": ipAddress,
			"user_agent": userAgent, "created_at": createdAt.Unix(),
		})
	}
	return entries, rows.Err()
}
