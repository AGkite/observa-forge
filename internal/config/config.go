package config

import (
	"os"
	"strings"
	"time"
)

// Target 表示一台被监测设备
type Target struct {
	DeviceID      string // 写入 metric label device.ids
	Endpoint      string // SNMP 地址，如 127.0.0.1:1161
	SNMPCommunity string // SNMP v2c community
	Site          string // 机房/站点
}

// Config 是 Agent 全局配置
type Config struct {
	OTLPEndpoint string
	Interval     time.Duration
	Targets      []Target
}

// Default 返回 Phase 0 默认配置，支持环境变量覆盖
func Default() Config {
	return Config{
		OTLPEndpoint: normalizeOTLPEndpoint(
			envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:4317"),
		),
		Interval: envDuration("OBSERVA_INTERVAL", 30 * time.Second),
		Targets: []Target{
			{
				DeviceID: envOr("OBSERVA_DEVICE_ID", "lab-snmpsim-01"),
				Endpoint: envOr("OBSERVA_SNMP_TARGET", "127.0.0.1:1161"),
				SNMPCommunity: envOr("OBSERVA_SNMP_COMMUNITY", "demo"),
				Site: envOr("OBSERVA_SITE", "lab"),
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

// normalizeOTLPEndpoint 去掉 gRPC 不需要的 http:// 前缀
func normalizeOTLPEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	return endpoint
}