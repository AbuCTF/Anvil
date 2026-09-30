package mailer

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripAndTamperDetection(t *testing.T) {
	cipher, err := NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	ciphertext, err := cipher.Encrypt("smtp-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("smtp-secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil || plaintext != "smtp-secret" {
		t.Fatalf("decrypt = %q, %v", plaintext, err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := cipher.Decrypt(ciphertext); err == nil {
		t.Fatal("expected tampered ciphertext rejection")
	}
}

func TestValidateProvider(t *testing.T) {
	valid := Provider{Host: "smtp.example.com", Port: 587, Security: "starttls", FromName: "Event", FromAddress: "event@example.com"}
	if err := ValidateProvider(valid); err != nil {
		t.Fatalf("valid provider rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Provider){
		"scheme":    func(provider *Provider) { provider.Host = "https://smtp.example.com" },
		"port":      func(provider *Provider) { provider.Port = 0 },
		"plaintext": func(provider *Provider) { provider.Security = "plain" },
		"from":      func(provider *Provider) { provider.FromAddress = "Event <event@example.com>" },
		"header":    func(provider *Provider) { provider.FromName = "Event\r\nBcc: victim@example.com" },
	} {
		t.Run(name, func(t *testing.T) {
			provider := valid
			mutate(&provider)
			if err := ValidateProvider(provider); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestEncodeMessageRejectsUnsafeSubjectBeforeSend(t *testing.T) {
	provider := Provider{Host: "smtp.example.com", Port: 587, Security: "starttls", FromName: "Event", FromAddress: "event@example.com"}
	message := Message{To: "player@example.com", Subject: "Welcome", HTML: "<p>Hello</p>", Text: "Hello"}
	raw, identifier, err := encodeMessage(provider, message)
	if err != nil {
		t.Fatalf("encode message: %v", err)
	}
	if identifier == "" || !bytes.Contains(raw, []byte("multipart/alternative")) || !bytes.Contains(raw, []byte("player@example.com")) {
		t.Fatalf("unexpected message: %s", raw)
	}
}
