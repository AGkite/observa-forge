package collector

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/gosnmp/gosnmp"
)

type PingResult struct {
	Success bool
	RTT     float64
}

// TCPProbe uses TCP connect as a connectivity check (no root required on Windows).
func TCPProbe(ctx context.Context, hostPort string, timeout time.Duration) PingResult {
	start := time.Now()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return PingResult{Success: false, RTT: 0}
	}
	_ = conn.Close()
	return PingResult{Success: true, RTT: time.Since(start).Seconds()}
}

type SNMPSystem struct {
	SysDescr  string
	SysUpTime float64
	Success   bool
}

func CollectSNMP(hostPort, community string, timeout time.Duration) SNMPSystem {
	host, port := splitHostPort(hostPort)
	params := &gosnmp.GoSNMP{
		Target:    host,
		Port:      port,
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   timeout,
		Retries:   1,
	}
	if err := params.Connect(); err != nil {
		return SNMPSystem{Success: false}
	}
	defer params.Conn.Close()

	oids := []string{
		"1.3.6.1.2.1.1.1.0", // sysDescr
		"1.3.6.1.2.1.1.3.0", // sysUpTime
	}
	result, err := params.Get(oids)
	if err != nil || len(result.Variables) < 2 {
		return SNMPSystem{Success: false}
	}

	sysDescr := fmt.Sprintf("%v", result.Variables[0].Value)
	ticks := gosnmp.ToBigInt(result.Variables[1].Value).Uint64()
	return SNMPSystem{
		SysDescr:  sysDescr,
		SysUpTime: float64(ticks) / 100,
		Success:   true,
	}
}

func splitHostPort(hostPort string) (string, uint16) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort, 161
	}
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return host, 161
	}
	return host, uint16(p)
}
