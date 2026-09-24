package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRoutingConfig(t *testing.T) {
	t.Setenv("SIGNA_ROUTING_PROVIDER", " osrm ")
	t.Setenv("SIGNA_ROUTING_BASE_URL", " http://routing.test/ ")
	t.Setenv("SIGNA_ROUTING_TIMEOUT", "1500ms")

	got, err := LoadRoutingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "osrm" || got.BaseURL != "http://routing.test/" || got.Timeout != 1500*time.Millisecond {
		t.Fatalf("routing config = %+v", got)
	}
}

func TestLoadRoutingConfigRequiresExplicitNetworkSettings(t *testing.T) {
	for _, test := range []struct {
		name    string
		base    string
		timeout string
		want    string
	}{
		{name: "base URL", timeout: "1s", want: "SIGNA_ROUTING_BASE_URL"},
		{name: "timeout", base: "http://routing.test", want: "SIGNA_ROUTING_TIMEOUT"},
		{name: "invalid timeout", base: "http://routing.test", timeout: "soon", want: "SIGNA_ROUTING_TIMEOUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SIGNA_ROUTING_BASE_URL", test.base)
			t.Setenv("SIGNA_ROUTING_TIMEOUT", test.timeout)
			_, err := LoadRoutingConfig()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
