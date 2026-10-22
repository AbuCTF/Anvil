package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestBulkSubmitErrorLogHashesFlag(t *testing.T) {
	const flag = "H7CTF{0123456789abcdef0123456789abcdef}"
	core, observed := observer.New(zap.ErrorLevel)
	logBulkSubmitError(zap.New(core), errors.New("database unavailable"), flag)

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	if strings.Contains(entries[0].Message, flag) {
		t.Fatal("raw submitted flag appeared in log message")
	}
	fields := entries[0].ContextMap()
	for _, value := range fields {
		if strings.Contains(fmt.Sprint(value), flag) {
			t.Fatal("raw submitted flag appeared in structured log field")
		}
	}
	digest := sha256.Sum256([]byte(flag))
	if got := fields["flag_sha256"]; got != hex.EncodeToString(digest[:]) {
		t.Fatalf("flag_sha256 = %v, want full SHA-256", got)
	}
}
