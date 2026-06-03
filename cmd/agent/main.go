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
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

func main() {
	cfg := config.Default()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	mp, err := newMeterProvider(ctx, cfg.OTLPEndpoint)
	if err != nil {
		log.Fatalf("meter provider: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mp.Shutdown(shutdownCtx)
	}()

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

	log.Printf("observa-agent started interval=%s otlp=%s", cfg.Interval, cfg.OTLPEndpoint)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	runCollect(ctx, cfg, pingSuccess, pingRTT, snmpSuccess, snmpUpTime)

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

func runCollect(
	ctx context.Context,
	cfg config.Config,
	pingSuccess, pingRTT, snmpSuccess, snmpUpTime metric.Float64Gauge,
) {
	for _, target := range cfg.Targets {
		attrs := metric.WithAttributes(
			attribute.String("device.id", target.DeviceID),
			attribute.String("device.site", target.Site),
			attribute.String("monitor.identify", "ConnectState"),
		)

		ping := collector.TCPProbe(ctx, target.Endpoint, 5*time.Second)
		if ping.Success {
			pingSuccess.Record(ctx, 1, attrs)
			pingRTT.Record(ctx, ping.RTT, attrs)
		} else {
			pingSuccess.Record(ctx, 0, attrs)
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

func newMeterProvider(ctx context.Context, otlpEndpoint string) (*sdkmetric.MeterProvider, error) {
	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(otlpEndpoint),
		otlpmetricgrpc.WithInsecure(),
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
			sdkmetric.WithInterval(15*time.Second),
		)),
	)
	otel.SetMeterProvider(mp)
	return mp, nil
}
