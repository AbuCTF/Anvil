package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestReserveRequests(t *testing.T) {
	cases := []struct{ cpu, mem, wantCPU, wantMem string }{
		{"500m", "256Mi", "50m", "64Mi"},
		{"1", "512Mi", "100m", "128Mi"},
		{"2", "1Gi", "200m", "256Mi"},
		{"50m", "64Mi", "10m", "64Mi"}, // floors
		{"5m", "32Mi", "5m", "32Mi"},   // floors never exceed the limit
	}
	for _, tc := range cases {
		c := corev1.Container{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse(tc.cpu), corev1.ResourceMemory: resource.MustParse(tc.mem),
		}}}
		reserveRequests(&c, 10, 25)
		if got := c.Resources.Requests[corev1.ResourceCPU]; got.Cmp(resource.MustParse(tc.wantCPU)) != 0 {
			t.Errorf("cpu %s: got %s want %s", tc.cpu, got.String(), tc.wantCPU)
		}
		if got := c.Resources.Requests[corev1.ResourceMemory]; got.Cmp(resource.MustParse(tc.wantMem)) != 0 {
			t.Errorf("mem %s: got %s want %s", tc.mem, got.String(), tc.wantMem)
		}
	}

	// explicit requests are the author's call
	c := corev1.Container{Resources: corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("700m")},
	}}
	reserveRequests(&c, 10, 25)
	if got := c.Resources.Requests[corev1.ResourceCPU]; got.Cmp(resource.MustParse("700m")) != 0 {
		t.Errorf("explicit request overwritten: %s", got.String())
	}
}
