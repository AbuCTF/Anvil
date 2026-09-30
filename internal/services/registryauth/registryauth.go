package registryauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/mailer"
	"github.com/distribution/reference"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Service struct {
	db     *database.DB
	cipher *mailer.Cipher
}

type Credential struct {
	Registry string
	Username string
	Secret   string
}

func NewService(secret string, db *database.DB) (*Service, error) {
	cipher, err := mailer.NewScopedCipher(secret, "registry-auth")
	if err != nil {
		return nil, err
	}
	return &Service{db: db, cipher: cipher}, nil
}

func SupportedRegistry(value string) bool {
	return value == "docker.io" || value == "ghcr.io"
}

func (s *Service) Save(ctx context.Context, registry, username, secret string, updatedBy uuid.UUID) error {
	registry = strings.ToLower(strings.TrimSpace(registry))
	username = strings.TrimSpace(username)
	if !SupportedRegistry(registry) {
		return errors.New("registry must be docker.io or ghcr.io")
	}
	if username == "" || len(username) > 255 || strings.ContainsAny(username, "\r\n") {
		return errors.New("registry username is required")
	}
	if secret == "" || len(secret) > 4096 {
		return errors.New("registry access token is required")
	}
	ciphertext, err := s.cipher.Encrypt(secret)
	if err != nil {
		return err
	}
	_, err = s.db.Pool.Exec(ctx, `
		INSERT INTO registry_credentials (registry, username, secret_ciphertext, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (registry) DO UPDATE SET username = EXCLUDED.username,
		secret_ciphertext = EXCLUDED.secret_ciphertext, updated_by = EXCLUDED.updated_by, updated_at = NOW()
	`, registry, username, ciphertext, updatedBy)
	return err
}

func (s *Service) Delete(ctx context.Context, registry string) (bool, error) {
	result, err := s.db.Pool.Exec(ctx, `DELETE FROM registry_credentials WHERE registry = $1`, strings.ToLower(strings.TrimSpace(registry)))
	return err == nil && result.RowsAffected() == 1, err
}

func (s *Service) Get(ctx context.Context, registry string) (*Credential, error) {
	var username string
	var ciphertext []byte
	err := s.db.Pool.QueryRow(ctx, `SELECT username, secret_ciphertext FROM registry_credentials WHERE registry = $1`, strings.ToLower(strings.TrimSpace(registry))).Scan(&username, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	secret, err := s.cipher.Decrypt(ciphertext)
	if err != nil {
		return nil, err
	}
	return &Credential{Registry: registry, Username: username, Secret: secret}, nil
}

func (s *Service) EncodedAuth(ctx context.Context, image string) string {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return ""
	}
	registry := reference.Domain(named)
	credential, err := s.Get(ctx, registry)
	if err != nil || credential == nil {
		return ""
	}
	payload, err := json.Marshal(map[string]string{
		"username":      credential.Username,
		"password":      credential.Secret,
		"serveraddress": registry,
	})
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(payload)
}
