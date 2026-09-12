package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidateCreateNodeRequestAppliesDefaultsAndTrims(t *testing.T) {
	req := CreateNodeRequest{
		Name:          "  core  ",
		Hostname:      "  core.internal  ",
		IPAddress:     "  10.20.30.40  ",
		TotalVCPU:     8,
		TotalMemoryMB: 16384,
		TotalDiskGB:   200,
		APIEndpoint:   "  https://core.internal:8443/api  ",
	}

	if err := validateCreateNodeRequest(&req); err != nil {
		t.Fatalf("validateCreateNodeRequest() error = %v", err)
	}
	if req.Name != "core" || req.Hostname != "core.internal" || req.IPAddress != "10.20.30.40" {
		t.Fatalf("trimmed identity fields = %#v", req)
	}
	if req.MaxVMs != 10 || req.SSHPort != 22 || req.SSHUser != "anvil" {
		t.Fatalf("defaults not applied: max_vms=%d ssh_port=%d ssh_user=%q", req.MaxVMs, req.SSHPort, req.SSHUser)
	}
}

func TestValidateCreateNodeRequestRejectsUnsafeOrInvalidValues(t *testing.T) {
	base := CreateNodeRequest{
		Name:          "worker",
		Hostname:      "worker.internal",
		IPAddress:     "10.20.30.40",
		TotalVCPU:     8,
		TotalMemoryMB: 16384,
		TotalDiskGB:   200,
	}

	tests := []struct {
		name    string
		mutate  func(*CreateNodeRequest)
		wantErr string
	}{
		{name: "blank name", mutate: func(req *CreateNodeRequest) { req.Name = "  " }, wantErr: "required"},
		{name: "invalid hostname", mutate: func(req *CreateNodeRequest) { req.Hostname = "worker /tmp" }, wantErr: "hostname"},
		{name: "invalid address", mutate: func(req *CreateNodeRequest) { req.IPAddress = "not-an-ip" }, wantErr: "ip_address"},
		{name: "negative capacity", mutate: func(req *CreateNodeRequest) { req.TotalVCPU = -1 }, wantErr: "positive"},
		{name: "invalid SSH port", mutate: func(req *CreateNodeRequest) { req.SSHPort = 70000 }, wantErr: "ssh_port"},
		{name: "relative endpoint", mutate: func(req *CreateNodeRequest) { req.APIEndpoint = "/agent" }, wantErr: "api_endpoint"},
		{name: "endpoint credentials", mutate: func(req *CreateNodeRequest) { req.APIEndpoint = "https://user:pass@worker.internal" }, wantErr: "api_endpoint"},
		{name: "unsupported endpoint scheme", mutate: func(req *CreateNodeRequest) { req.APIEndpoint = "file:///tmp/socket" }, wantErr: "api_endpoint"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := base
			test.mutate(&req)
			err := validateCreateNodeRequest(&req)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestNilNodeHandlerReturnsServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes", nil)

	var handler *NodeHandler
	handler.List(ctx)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
