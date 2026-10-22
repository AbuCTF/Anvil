package game

import (
	"regexp"
	"strings"

	"github.com/anvil-lab/anvil/internal/models"
)

const (
	maxCheckerDiagnosticCode    = 40
	maxCheckerDiagnosticMessage = 160
)

var checkerDiagnosticCode = regexp.MustCompile(`^[A-Z0-9]+-[A-Z]+-[0-9]+$`)

func SanitizeCheckerDiagnostic(status models.SLAStatus, code, message string) (string, string) {
	if strings.TrimSpace(code) == "" && strings.TrimSpace(message) == "" {
		return "", ""
	}
	code = strings.TrimSpace(code)
	if code == "" {
		var found bool
		code, _, found = strings.Cut(message, ":")
		if !found {
			code = ""
		}
		code = strings.TrimSpace(code)
	}
	if len(code) > maxCheckerDiagnosticCode || !checkerDiagnosticCode.MatchString(code) {
		code = ""
	}

	switch status {
	case models.SLAOk:
		message = "Service check passed"
	case models.SLADown:
		message = "Service unavailable"
	case models.SLAFaulty:
		message = "Service response invalid"
	case models.SLAFlagNotFound:
		message = "Stored flag unavailable"
	case models.SLARecovering:
		message = "Older stored flag unavailable"
	case models.SLAError:
		message = "Checker error; tick credited"
	default:
		message = "Checker result unavailable"
	}
	if len(message) > maxCheckerDiagnosticMessage {
		message = message[:maxCheckerDiagnosticMessage]
	}
	return code, message
}

func sanitizeCheckerResult(result Result) Result {
	result.Code, result.Message = SanitizeCheckerDiagnostic(result.Status, result.Code, result.Message)
	return result
}
