package handlers

import (
	"errors"
	"testing"
)

// a read-back failure after a committed extend still answers success without times,
// so the operator never retries a shift that already happened.
func TestExtendReplyIsRetrySafe(t *testing.T) {
	ok := extendReply(30, "2026-10-25T07:00:00Z", "2026-10-25T06:00:00Z", nil)
	if ok["status"] != "extended" || ok["end_at"] != "2026-10-25T07:00:00Z" || ok["freeze_at"] != "2026-10-25T06:00:00Z" {
		t.Fatalf("reply = %#v", ok)
	}
	failed := extendReply(30, "", "", errors.New("read failed"))
	if failed["status"] != "extended" || failed["minutes"] != 30 {
		t.Fatalf("reply = %#v", failed)
	}
	if _, has := failed["end_at"]; has {
		t.Fatalf("unread schedule must be omitted: %#v", failed)
	}
}
