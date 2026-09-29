package handlers

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAnnouncementNormalizesSafeDefaults(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	req := createAnnouncementRequest{
		Title: "  Challenge update  ", Body: "  The attachment has been replaced.  ",
		Href: "/challenges/example",
	}
	if err := validateAnnouncement(&req, now); err != nil {
		t.Fatalf("validateAnnouncement(): %v", err)
	}
	if req.Title != "Challenge update" || req.Body != "The attachment has been replaced." {
		t.Fatalf("text was not normalized: %+v", req)
	}
	if req.Severity != "info" || req.Audience != "all" {
		t.Fatalf("defaults = %s/%s, want info/all", req.Severity, req.Audience)
	}
}

func TestValidateAnnouncementRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	tests := []struct {
		name string
		req  createAnnouncementRequest
	}{
		{name: "missing title", req: createAnnouncementRequest{Body: "body"}},
		{name: "long title", req: createAnnouncementRequest{Title: strings.Repeat("x", 161), Body: "body"}},
		{name: "missing body", req: createAnnouncementRequest{Title: "title"}},
		{name: "long body", req: createAnnouncementRequest{Title: "title", Body: strings.Repeat("x", maxAnnouncementBody+1)}},
		{name: "bad severity", req: createAnnouncementRequest{Title: "title", Body: "body", Severity: "urgent"}},
		{name: "targeted admin announcement", req: createAnnouncementRequest{Title: "title", Body: "body", Audience: "team"}},
		{name: "insecure external link", req: createAnnouncementRequest{Title: "title", Body: "body", Href: "http://example.com"}},
		{name: "script link", req: createAnnouncementRequest{Title: "title", Body: "body", Href: "javascript:alert(1)"}},
		{name: "hostless https link", req: createAnnouncementRequest{Title: "title", Body: "body", Href: "https:///status"}},
		{name: "protocol relative link", req: createAnnouncementRequest{Title: "title", Body: "body", Href: "//example.com"}},
		{name: "expiry before publication", req: createAnnouncementRequest{Title: "title", Body: "body", PublishAt: &future, ExpiresAt: &past}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateAnnouncement(&tt.req, now); err == nil {
				t.Fatal("validateAnnouncement() accepted invalid input")
			}
		})
	}
}

func TestValidateAnnouncementAcceptsHTTPSAndSchedule(t *testing.T) {
	now := time.Now().UTC()
	publish := now.Add(time.Hour)
	expires := publish.Add(2 * time.Hour)
	req := createAnnouncementRequest{
		Title: "Maintenance", Body: "Scheduled window", Severity: "warning",
		Audience: "participants", Href: "https://status.example.com/incidents/1",
		PublishAt: &publish, ExpiresAt: &expires, Pinned: true,
	}
	if err := validateAnnouncement(&req, now); err != nil {
		t.Fatalf("validateAnnouncement(): %v", err)
	}
}
