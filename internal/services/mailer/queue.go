package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type Service struct {
	db     *database.DB
	cipher *Cipher
	logger *zap.Logger
}

type enqueueQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type queuedDelivery struct {
	ID             uuid.UUID
	Recipient      string
	TemplateSlug   string
	Payload        []byte
	Attempts       int
	MaxAttempts    int
	Subject        string
	BodyHTML       string
	BodyText       string
	TemplateActive bool
}

type queueProvider struct {
	ID                 uuid.UUID
	Name               string
	Host               string
	Port               int
	Username           string
	PasswordCiphertext []byte
	Security           string
	FromName           string
	FromAddress        string
	ReplyTo            string
	Priority           int
	DailyLimit         *int
	HourlyLimit        *int
	MinuteLimit        *int
}

var QueueVariables = map[string]bool{
	"participant_name": true,
	"event_name":       true,
	"activation_url":   true,
	"reset_url":        true,
	"login_url":        true,
	"username":         true,
	"expires_at":       true,
	"event_start":      true,
	"event_end":        true,
	"support_email":    true,
	"team_name":        true,
	"update_title":     true,
	"update_body":      true,
	"rank":             true,
	"score":            true,
}

func NewService(secret string, db *database.DB, logger *zap.Logger) (*Service, error) {
	cipher, err := NewCipher(secret)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, cipher: cipher, logger: logger}, nil
}

func (s *Service) Enqueue(ctx context.Context, recipient, templateSlug string, values map[string]string, createdBy *uuid.UUID) (uuid.UUID, error) {
	return s.enqueue(ctx, s.db.Pool, recipient, templateSlug, values, createdBy)
}

func (s *Service) EnqueueTx(ctx context.Context, tx pgx.Tx, recipient, templateSlug string, values map[string]string, createdBy *uuid.UUID) (uuid.UUID, error) {
	return s.enqueue(ctx, tx, recipient, templateSlug, values, createdBy)
}

func (s *Service) Ready(ctx context.Context) bool {
	var ready bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM mail_providers
			WHERE is_active AND is_healthy AND (circuit_open_until IS NULL OR circuit_open_until <= NOW())
		)
	`).Scan(&ready)
	return err == nil && ready
}

func (s *Service) enqueue(ctx context.Context, query enqueueQuery, recipient, templateSlug string, values map[string]string, createdBy *uuid.UUID) (uuid.UUID, error) {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	if err := ValidateAddress(recipient); err != nil {
		return uuid.Nil, err
	}
	if templateSlug == "" || len(templateSlug) > 64 {
		return uuid.Nil, errors.New("invalid mail template")
	}
	for key, value := range values {
		if !QueueVariables[key] || len(value) > 4000 {
			return uuid.Nil, errors.New("invalid mail template values")
		}
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return uuid.Nil, err
	}
	ciphertext, err := s.cipher.Encrypt(string(payload))
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = query.QueryRow(ctx, `
		INSERT INTO mail_deliveries (recipient, template_slug, payload_ciphertext, status, created_by)
		SELECT $1, mt.slug, $3, 'queued', $4 FROM mail_templates mt WHERE mt.slug = $2 AND mt.is_active
		RETURNING id
	`, recipient, templateSlug, ciphertext, createdBy).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errors.New("mail template is unavailable")
	}
	return id, err
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for range 20 {
				delivery, err := s.claim(ctx)
				if errors.Is(err, pgx.ErrNoRows) {
					break
				}
				if err != nil {
					s.logger.Error("claim mail delivery", zap.Error(err))
					break
				}
				s.process(ctx, delivery)
			}
		}
	}
}

func (s *Service) claim(ctx context.Context) (queuedDelivery, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return queuedDelivery{}, err
	}
	defer tx.Rollback(ctx)
	var delivery queuedDelivery
	err = tx.QueryRow(ctx, `
		SELECT d.id, d.recipient, COALESCE(d.template_slug, ''), d.payload_ciphertext,
		       CASE WHEN d.status = 'queued' THEN d.attempts + 1 ELSE d.attempts END,
		       d.max_attempts, COALESCE(t.subject, ''),
		       COALESCE(t.body_html, ''), COALESCE(t.body_text, ''), COALESCE(t.is_active, FALSE)
		FROM mail_deliveries d
		LEFT JOIN mail_templates t ON t.slug = d.template_slug
		WHERE (d.status = 'queued' AND d.not_before <= NOW() AND d.attempts < d.max_attempts)
		   OR (d.status = 'sending' AND d.updated_at <= NOW() - INTERVAL '5 minutes' AND d.attempts <= d.max_attempts)
		ORDER BY d.not_before, d.created_at
		FOR UPDATE OF d SKIP LOCKED LIMIT 1
	`).Scan(&delivery.ID, &delivery.Recipient, &delivery.TemplateSlug, &delivery.Payload, &delivery.Attempts, &delivery.MaxAttempts, &delivery.Subject, &delivery.BodyHTML, &delivery.BodyText, &delivery.TemplateActive)
	if err != nil {
		return queuedDelivery{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_deliveries SET status = 'sending', attempts = CASE WHEN status = 'queued' THEN attempts + 1 ELSE attempts END, updated_at = NOW() WHERE id = $1`, delivery.ID); err != nil {
		return queuedDelivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return queuedDelivery{}, err
	}
	return delivery, nil
}

func (s *Service) process(ctx context.Context, delivery queuedDelivery) {
	if !delivery.TemplateActive || len(delivery.Payload) == 0 {
		s.finishFailure(ctx, delivery, "template_unavailable")
		return
	}
	plaintext, err := s.cipher.Decrypt(delivery.Payload)
	if err != nil {
		s.logger.Error("decrypt mail payload", zap.String("delivery_id", delivery.ID.String()))
		s.finishFailure(ctx, delivery, "payload_unavailable")
		return
	}
	values := map[string]string{}
	if json.Unmarshal([]byte(plaintext), &values) != nil {
		s.finishFailure(ctx, delivery, "payload_unavailable")
		return
	}
	subject, bodyHTML, bodyText := Render(delivery.Subject, delivery.BodyHTML, delivery.BodyText, values)
	providers, err := s.availableProviders(ctx)
	if err != nil {
		s.logger.Error("load mail providers", zap.Error(err))
		s.finishFailure(ctx, delivery, "provider_unavailable")
		return
	}
	lastCode := "provider_unavailable"
	for _, provider := range providers {
		password, err := s.cipher.Decrypt(provider.PasswordCiphertext)
		if err != nil {
			lastCode = "credential_unavailable"
			s.recordProviderFailure(ctx, provider.ID, lastCode)
			continue
		}
		messageID, err := Send(ctx, Provider{
			Host: provider.Host, Port: provider.Port, Username: provider.Username,
			Password: password, Security: provider.Security, FromName: provider.FromName,
			FromAddress: provider.FromAddress, ReplyTo: provider.ReplyTo,
		}, Message{To: delivery.Recipient, Subject: subject, HTML: bodyHTML, Text: bodyText})
		if err != nil {
			lastCode = ErrorCode(err)
			s.logger.Warn("mail delivery provider failed", zap.String("delivery_id", delivery.ID.String()), zap.String("provider_id", provider.ID.String()), zap.String("error_code", lastCode), zap.Error(err))
			s.recordProviderFailure(ctx, provider.ID, lastCode)
			continue
		}
		_, _ = s.db.Pool.Exec(ctx, `UPDATE mail_providers SET is_healthy = TRUE, failure_count = 0, circuit_open_until = NULL, last_error_code = NULL, last_error_at = NULL, updated_at = NOW() WHERE id = $1`, provider.ID)
		_, err = s.db.Pool.Exec(ctx, `UPDATE mail_deliveries SET status = 'sent', provider_id = $2, provider_name = $3, error_code = NULL, message_id = $4, payload_ciphertext = NULL, sent_at = NOW(), updated_at = NOW() WHERE id = $1 AND status = 'sending'`, delivery.ID, provider.ID, provider.Name, messageID)
		if err != nil {
			s.logger.Error("finish mail delivery", zap.String("delivery_id", delivery.ID.String()), zap.Error(err))
		}
		return
	}
	s.finishFailure(ctx, delivery, lastCode)
}

func (s *Service) availableProviders(ctx context.Context) ([]queueProvider, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT p.id, p.name, p.host, p.port, COALESCE(p.username, ''), p.password_ciphertext,
		       p.security, p.from_name, p.from_address, COALESCE(p.reply_to, ''), p.priority,
		       p.daily_limit, p.hourly_limit, p.minute_limit
		FROM mail_providers p
		WHERE p.is_active
		  AND (p.is_healthy OR (p.failure_count >= 3 AND p.circuit_open_until <= NOW()))
		  AND (p.circuit_open_until IS NULL OR p.circuit_open_until <= NOW())
		  AND (p.daily_limit IS NULL OR p.daily_limit > (SELECT COUNT(*) FROM mail_deliveries d WHERE d.provider_id = p.id AND d.status = 'sent' AND d.sent_at >= date_trunc('day', NOW())))
		  AND (p.hourly_limit IS NULL OR p.hourly_limit > (SELECT COUNT(*) FROM mail_deliveries d WHERE d.provider_id = p.id AND d.status = 'sent' AND d.sent_at >= NOW() - INTERVAL '1 hour'))
		  AND (p.minute_limit IS NULL OR p.minute_limit > (SELECT COUNT(*) FROM mail_deliveries d WHERE d.provider_id = p.id AND d.status = 'sent' AND d.sent_at >= NOW() - INTERVAL '1 minute'))
		ORDER BY p.priority, p.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	providers := []queueProvider{}
	for rows.Next() {
		var provider queueProvider
		if err := rows.Scan(&provider.ID, &provider.Name, &provider.Host, &provider.Port, &provider.Username, &provider.PasswordCiphertext, &provider.Security, &provider.FromName, &provider.FromAddress, &provider.ReplyTo, &provider.Priority, &provider.DailyLimit, &provider.HourlyLimit, &provider.MinuteLimit); err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

func (s *Service) recordProviderFailure(ctx context.Context, providerID uuid.UUID, errorCode string) {
	_, _ = s.db.Pool.Exec(ctx, `
		UPDATE mail_providers SET is_healthy = (failure_count + 1 < 3), failure_count = failure_count + 1,
		last_error_code = $2, last_error_at = NOW(),
		circuit_open_until = CASE WHEN failure_count + 1 >= 3 THEN NOW() + INTERVAL '10 minutes' ELSE circuit_open_until END,
		updated_at = NOW() WHERE id = $1
	`, providerID, errorCode)
}

func (s *Service) finishFailure(ctx context.Context, delivery queuedDelivery, errorCode string) {
	if delivery.Attempts >= delivery.MaxAttempts {
		_, _ = s.db.Pool.Exec(ctx, `UPDATE mail_deliveries SET status = 'failed', error_code = $2, payload_ciphertext = NULL, updated_at = NOW() WHERE id = $1 AND status = 'sending'`, delivery.ID, errorCode)
		return
	}
	delayMinutes := 1 << min(delivery.Attempts-1, 6)
	_, _ = s.db.Pool.Exec(ctx, `UPDATE mail_deliveries SET status = 'queued', error_code = $2, not_before = NOW() + ($3 * INTERVAL '1 minute'), updated_at = NOW() WHERE id = $1 AND status = 'sending'`, delivery.ID, errorCode, delayMinutes)
}

func Render(subject, bodyHTML, bodyText string, values map[string]string) (string, string, string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		placeholder := "{{" + key + "}}"
		headerValue := strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
		subject = strings.ReplaceAll(subject, placeholder, headerValue)
		bodyText = strings.ReplaceAll(bodyText, placeholder, value)
		bodyHTML = strings.ReplaceAll(bodyHTML, placeholder, html.EscapeString(value))
	}
	return subject, bodyHTML, bodyText
}

func ErrorCode(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "authentication"):
		return "authentication_failed"
	case strings.Contains(message, "starttls") || strings.Contains(message, " tls"):
		return "tls_failed"
	case strings.Contains(message, "recipient"):
		return "recipient_rejected"
	case strings.Contains(message, "sender"):
		return "sender_rejected"
	case strings.Contains(message, "connect") || strings.Contains(message, "dial"):
		return "connection_failed"
	default:
		return "delivery_failed"
	}
}
