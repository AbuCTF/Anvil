package mailer

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Cipher struct {
	aead cipher.AEAD
	aad  []byte
}

type Provider struct {
	Host        string
	Port        int
	Username    string
	Password    string
	Security    string
	FromName    string
	FromAddress string
	ReplyTo     string
}

type Message struct {
	To       string
	Subject  string
	HTML     string
	Text     string
	FromName string
	ReplyTo  string
}

var hostnamePattern = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*)$`)

func NewCipher(secret string) (*Cipher, error) {
	return newCipher(secret, "anvil/mail-secrets/v1\x00", "anvil-mail")
}

func NewScopedCipher(secret, scope string) (*Cipher, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`).MatchString(scope) {
		return nil, errors.New("invalid secret scope")
	}
	return newCipher(secret, "anvil/"+scope+"/v1\x00", "anvil-"+scope)
}

func newCipher(secret, keyPrefix, aad string) (*Cipher, error) {
	if len(secret) < 32 {
		return nil, errors.New("encryption secret must be at least 32 bytes")
	}
	key := sha256.Sum256([]byte(keyPrefix + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, aad: []byte(aad)}, nil
}

func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	output := append([]byte{1}, nonce...)
	return c.aead.Seal(output, nonce, []byte(plaintext), c.aad), nil
}

func (c *Cipher) Decrypt(ciphertext []byte) (string, error) {
	nonceSize := c.aead.NonceSize()
	if len(ciphertext) < 1+nonceSize || ciphertext[0] != 1 {
		return "", errors.New("invalid mail secret")
	}
	nonce := ciphertext[1 : 1+nonceSize]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext[1+nonceSize:], c.aad)
	if err != nil {
		return "", errors.New("invalid mail secret")
	}
	return string(plaintext), nil
}

func ValidateProvider(provider Provider) error {
	provider.Host = strings.TrimSpace(provider.Host)
	if provider.Host == "" || len(provider.Host) > 255 || (net.ParseIP(provider.Host) == nil && !hostnamePattern.MatchString(provider.Host)) {
		return errors.New("host must be a valid hostname or IP address")
	}
	if provider.Port < 1 || provider.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if provider.Security != "starttls" && provider.Security != "tls" {
		return errors.New("security must be starttls or tls")
	}
	if err := ValidateAddress(provider.FromAddress); err != nil {
		return fmt.Errorf("from address %w", err)
	}
	if provider.ReplyTo != "" {
		if err := ValidateAddress(provider.ReplyTo); err != nil {
			return fmt.Errorf("reply-to %w", err)
		}
	}
	for label, value := range map[string]string{"username": provider.Username, "from name": provider.FromName} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s contains a line break", label)
		}
	}
	return nil
}

func ValidateAddress(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return errors.New("must be a valid email address")
	}
	address, err := mail.ParseAddress(value)
	if err != nil || !strings.EqualFold(address.Address, value) {
		return errors.New("must be a valid email address")
	}
	return nil
}

func Send(ctx context.Context, provider Provider, message Message) (string, error) {
	if err := ValidateProvider(provider); err != nil {
		return "", err
	}
	if err := ValidateAddress(message.To); err != nil {
		return "", fmt.Errorf("recipient %w", err)
	}
	if strings.TrimSpace(message.Subject) == "" || len([]rune(message.Subject)) > 500 || strings.ContainsAny(message.Subject, "\r\n") {
		return "", errors.New("subject must be 1-500 characters without line breaks")
	}
	if message.FromName == "" {
		message.FromName = provider.FromName
	}
	if message.ReplyTo == "" {
		message.ReplyTo = provider.ReplyTo
	}
	raw, messageID, err := encodeMessage(provider, message)
	if err != nil {
		return "", err
	}

	dialer := &net.Dialer{Timeout: 12 * time.Second}
	address := net.JoinHostPort(provider.Host, strconv.Itoa(provider.Port))
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", fmt.Errorf("connect smtp: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(20 * time.Second))
	tlsConfig := &tls.Config{ServerName: provider.Host, MinVersion: tls.VersionTLS12}
	if provider.Security == "tls" {
		tlsConnection := tls.Client(connection, tlsConfig)
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return "", fmt.Errorf("smtp tls: %w", err)
		}
		connection = tlsConnection
	}
	client, err := smtp.NewClient(connection, provider.Host)
	if err != nil {
		return "", fmt.Errorf("smtp greeting: %w", err)
	}
	defer client.Close()
	if provider.Security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return "", errors.New("smtp server does not offer STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return "", fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if provider.Username != "" || provider.Password != "" {
		if provider.Username == "" || provider.Password == "" {
			return "", errors.New("smtp username and password must be configured together")
		}
		if err := client.Auth(smtp.PlainAuth("", provider.Username, provider.Password, provider.Host)); err != nil {
			return "", fmt.Errorf("smtp authentication: %w", err)
		}
	}
	if err := client.Mail(provider.FromAddress); err != nil {
		return "", fmt.Errorf("smtp sender: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return "", fmt.Errorf("smtp recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return "", fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return "", fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("smtp finish: %w", err)
	}
	if err := client.Quit(); err != nil {
		return "", fmt.Errorf("smtp quit: %w", err)
	}
	return messageID, nil
}

func encodeMessage(provider Provider, message Message) ([]byte, string, error) {
	identifier := make([]byte, 18)
	if _, err := io.ReadFull(rand.Reader, identifier); err != nil {
		return nil, "", err
	}
	messageID := base64.RawURLEncoding.EncodeToString(identifier) + "@" + provider.Host
	from := (&mail.Address{Name: message.FromName, Address: provider.FromAddress}).String()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if message.Text != "" {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", "text/plain; charset=utf-8")
		header.Set("Content-Transfer-Encoding", "base64")
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		_, _ = part.Write([]byte(base64.StdEncoding.EncodeToString([]byte(message.Text))))
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Content-Transfer-Encoding", "base64")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	_, _ = part.Write([]byte(base64.StdEncoding.EncodeToString([]byte(message.HTML))))
	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	var output bytes.Buffer
	fmt.Fprintf(&output, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&output, "Message-ID: <%s>\r\n", messageID)
	fmt.Fprintf(&output, "From: %s\r\n", from)
	fmt.Fprintf(&output, "To: %s\r\n", message.To)
	fmt.Fprintf(&output, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", message.Subject))
	if message.ReplyTo != "" {
		fmt.Fprintf(&output, "Reply-To: %s\r\n", message.ReplyTo)
	}
	fmt.Fprintf(&output, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&output, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", writer.Boundary())
	output.Write(body.Bytes())
	return output.Bytes(), messageID, nil
}
