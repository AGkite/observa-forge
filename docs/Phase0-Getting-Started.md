# ObservaForge Phase 0 实战指南

> **目标**: 在本地用 Docker Compose 拉起 **VictoriaMetrics + Grafana + OTel Collector + PostgreSQL**，并实现第一个 **Go 采集器**（`ping` + `snmp`），跑通「采集 → OTLP → 存储 → 查询」全链路。  
> **预计耗时**: 4～8 小时（含阅读与实验）  
> **前置文档**: [ObservaForge-Architecture.md](./ObservaForge-Architecture.md)

---

## 目录

1. [Phase 0 要达成什么](#1-phase-0-要达成什么)
2. [核心概念速览（必读）](#2-核心概念速览必读)
3. [环境准备](#3-环境准备)
4. [Step 1 — 启动基础设施](#4-step-1--启动基础设施)
5. [Step 2 — 理解 docker-compose 与端口](#5-step-2--理解-docker-compose-与端口)
6. [Step 3 — OTel Collector 配置详解](#6-step-3--otel-collector-配置详解)
7. [Step 4 — VictoriaMetrics 与 PromQL](#7-step-4--victoriametrics-与-promql)
8. [Step 5 — Grafana 可视化](#8-step-5--grafana-可视化)
9. [Step 6 — PostgreSQL 元数据骨架](#9-step-6--postgresql-元数据骨架)
10. [Step 7 — 编写 observa-agent（Go）](#10-step-7--编写-observa-agentgo)
11. [Step 8 — 联调验证清单](#11-step-8--联调验证清单)
12. [Step 9 — PromQL 实验题](#12-step-9--promql-实验题)
13. [Step 10 — 故障排查](#13-step-10--故障排查)
14. [Phase 0 完成标准与下一步](#14-phase-0-完成标准与下一步)

---

## 1. Phase 0 要达成什么

Phase 0 是 **学习/验证阶段**，不追求生产可用，但要亲手跑通 ObservaForge 的数据主路径：

```
┌─────────────┐   OTLP gRPC    ┌──────────────────┐  remote_write  ┌─────────────────┐
│ observa-agent│ ────────────► │ OTel Collector   │ ─────────────► │ VictoriaMetrics │
│ (Go 进程)    │   :4317       │ (Gateway)        │                │ (时序库)         │
└──────┬──────┘               └──────────────────┘                └────────┬────────┘
       │ ping / snmp                                                      │ PromQL
       ▼                                                                  ▼
┌─────────────┐                                                    ┌─────────────────┐
│ snmpsim     │                                                    │ Grafana         │
│ (模拟 SNMP) │                                                    │ (Dashboard)     │
└─────────────┘                                                    └─────────────────┘
```

**Phase 0 交付物**:

| 交付物 | 路径 |
|--------|------|
| Docker Compose 栈 | `deploy/phase0/docker-compose.yml` |
| OTel Collector 配置 | `deploy/phase0/otel-collector/config.yaml` |
| Grafana 数据源 | 自动 provisioning |
| PostgreSQL 设备表 | `deploy/phase0/postgres/init.sql` |
| Go 采集 Agent | `cmd/agent/`（本指南 Step 7 创建） |

**与 DCOS 的对应关系**（帮助理解「我们在重现什么」）:

| DCOS 概念 | Phase 0 对应 |
|-----------|--------------|
| `MonitorTask` 轮询 | Go Agent 定时 `Collect()` |
| `MonitorResult` / `Meta` | OTel Metric + attributes |
| `DataParser` 写库 | OTel Collector → VM remote_write |
| OOBS Web 看曲线 | Grafana Explore |

---

## 2. 核心概念速览（必读）

### 2.1 三类可观测性信号

| 信号 | 回答的问题 | Phase 0 涉及 |
|------|-----------|--------------|
| **Metrics（指标）** | 数值随时间变化（CPU、温度、Ping 延迟） | ✅ 主线 |
| **Logs（日志）** | 离散事件文本 | ❌ Phase 1 引入 Loki |
| **Traces（链路）** | 请求跨服务路径 | ❌ Phase 1 引入 Tempo |

SRE 日常排障顺序通常是：**Metrics 发现异常 → Logs 看细节 → Traces 定位慢路径**。

### 2.2 OpenTelemetry（OTel）

OTel 是 **CNCF 可观测性标准**，解决「各语言 SDK 格式不统一」的问题。

| 概念 | 说明 |
|------|------|
| **SDK** | 应用/Agent 内嵌，产生 telemetry |
| **OTLP** | OpenTelemetry Protocol，默认传输协议（gRPC 或 HTTP） |
| **Collector** | 接收 → 处理 → 导出，类似 Logstash 之于日志 |
| **Semantic Conventions** | 统一 metric/attribute 命名（如 `hw.temperature`） |

**Phase 0 用的端口**:

| 端口 | 协议 | 用途 |
|------|------|------|
| `4317` | gRPC | OTLP 指标/日志/链路（Agent 默认推这里） |
| `4318` | HTTP | OTLP HTTP 备选 |
| `8888` | HTTP | Collector 自身 Prometheus metrics |

### 2.3 Prometheus 与 VictoriaMetrics

**Prometheus** 拉（Pull）模型为主：Prometheus Server 主动 `scrape` 目标的 `/metrics` 端点。

**VictoriaMetrics（VM）** 兼容 PromQL，但写入路径更适合 **remote_write**（Push），吞吐和压缩更好。ObservaForge 选型 VM 作为主时序库（见架构文档 ADR-001 草案）。

| 操作 | URL / 命令 |
|------|-----------|
| 写入（remote_write） | `POST /api/v1/write` |
| 查询（PromQL） | `GET /api/v1/query?query=...` |
| 范围查询 | `GET /api/v1/query_range?query=...&start=...&end=...&step=15s` |
| 查看已存 series | `GET /api/v1/series?match[]=...` |

### 2.4 指标数据模型

Prometheus/VM 中每条时间序列由 **metric 名 + labels** 唯一确定：

```
hardware_ping_rtt_seconds{device_id="lab-snmpsim-01", site="lab"} 0.002
│                         │                                      │
│                         └── labels（维度）                      └── value
└── metric name
```

这与 DCOS `MonitorResult` 中的 `Meta.identify` + `Meta.part` + `perfs` 类似：  
**identify → metric 名前缀，part/device → labels，perf 值 → value**。

---

## 3. 环境准备

### 3.1 硬件与系统

| 项目 | 最低要求 |
|------|----------|
| CPU | 4 核 |
| 内存 | 8 GB（Docker 分配 ≥ 4 GB） |
| 磁盘 | 10 GB 可用 |
| OS | Windows 10/11 + WSL2，或 Linux/macOS |

### 3.2 必装软件

#### Docker Desktop（Windows）

```powershell
# 检查 Docker 是否可用
docker version

# 检查 Compose V2（注意是 "docker compose" 不是 "docker-compose"）
docker compose version
```

**参数说明**:

| 命令 | 输出含义 |
|------|----------|
| `docker version` | Client/Server 版本；Server 报错说明 Docker Desktop 未启动 |
| `docker compose version` | 需 ≥ 2.20；V2 集成在 Docker CLI 中 |

Windows 需在 Docker Desktop → Settings → Resources 中分配 **Memory ≥ 4GB**，并启用 **WSL2 backend**。

#### Go 1.22+

```powershell
go version
# 期望: go version go1.22.x 或更高

go env GOPATH GO111MODULE
```

| 变量 | 说明 |
|------|------|
| `GOPATH` | Go 工作区路径，默认 `%USERPROFILE%\go` |
| `GO111MODULE` | 应为 `on`（Go 1.16+ 默认） |

#### Git

```powershell
git clone https://github.com/<your-org>/observa-forge.git
cd observa-forge
```

#### 可选工具

```powershell
# Windows 包管理
winget install Schniz.fnm          # Node（后续 Console 用）
winget install PostgreSQL.psql     # psql 客户端

# 或用 Docker 内 psql（本指南默认用 docker exec）
```

### 3.3 克隆后目录结构

```
observa-forge/
├── deploy/phase0/          ← Phase 0 基础设施（已提供）
├── docs/
│   ├── ObservaForge-Architecture.md
│   └── Phase0-Getting-Started.md   ← 本文档
├── cmd/agent/              ← Step 7 创建
└── README.md
```

---

## 4. Step 1 — 启动基础设施

### 4.1 进入 Phase 0 部署目录

```powershell
cd c:\work\observa-forge\deploy\phase0
```

### 4.2 拉取镜像并启动

```powershell
# 前台启动（首次建议，方便看日志）
docker compose up

# 或后台启动
docker compose up -d
```

**`docker compose up` 参数解析**:

| 参数 | 含义 |
|------|------|
| `-d` / `--detach` | 后台运行，不占用当前终端 |
| `--build` | 启动前构建本地 Dockerfile（Phase 0 无自定义镜像，暂不需要） |
| `-f <file>` | 指定 compose 文件路径 |
| `--pull always` | 每次启动拉最新镜像 |
| `up` | 创建网络、卷、容器并启动 |

### 4.3 检查容器状态

```powershell
docker compose ps
```

期望输出（STATE 均为 `running`）:

| NAME | PORTS | 说明 |
|------|-------|------|
| `of-vm` | `8428` | VictoriaMetrics |
| `of-otel-collector` | `4317-4318`, `8888` | OTel Collector |
| `of-grafana` | `3000` | Grafana |
| `of-postgres` | `5432` | PostgreSQL |
| `of-snmpsim` | `1161/udp` | SNMP 模拟器 |

```powershell
# 查看某个服务日志
docker compose logs otel-collector --tail 50 -f
```

**`docker compose logs` 参数**:

| 参数 | 含义 |
|------|------|
| `<service>` | 服务名（compose 文件中 `services:` 下的 key） |
| `--tail 50` | 只显示最后 50 行 |
| `-f` / `--follow` | 持续跟踪新日志（Ctrl+C 退出） |
| `--since 10m` | 只看最近 10 分钟 |

### 4.4 健康检查命令

```powershell
# VictoriaMetrics 健康
curl http://localhost:8428/health
# 期望: OK

# OTel Collector 健康扩展
curl http://localhost:13133/
# 期望: JSON status "Server available"

# Grafana 健康
curl http://localhost:3000/api/health
# 期望: {"database":"ok",...}
```

PowerShell 若无 `curl`，可用：

```powershell
Invoke-WebRequest -Uri http://localhost:8428/health -UseBasicParsing
```

---

## 5. Step 2 — 理解 docker-compose 与端口

配置文件: `deploy/phase0/docker-compose.yml`

### 5.1 VictoriaMetrics 服务

```yaml
command:
  - "-storageDataPath=/victoria-metrics-data"
  - "-retentionPeriod=7d"
  - "-httpListenAddr=:8428"
  - "-promscrape.config=/etc/prometheus/prometheus.yml"
```

| 启动参数 | 含义 |
|----------|------|
| `-storageDataPath` | 数据文件目录（挂载 Docker volume 持久化） |
| `-retentionPeriod=7d` | 数据保留 7 天（Phase 0 实验足够） |
| `-httpListenAddr=:8428` | HTTP 监听地址（查询 + 写入 + `/metrics`） |
| `-promscrape.config` | 内置 scrape 配置，Phase 0 用于采集 VM 自身和 OTel metrics |

### 5.2 OTel Collector 服务

```yaml
image: otel/opentelemetry-collector-contrib:0.109.0
command: ["--config=/etc/otelcol/config.yaml"]
```

| 项 | 说明 |
|----|------|
| `contrib` 镜像 | 包含 `prometheusremotewrite` 等扩展组件；核心版不含 |
| `--config` | Collector 配置文件路径（唯一必填启动参数） |

### 5.3 Grafana 服务

```yaml
environment:
  GF_SECURITY_ADMIN_USER: admin
  GF_SECURITY_ADMIN_PASSWORD: observaforge
```

| 环境变量 | 含义 |
|----------|------|
| `GF_SECURITY_ADMIN_USER` | 管理员用户名 |
| `GF_SECURITY_ADMIN_PASSWORD` | 管理员密码（Phase 0 明文，生产必须用 Secret） |
| `GF_USERS_ALLOW_SIGN_UP` | 禁止自助注册 |

登录: http://localhost:3000 → `admin` / `observaforge`

### 5.4 PostgreSQL 服务

```yaml
environment:
  POSTGRES_USER: observa
  POSTGRES_PASSWORD: observaforge
  POSTGRES_DB: observa_forge
```

| 环境变量 | 含义 |
|----------|------|
| `POSTGRES_USER` | 超级用户（Phase 0 简化） |
| `POSTGRES_DB` | 启动时自动创建的数据库名 |
| `init.sql` | 首次启动时自动执行（`docker-entrypoint-initdb.d/`） |

### 5.5 snmpsim（SNMP 模拟器）

无真实交换机/服务器 SNMP 时，用此容器模拟 **`127.0.0.1:1161`** 上的 SNMP Agent。

```powershell
# 宿主机测试 SNMP（需安装 Net-SNMP 工具，或用 Docker）
docker run --rm -it --network observa-forge-phase0_default nicolaka/netshoot \
  snmpwalk -v2c -c public host.docker.internal:1161 1.3.6.1.2.1.1
```

**`snmpwalk` 参数**:

| 参数 | 含义 |
|------|------|
| `-v2c` | SNMP 版本 2c |
| `-c public` | Community 字符串（口令，v2c 无加密） |
| `host:1161` | Agent 地址；容器内访问宿主机 snmpsim 用 `host.docker.internal:1161` |
| `1.3.6.1.2.1.1` | OID 子树（`system` 组，含 sysDescr、sysUpTime 等） |

---

## 6. Step 3 — OTel Collector 配置详解

配置文件: `deploy/phase0/otel-collector/config.yaml`

### 6.1 整体结构

```yaml
receivers:    # 数据从哪来
processors:   # 中间怎么处理
exporters:    # 数据往哪去
extensions:   # 扩展（健康检查、pprof 等）
service:      # 组装 pipeline
```

**Pipeline** 是有向图：`receiver → processor(s) → exporter(s)`。

Phase 0 只有一条 metrics pipeline:

```
otlp → memory_limiter → batch → attributes → prometheusremotewrite
                                            → debug
```

### 6.2 receivers.otlp

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318
```

| 字段 | 含义 |
|------|------|
| `0.0.0.0:4317` | 监听所有网卡；Agent 在宿主机运行时连 `localhost:4317` |
| gRPC vs HTTP | Go SDK 默认 gRPC；调试用 HTTP 可用 `4318` |

### 6.3 processors

#### batch

```yaml
batch:
  timeout: 5s
  send_batch_size: 512
```

| 字段 | 含义 |
|------|------|
| `timeout` | 最长等待 5 秒即发送一批（即使未满） |
| `send_batch_size` | 单批最多 512 条，减少 HTTP 请求次数 |

**为何需要 batch**: 每条 metric 单独 POST 会导致 VM 写入压力过大；批量可提升吞吐 10～100 倍。

#### memory_limiter

```yaml
memory_limiter:
  check_interval: 1s
  limit_mib: 256
  spike_limit_mib: 64
```

防止 Collector 内存暴涨被 OOM Kill；超过 `limit_mib` 会反压（backpressure）拒绝新数据。

#### attributes

```yaml
attributes:
  actions:
    - key: observaforge.phase
      value: phase0
      action: insert
```

给所有经过的 metric 插入固定 label，便于 Grafana 中过滤实验数据。

### 6.4 exporters.prometheusremotewrite

```yaml
exporters:
  prometheusremotewrite:
    endpoint: http://victoria-metrics:8428/api/v1/write
    tls:
      insecure: true
```

| 字段 | 含义 |
|------|------|
| `endpoint` | VM 的 remote_write URL；注意容器内用服务名 `victoria-metrics` |
| `tls.insecure: true` | 跳过 TLS 验证（Phase 0 无 HTTPS） |

**数据格式**: Prometheus remote write protocol（Protobuf + Snappy 压缩），OTel Collector 自动将 OTLP metrics 转换。

### 6.5 验证 Collector 收到数据

启动 Agent 后，查看 Collector 日志：

```powershell
docker compose logs otel-collector -f
```

应看到类似：

```
MetricsExporter ... "resource metrics": ...
```

也可查 Collector 自身 metrics:

```powershell
curl http://localhost:8888/metrics | Select-String "otelcol_receiver"
```

关键指标:

| 指标 | 含义 |
|------|------|
| `otelcol_receiver_accepted_metric_points` | 成功接收的 metric 点数 |
| `otelcol_exporter_sent_metric_points` | 成功导出的点数 |
| `otelcol_exporter_send_failed_metric_points` | 导出失败（应 = 0） |

---

## 7. Step 4 — VictoriaMetrics 与 PromQL

### 7.1 查询 API

```powershell
# 即时查询 — 当前值
curl --globoff "http://localhost:8428/api/v1/query?query=up"

# 范围查询 — 过去 1 小时，每 15 秒一个点
$end = [int][double]::Parse((Get-Date -UFormat %s))
$start = $end - 3600
curl --globoff "http://localhost:8428/api/v1/query_range?query=up&start=$start&end=$end&step=15"
```

**`/api/v1/query` 参数**:

| 参数 | 必填 | 含义 |
|------|------|------|
| `query` | ✅ | PromQL 表达式 |
| `time` | ❌ | 评估时间点（Unix 秒），默认 now |
| `dedup` | ❌ | 去重开关 |

**`/api/v1/query_range` 参数**:

| 参数 | 必填 | 含义 |
|------|------|------|
| `query` | ✅ | PromQL |
| `start` | ✅ | 范围起始（Unix 秒） |
| `end` | ✅ | 范围结束 |
| `step` | ✅ | 步长（如 `15s`、`1m`） |

### 7.2 查看所有 label

```powershell
curl "http://localhost:8428/api/v1/labels"
curl --globoff "http://localhost:8428/api/v1/series?match[]={observaforge_phase=\"phase0\"}"
```

### 7.3 常用 PromQL 速查

| 表达式 | 含义 |
|--------|------|
| `up` | scrape 目标是否存活（1=正常） |
| `hardware_ping_success` | Phase 0 Agent 上报的 Ping 状态 |
| `rate(hardware_snmp_sysuptime[5m])` | sysUpTime 变化率（应用需谨慎，sysUpTime 是累计值） |
| `{__name__=~"hardware_.*"}` | 所有 hardware_ 前缀指标 |

---

## 8. Step 5 — Grafana 可视化

### 8.1 登录与数据源

1. 打开 http://localhost:3000
2. 用户名 `admin`，密码 `observaforge`
3. 左侧 **Connections → Data sources → VictoriaMetrics** 应已自动配置（provisioning）

数据源 URL 为 `http://victoria-metrics:8428`（容器内网络），**不要**改成 `localhost`（那是 Grafana 容器自身）。

### 8.2 Explore 快速查指标

1. 左侧 **Explore**
2. 数据源选 **VictoriaMetrics**
3. 输入 PromQL:

```promql
hardware_ping_rtt_seconds
```

4. 点击 **Run query**

### 8.3 创建 Phase 0 Dashboard 面板

**Panel 1 — Ping 延迟（Time series）**:

```promql
hardware_ping_rtt_seconds{device_id="lab-snmpsim-01"}
```

**Panel 2 — Ping 成功率（Stat）**:

```promql
hardware_ping_success{device_id="lab-snmpsim-01"}
```

**Panel 3 — SNMP sysUpTime（Time series）**:

```promql
hardware_snmp_sysuptime_seconds{device_id="lab-snmpsim-01"}
```

**Panel 4 — OTel Collector 吞吐**:

```promql
rate(otelcol_receiver_accepted_metric_points[1m])
```

| Grafana 选项 | 建议值 |
|--------------|--------|
| Min interval | `15s`（与采集间隔对齐） |
| Legend | `{{device_id}} - {{monitor_identify}}` |
| Unit（Ping RTT） | seconds (s) |
| Unit（sysUpTime） | seconds (s) |

---

## 9. Step 6 — PostgreSQL 元数据骨架

Phase 0 只验证 **元数据表存在**，Phase 2 的 `observa-control` 会读写此库。

### 9.1 连接数据库

```powershell
docker exec -it of-postgres psql -U observa -d observa_forge
```

**`psql` 启动参数**:

| 参数 | 含义 |
|------|------|
| `-U observa` | 用户名 |
| `-d observa_forge` | 数据库名 |
| `-it` | 交互 + TTY |

### 9.2 验证表与样例数据

```sql
-- 查看设备表
SELECT * FROM devices;

-- 期望一行: lab-snmpsim-01 / SNMP Simulator
```

### 9.3 与 DCOS Device 的映射

| PostgreSQL 列 | DCOS `Device` 字段 |
|---------------|-------------------|
| `device_code` | 设备唯一编码 |
| `node_class` | `nodeclass` |
| `endpoint` | IP:Port |
| `site` | 机房/站点 |

Phase 0 Agent 的 `device_id` label 应与 `device_code` 一致，便于后续 Control Plane 关联。

退出 psql: `\q`

---

## 10. Step 7 — 编写 observa-agent（Go）

这是 Phase 0 的核心：**第一个 Go 采集器**，实现 `ping` + `snmp`（sysDescr/sysUpTime），经 OTLP 上报。

### 10.1 初始化 Go 模块

在项目根目录:

```powershell
cd c:\work\observa-forge

go mod init github.com/observa-forge/observa-forge
```

### 10.2 安装依赖

```powershell
go get go.opentelemetry.io/otel@v1.32.0
go get go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@v1.32.0
go get go.opentelemetry.io/otel/sdk/metric@v1.32.0
go get github.com/gosnmp/gosnmp@v1.38.0
```

| 依赖 | 用途 |
|------|------|
| `otel` | OpenTelemetry API |
| `otlpmetricgrpc` | OTLP gRPC 导出器 |
| `sdk/metric` | MeterProvider、PeriodicReader |
| `gosnmp` | SNMP 协议客户端 |

### 10.3 创建目录

```powershell
mkdir -Force cmd\agent, internal\collector, internal\config
```

### 10.4 配置文件 `internal/config/config.go`

```go
package config

import "time"

type Target struct {
	DeviceID   string
	Endpoint   string // host:port
	SNMPCommunity string
}

type Config struct {
	OTLPEndpoint string
	Interval     time.Duration
	Targets      []Target
}

func Default() Config {
	return Config{
		OTLPEndpoint: "localhost:4317",
		Interval:     30 * time.Second,
		Targets: []Target{
			{
				DeviceID:      "lab-snmpsim-01",
				Endpoint:      "127.0.0.1:1161",
				SNMPCommunity: "public",
			},
		},
	}
}
```

### 10.5 Ping 与 SNMP 采集器 `internal/collector/collector.go`

Phase 0 将 ping（TCP 探测）与 SNMP 采集放在同一文件，完整代码见仓库 `internal/collector/collector.go`。
> - `1.3.6.1.2.1.1.1.0` → `sysDescr`（系统描述）
> - `1.3.6.1.2.1.1.3.0` → `sysUpTime`（运行时间，TimeTicks）

### 10.6 主程序 `cmd/agent/main.go`

```powershell
# 从仓库复制完整 main.go 后执行:
go run ./cmd/agent
```

**环境变量（可选覆盖）**:

| 变量 | 默认 | 含义 |
|------|------|------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC 地址 |
| `OBSERVA_INTERVAL` | `30s` | 采集间隔 |
| `OBSERVA_SNMP_TARGET` | `127.0.0.1:1161` | SNMP 目标 |
| `OBSERVA_DEVICE_ID` | `lab-snmpsim-01` | device_id label |

**运行命令**:

```powershell
# 确保 docker compose 已启动
cd c:\work\observa-forge

$env:OTEL_EXPORTER_OTLP_ENDPOINT = "localhost:4317"
go run ./cmd/agent
```

期望输出（每 30 秒）:

```
observa-agent started interval=30s otlp=localhost:4317
collect device=lab-snmpsim-01 ping_ok=true rtt=0.001 snmp_ok=true sysUpTime=12345.6
```

### 10.7 指标命名（对齐架构文档）

| OTel Metric | Type | Labels | DCOS 对应 |
|-------------|------|--------|-----------|
| `hardware.ping.success` | Gauge | `device_id`, `site` | ConnectState |
| `hardware.ping.rtt` | Gauge | `device_id` | Ping 延迟 perf |
| `hardware.snmp.success` | Gauge | `device_id` | SNMP 采集状态 |
| `hardware.snmp.sysuptime` | Gauge | `device_id` | 系统运行时间 |

OTel 导出到 Prometheus/VM 时，`.` 通常变为 `_`：

```
hardware_ping_success
hardware_ping_rtt_seconds
hardware_snmp_sysuptime_seconds
```

---

## 11. Step 8 — 联调验证清单

按顺序逐项打勾:

### 11.1 基础设施

- [ ] `docker compose ps` 五个容器均为 `running`
- [ ] `curl http://localhost:8428/health` 返回 `OK`
- [ ] `curl http://localhost:13133/` Collector 健康
- [ ] Grafana http://localhost:3000 可登录

### 11.2 SNMP 模拟器

- [ ] `snmpwalk` 能返回 sysDescr（见 Step 5.5）

### 11.3 Agent → Collector → VM

- [ ] `go run ./cmd/agent` 无报错
- [ ] Collector 日志有 `MetricsExporter` 输出
- [ ] 查询有数据:

```powershell
curl --globoff "http://localhost:8428/api/v1/query?query=hardware_ping_success"
```

返回 `"value":[..., "1"]` 表示成功。

### 11.4 Grafana

- [ ] Explore 中 `hardware_ping_rtt_seconds` 有曲线
- [ ] Dashboard 至少 3 个 Panel 正常

### 11.5 PostgreSQL

- [ ] `SELECT * FROM devices` 有 `lab-snmpsim-01`

---

## 12. Step 9 — PromQL 实验题

完成以下练习（答案在 Grafana Explore 验证）:

### 练习 1 — 过滤

查出 `device_id="lab-snmpsim-01"` 的 Ping RTT:

```promql
hardware_ping_rtt_seconds{device_id="lab-snmpsim-01"}
```

### 练习 2 — 布尔判断

Ping 失败时值为 0，写表达式判断「当前是否失败」:

```promql
hardware_ping_success == 0
```

### 练习 3 — 5 分钟平均 RTT

```promql
avg_over_time(hardware_ping_rtt_seconds[5m])
```

| 函数 | 含义 |
|------|------|
| `avg_over_time(v[5m])` | 过去 5 分钟内 v 的平均值 |
| `[5m]` | 范围向量（range vector） |

### 练习 4 — Collector 接收速率

```promql
rate(otelcol_receiver_accepted_metric_points[1m])
```

| 函数 | 含义 |
|------|------|
| `rate(v[1m])` | 每秒平均增长率（Counter 专用） |

### 练习 5 — 多指标关联（预留）

思考: 若 Ping 失败且 SNMP 也失败，可能是网络问题还是 Agent 问题？  
Phase 3 AIOps 会用此类模式做 **关联规则**。

---

## 13. Step 10 — 故障排查

### 13.1 Agent 连接 OTLP 失败

```
rpc error: connection refused
```

| 检查 | 命令 |
|------|------|
| Collector 是否运行 | `docker compose ps otel-collector` |
| 端口是否监听 | `netstat -an | findstr 4317` |
|  endpoint 是否正确 | 应为 `localhost:4317`，非 `http://` 前缀（gRPC） |

### 13.2 VM 查不到 metric

| 可能原因 | 排查 |
|----------|------|
| Collector 导出失败 | `docker compose logs otel-collector` 搜 `error` |
| 查询名错误 | `curl .../api/v1/label/__name__/values` 列出所有 metric |
| Agent 未运行 | 确认 `go run` 进程存在 |
| label 过滤太严 | 先用 `{__name__=~"hardware_.*"}` |

### 13.3 SNMP 采集失败

| 可能原因 | 排查 |
|----------|------|
| snmpsim 未启动 | `docker compose ps snmpsim` |
| 端口错误 | 模拟器映射 `1161`，不是标准 `161` |
| community 不匹配 | 应为 `public` |
| Windows 防火墙 | 允许 UDP 1161 |

### 13.4 Grafana 无数据

| 可能原因 | 排查 |
|----------|------|
| 数据源 URL 错误 | 必须是 `http://victoria-metrics:8428` |
| 时间范围 | 右上角选 **Last 15 minutes** |
| provisioning 失败 | `docker compose logs grafana` |

### 13.5 停止与清理

```powershell
cd c:\work\observa-forge\deploy\phase0

# 停止容器（保留数据卷）
docker compose down

# 停止并删除卷（清空所有实验数据）
docker compose down -v
```

| 命令 | 含义 |
|------|------|
| `down` | 停止并删除容器、网络 |
| `down -v` | 额外删除 named volumes（VM/Grafana/PG 数据清空） |
| `down --rmi local` | 删除本地构建的镜像 |

---

## 14. Phase 0 完成标准与下一步

### 14.1 完成标准

你应能口头回答:

1. **OTLP 是什么？** Agent 与 Collector 之间的标准传输协议。  
2. **为何用 batch processor？** 减少 remote_write 请求，提升吞吐。  
3. **VM 与 Prometheus 查询接口兼容吗？** 兼容 PromQL 和 `/api/v1/query`。  
4. **metric 名与 labels 如何对应 DCOS MonitorResult？** identify→名，part/device→labels，perf→value。  
5. **数据流经过哪些组件？** Agent → Collector → VM → Grafana。

### 14.2 建议提交到 Git 的内容

```
deploy/phase0/          # 已提供
cmd/agent/              # Step 7 实现
internal/collector/
internal/config/
go.mod / go.sum
docs/Phase0-Getting-Started.md
```

### 14.3 下一步 Phase 1 预览

| 任务 | 说明 |
|------|------|
| Kafka Bridge | Java Agent `monitor-data` → OTLP |
| XML → YAML | 迁移 `monitor-model` 监测定义 |
| Alertmanager | 加告警规则与路由 |
| Loki | 接入 Syslog/Trap 日志 |

---

## 附录 A — 端口速查

| 端口 | 服务 | 协议 |
|------|------|------|
| 3000 | Grafana | HTTP |
| 4317 | OTel OTLP | gRPC |
| 4318 | OTel OTLP | HTTP |
| 5432 | PostgreSQL | TCP |
| 8428 | VictoriaMetrics | HTTP |
| 8888 | OTel self-metrics | HTTP |
| 1161 | snmpsim | UDP |
| 13133 | OTel health | HTTP |

## 附录 B — 参考链接

- [OpenTelemetry Collector 配置](https://opentelemetry.io/docs/collector/configuration/)
- [VictoriaMetrics Quick Start](https://docs.victoriametrics.com/quick-start/)
- [PromQL 基础](https://prometheus.io/docs/prometheus/latest/querying/basics/)
- [Grafana Provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/)
- [gosnmp 文档](https://github.com/gosnmp/gosnmp)
- [ObservaForge 架构设计](./ObservaForge-Architecture.md)
