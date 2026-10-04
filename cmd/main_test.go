package main

import (
	"testing"
	"time"

	"github.com/truenas/truenas-csi/pkg/driver"
)

// The deployment manifests configure metrics through the environment rather than
// the flag, because an older driver image ignores an unknown environment variable
// but exits on an unknown flag. Both paths have to work, with the flag winning when
// an operator passes it explicitly.
func TestLoadEnvConfig_MetricsAddr(t *testing.T) {
	tests := []struct {
		name string
		flag string
		env  string
		want string
	}{
		{"disabled by default", "", "", ""},
		{"from the environment", "", ":8080", ":8080"},
		{"from the flag", ":9090", "", ":9090"},
		{"flag wins over the environment", ":9090", ":8080", ":9090"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TRUENAS_URL", "wss://truenas.example")
			t.Setenv("TRUENAS_API_KEY", "key")
			t.Setenv("TRUENAS_DEFAULT_POOL", "tank")
			t.Setenv("TRUENAS_METRICS_ADDR", tt.env)

			config := &driver.DriverConfig{MetricsAddr: tt.flag}
			if err := loadEnvConfig(config); err != nil {
				t.Fatalf("loadEnvConfig() = %v, want nil", err)
			}
			if config.MetricsAddr != tt.want {
				t.Errorf("MetricsAddr = %q, want %q", config.MetricsAddr, tt.want)
			}
		})
	}
}

func TestLoadEnvConfig_ConnectionTuning(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c *driver.DriverConfig)
	}{
		{
			name: "unset keeps client defaults",
			check: func(t *testing.T, c *driver.DriverConfig) {
				if c.PingInterval != 0 || c.PingTimeout != 0 || c.DialTimeout != 0 || c.PingFailureThreshold != 0 {
					t.Errorf("expected zero values, got %+v", c)
				}
			},
		},
		{
			name: "all set",
			env: map[string]string{
				"TRUENAS_PING_INTERVAL":          "20s",
				"TRUENAS_PING_TIMEOUT":           "45s",
				"TRUENAS_DIAL_TIMEOUT":           "1m",
				"TRUENAS_PING_FAILURE_THRESHOLD": "3",
			},
			check: func(t *testing.T, c *driver.DriverConfig) {
				if c.PingInterval != 20*time.Second || c.PingTimeout != 45*time.Second ||
					c.DialTimeout != time.Minute || c.PingFailureThreshold != 3 {
					t.Errorf("unexpected config %+v", c)
				}
			},
		},
		{name: "bad duration", env: map[string]string{"TRUENAS_PING_TIMEOUT": "30"}, wantErr: true},
		{name: "non-positive duration", env: map[string]string{"TRUENAS_DIAL_TIMEOUT": "0s"}, wantErr: true},
		{name: "bad threshold", env: map[string]string{"TRUENAS_PING_FAILURE_THRESHOLD": "0"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TRUENAS_URL", "wss://truenas.example")
			t.Setenv("TRUENAS_API_KEY", "key")
			t.Setenv("TRUENAS_DEFAULT_POOL", "tank")
			for _, k := range []string{"TRUENAS_PING_INTERVAL", "TRUENAS_PING_TIMEOUT", "TRUENAS_DIAL_TIMEOUT", "TRUENAS_PING_FAILURE_THRESHOLD"} {
				t.Setenv(k, tt.env[k])
			}

			config := &driver.DriverConfig{}
			err := loadEnvConfig(config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadEnvConfig: %v", err)
			}
			tt.check(t, config)
		})
	}
}
