package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	instv1 "github.com/anvil-lab/anvil/instancer/api/v1alpha1"
)

func TestInstanceID(t *testing.T) {
	a := InstanceID("secret", "team1", "chalA")
	if len(a) != 16 {
		t.Fatalf("want 16 hex chars, got %d (%q)", len(a), a)
	}
	if a != InstanceID("secret", "team1", "chalA") {
		t.Fatal("not deterministic")
	}
	// The null separator must prevent (team+chal) collisions.
	if InstanceID("s", "ab", "c") == InstanceID("s", "a", "bc") {
		t.Fatal("delimiter collision")
	}
	if a == InstanceID("other", "team1", "chalA") {
		t.Fatal("secret should change the id")
	}
}

func TestHost(t *testing.T) {
	got := host("web", "deadbeefdeadbeef", "web", "h7tex.com")
	if got != "web-deadbeefdeadbeef.web.h7tex.com" {
		t.Fatalf("bad host: %s", got)
	}
}

func TestEffectiveExpiry(t *testing.T) {
	r := &ChallengeInstanceReconciler{}
	created := time.Now()
	exp := created.Add(2 * time.Hour)
	inst := &instv1.ChallengeInstance{}
	inst.CreationTimestamp = metav1.NewTime(created)
	inst.Spec.ExpiresAt = metav1.NewTime(exp)

	// No cap: honor ExpiresAt.
	if got := r.effectiveExpiry(inst); !got.Equal(exp) {
		t.Fatalf("uncapped: want %v got %v", exp, got)
	}
	// MaxLifetime shorter than ExpiresAt clamps it.
	inst.Spec.MaxLifetime = &metav1.Duration{Duration: 30 * time.Minute}
	want := created.Add(30 * time.Minute)
	if got := r.effectiveExpiry(inst); !got.Equal(want) {
		t.Fatalf("capped: want %v got %v", want, got)
	}
}
