package handlers

import (
	"reflect"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/services/mailer"
)

func TestValidateMailTemplateRequest(t *testing.T) {
	request := mailTemplateRequest{
		Name: "Activation", Subject: "Activate {{event_name}}",
		BodyHTML: "<p>Hello {{participant_name}}</p><a href=\"{{activation_url}}\">Activate</a>",
		BodyText: "Hello {{participant_name}} {{activation_url}}", IsActive: true,
	}
	variables, err := validateMailTemplateRequest(&request)
	if err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
	want := []string{"activation_url", "event_name", "participant_name"}
	if !reflect.DeepEqual(variables, want) {
		t.Fatalf("variables = %v, want %v", variables, want)
	}
	request.BodyHTML = "{{server_secret}}"
	if _, err := validateMailTemplateRequest(&request); err == nil {
		t.Fatal("expected unsupported variable rejection")
	}
}

func TestRenderMailTemplateEscapesHTMLValues(t *testing.T) {
	subject, bodyHTML, bodyText := mailer.Render(
		"Hello {{participant_name}}",
		"<p>{{participant_name}}</p>",
		"{{participant_name}}",
		map[string]string{"participant_name": `<script>alert("x")</script>`},
	)
	if subject != `Hello <script>alert("x")</script>` || bodyText != `<script>alert("x")</script>` {
		t.Fatal("plain template values changed unexpectedly")
	}
	if bodyHTML != "<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</p>" {
		t.Fatalf("HTML value was not escaped: %s", bodyHTML)
	}
	subject, _, _ = mailer.Render("Notice {{update_title}}", "<p>ok</p>", "ok", map[string]string{"update_title": "safe\r\nBcc: attacker@example.test"})
	if strings.ContainsAny(subject, "\r\n") {
		t.Fatalf("subject contains a line break: %q", subject)
	}
}

func TestMailErrorCodeDoesNotExposeProviderError(t *testing.T) {
	for input, want := range map[string]string{
		"smtp authentication: 535 invalid credentials": "authentication_failed",
		"connect smtp: connection refused":             "connection_failed",
		"smtp recipient: mailbox unavailable":          "recipient_rejected",
		"unexpected provider response":                 "delivery_failed",
	} {
		if got := mailErrorCode(assertError(input)); got != want {
			t.Fatalf("mailErrorCode(%q) = %q, want %q", input, got, want)
		}
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
