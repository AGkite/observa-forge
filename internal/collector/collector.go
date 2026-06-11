package collector

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
	"github.com/gosnmp/gosnmp"
)

// PingResult 是一次连通性探测的结果
type PingResult struct {
	Success bool
	RTT     float64 // 秒
}

// TCPProbe 用 TCP 连接代替 ICMP Ping（Windows 上 ping 常需管理员权限）
func TCPProbe(ctx context.Context, hostPort string, timeout time.Duration) PingResult {
	start := time.Now()
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return PingResult{Success: false, RTT: 0}
	}
	_ = conn.Close()
	return PingResult{Success: true, RTT: time.Since(start).Seconds()}
}

// SNMPSystem 是 SNMP system 组 MIB 的采集结果
type SNMPSystem struct {
	SysDescr string
	SysUpTime float64 // 秒
	Success   bool
}

// CollectSNMP 对 hostPort 执行 SNMP v2c Get，读取 sysDescr 与 sysUpTime
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
		"1.3.6.1.2.1.1.1.0", // sysDescr - 系统描述
		"1.3.6.1.2.1.1.3.0", // sysUpTime - 运行时间（百分之一秒）
	}
	result, err := params.Get(oids)
	if err != nil || len(result.Variables) < 2 {
		return SNMPSystem{Success: false}
	}

	sysDescr := fmt.Sprintf("%v", result.Variables[0].Value)
	ticks := gosnmp.ToBigInt(result.Variables[1].Value).Uint64()
	return SNMPSystem{
		SysDescr:  sysDescr,
		SysUpTime: float64(ticks) / 100, // TimeTicks → 秒
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
