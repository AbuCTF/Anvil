package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func evidenceTestContext(remoteAddr string, headers map[string]string) *gin.Context {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("POST", "/api/v1/challenges/test/submit", nil)
	context.Request.RemoteAddr = remoteAddr
	for key, value := range headers {
		context.Request.Header.Set(key, value)
	}
	return context
}

func TestRequestGeoEvidenceRequiresTrustedProxy(t *testing.T) {
	context := evidenceTestContext("203.0.113.9:43120", map[string]string{
		"X-Anvil-Geo-Country":   "IN",
		"X-Anvil-Geo-City":      "Bengaluru",
		"X-Anvil-Geo-Latitude":  "12.9716",
		"X-Anvil-Geo-Longitude": "77.5946",
	})

	geo := requestGeoEvidence(context, []string{"127.0.0.1", "172.16.0.0/12"})
	if geo.CountryCode != "" || geo.City != "" || geo.Latitude != nil || geo.Longitude != nil {
		t.Fatalf("untrusted peer supplied geo evidence: %#v", geo)
	}
}

func TestRequestGeoEvidenceReadsTrustedEdgeHeaders(t *testing.T) {
	context := evidenceTestContext("172.20.0.4:43120", map[string]string{
		"CF-IPCountry":          "in",
		"X-Anvil-Geo-Region":    "Karnataka",
		"X-Anvil-Geo-City":      "Bengaluru",
		"X-Anvil-Geo-Latitude":  "12.9716",
		"X-Anvil-Geo-Longitude": "77.5946",
	})

	geo := requestGeoEvidence(context, []string{"172.16.0.0/12"})
	if geo.CountryCode != "IN" || geo.Region != "Karnataka" || geo.City != "Bengaluru" {
		t.Fatalf("unexpected geo labels: %#v", geo)
	}
	if geo.Latitude == nil || geo.Longitude == nil || *geo.Latitude != 12.9716 || *geo.Longitude != 77.5946 {
		t.Fatalf("unexpected coordinates: %#v", geo)
	}
}

func TestRequestGeoEvidenceRejectsInvalidCoordinates(t *testing.T) {
	context := evidenceTestContext("172.20.0.4:43120", map[string]string{
		"X-Anvil-Geo-Country":   "XX",
		"X-Anvil-Geo-Latitude":  "120",
		"X-Anvil-Geo-Longitude": "77.5946",
	})

	geo := requestGeoEvidence(context, []string{"172.16.0.0/12"})
	if geo.CountryCode != "" || geo.Latitude != nil || geo.Longitude != nil {
		t.Fatalf("invalid geo evidence was accepted: %#v", geo)
	}
}
