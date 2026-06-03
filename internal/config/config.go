package config

import (
	"os"
	"time"
)

type Target struct {
	DeviceID      string
	Endpoint      string
	SNMPCommunity string
	Site          string
}

type Config struct {
	OTLPEndpoint string
	Interval     time.Duration
	Targets      []Target
}

func Default() Config {
	return Config{
		OTLPEndpoint: envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		Interval:     envDuration("OBSERVA_INTERVAL", 30*time.Second),
		Targets: []Target{
			{
				DeviceID:      envOr("OBSERVA_DEVICE_ID", "lab-snmpsim-01"),
				Endpoint:      envOr("OBSERVA_SNMP_TARGET", "127.0.0.1:1161"),
				SNMPCommunity: envOr("OBSERVA_SNMP_COMMUNITY", "public"),
				Site:          envOr("OBSERVA_SITE", "lab"),
			},
		},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
