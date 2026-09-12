package handlers

import "testing"

func TestValidatePlatformSetting(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   interface{}
		wantErr bool
	}{
		{name: "instance limit", key: "instance.max_per_user", value: float64(5)},
		{name: "fractional instance limit", key: "instance.max_per_user", value: 1.5, wantErr: true},
		{name: "zero instance limit", key: "instance.max_per_user", value: float64(0), wantErr: true},
		{name: "extension duration", key: "instance.extension_minutes", value: float64(30)},
		{name: "oversized extension", key: "instance.extension_minutes", value: float64(1441), wantErr: true},
		{name: "scoreboard boolean", key: "scoreboard_enabled", value: true},
		{name: "scoreboard string", key: "scoreboard_enabled", value: "true", wantErr: true},
		{name: "registration mode", key: "registration_mode", value: "invite"},
		{name: "bad registration mode", key: "registration_mode", value: "yes", wantErr: true},
		{name: "legacy setting remains writable", key: "platform_name", value: "Anvil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePlatformSetting(tt.key, tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePlatformSetting(%q, %#v) error = %v, wantErr %v", tt.key, tt.value, err, tt.wantErr)
			}
		})
	}
}
