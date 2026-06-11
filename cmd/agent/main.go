package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/observa-forge/observa-forge/internal/collector"
	"github.com/observa-forge/observa-forge/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func main() {
	cfg := config.Default()

	// 将 SIGINT/SIGTERM 转为可取消的 context，Ctrl+C 时 ctx.Done() 关闭
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	mp, err := newMeterProvider(ctx, cfg.OTLPEndpoint)
	if err != nil {
		log.Fatalf("meter provider: %v", err)
	}
	// 进程退出前 flush 未发送的 metric；独立 5s 超时，不依赖可能已取消的主 ctx
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mp.Shutdown(shutdownCtx)
	}()

	// Meter 是指标工厂；Gauge 记录当前瞬时值（非累加 Counter）
	meter := mp.Meter("observa-agent")

	pingSuccess, _ := meter.Float64Gauge("hardware.ping.success",
		metric.WithDescription("Ping/connect probe success (1=ok, 0=fail)"))
	pingRTT, _ := meter.Float64Gauge("hardware.ping.rtt",
		metric.WithDescription("Probe round-trip time"),
		metric.WithUnit("s"))
	snmpSuccess, _ := meter.Float64Gauge("hardware.snmp.success",
		metric.WithDescription("SNMP system MIB probe success"))
	snmpUpTime, _ := meter.Float64Gauge("hardware.snmp.sysuptime",
		metric.WithDescription("SNMP sysUpTime"),
		metric.WithUnit("s"))

	log.Printf("observa-agent started interval=%s oltp=%s", cfg.Interval, cfg.OTLPEndpoint)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	runCollect(ctx, cfg, pingSuccess, pingRTT, snmpSuccess, snmpUpTime)

	// select 多路复用：信号取消 或 定时 tick，二者任一就绪即唤醒
	for {
		select {
		case <-ctx.Done():
			log.Println("observa-agent stopped")
			return
		case <-ticker.C:
			runCollect(ctx, cfg, pingSuccess, pingRTT, snmpSuccess, snmpUpTime)
		}
	}
}

// runCollect 遍历所有 Target，分别做 TCP 连通探测和 SNMP 采集并上报 Gauge
func runCollect(
	ctx context.Context,
	cfg config.Config,
	pingSuccess, pingRTT, snmpSuccess, snmpUpTime metric.Float64Gauge,
) {
	for _, target := range cfg.Targets {
		// WithAttributes 为每次 Record 附加 label，便于按设备/站点筛选
		pingAttrs := metric.WithAttributes(
			attribute.String("device.id", target.DeviceID),
			attribute.String("device.site", target.Site),
			attribute.String("monitor.identify", "ConnectState"),
		)

		ping := collector.TCPProbe(ctx, target.Endpoint, 5*time.Second)
		if ping.Success {
			pingSuccess.Record(ctx, 1, pingAttrs)
			pingRTT.Record(ctx, ping.RTT, pingAttrs)
		} else {
			pingSuccess.Record(ctx, 0, pingAttrs)
		}

		snmpAttrs := metric.WithAttributes(
			attribute.String("device.id", target.DeviceID),
			attribute.String("device.site", target.Site),
			attribute.String("monitor.identify", "SNMPSystem"),
		)
		snmp := collector.CollectSNMP(target.Endpoint, target.SNMPCommunity, 5*time.Second)
		if snmp.Success {
			snmpSuccess.Record(ctx, 1, snmpAttrs)
			snmpUpTime.Record(ctx, snmp.SysUpTime, snmpAttrs)
		} else {
			snmpSuccess.Record(ctx, 0, snmpAttrs)
		}

		log.Printf("collect device=%s ping_ok=%v rtt=%.3f snmp_ok=%v sysUpTime=%.1f",
			target.DeviceID, ping.Success, ping.RTT, snmp.Success, snmp.SysUpTime)
	}
}

// newMeterProvider 创建 OTLP gRPC exporter + 周期性 Reader，并注册为全局 MeterProvider
func newMeterProvider(ctx context.Context, otlpEndpoint string) (*sdkmetric.MeterProvider, error) {
	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(otlpEndpoint),
		otlpmetricgrpc.WithInsecure(), // Phase 0 本地明文 gRPC，生产应启用 TLS
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("observa-agent"),
			attribute.String("observaforge.phase", "phase0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(15*time.Second), // 每 15s 批量推送 metric 到 Collector
		)),
	)
	otel.SetMeterProvider(mp) // 让 otel.Meter() 等全局 API 使用此 Provider
	return mp, nil
}
