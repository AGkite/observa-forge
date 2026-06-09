# ObservaForge Phase 0 实战指南（零基础版）

> **目标**：在本地用 Docker Compose 拉起 **VictoriaMetrics + Grafana + OTel Collector + PostgreSQL**，并用 **Go** 编写第一个采集器（`ping` + `snmp`），亲手跑通「采集 → OTLP → 存储 → 查询」全链路。  
> **预计耗时**：6～12 小时（含阅读、敲代码与实验；零基础建议分 2～3 天完成）  
> **前置文档**：[ObservaForge-Architecture.md](./ObservaForge-Architecture.md)（可先跳过，Phase 0 结束后再读）

---

## 目录

0. [写给零基础读者](#0-写给零基础读者)
1. [Phase 0 要达成什么](#1-phase-0-要达成什么)
2. [核心概念详解（必读）](#2-核心概念详解必读)
3. [环境准备](#3-环境准备)
4. [Step 1 — 启动基础设施](#4-step-1--启动基础设施)
5. [Step 2 — 理解 Docker 与端口](#5-step-2--理解-docker-与端口)
6. [Step 3 — OTel Collector 配置详解](#6-step-3--otel-collector-配置详解)
7. [Step 4 — VictoriaMetrics 与 PromQL](#7-step-4--victoriametrics-与-promql)
8. [Step 5 — Grafana 可视化](#8-step-5--grafana-可视化)
9. [Step 6 — PostgreSQL 元数据骨架](#9-step-6--postgresql-元数据骨架)
10. [Step 7 — 编写 observa-agent（Go 详解）](#10-step-7--编写-observa-agentgo-详解)
11. [Step 8 — 联调验证清单](#11-step-8--联调验证清单)
12. [Step 9 — PromQL 实验题](#12-step-9--promql-实验题)
13. [Step 10 — 故障排查](#13-step-10--故障排查)
14. [Phase 0 完成标准与下一步](#14-phase-0-完成标准与下一步)
15. [附录](#附录)

---

## 0. 写给零基础读者

### 0.1 这份文档适合谁？

| 背景 | 说明 |
|------|------|
| **完全新手** | 没写过 Go、没用过 Docker、没接触过监控 — 可以跟，但请按顺序读 **第 2 章概念** 和 **第 10 章 Go 语法**，不要跳步 |
| **会一点编程** | 有 Python/Java 经验即可；Go 语法会在 Step 7 逐行讲解 |
| **有运维经验** | SNMP、Prometheus 可能已熟悉；可快速浏览概念章，重点做 Step 7～8 |

### 0.2 你需要先知道的 5 个词

| 词 | 一句话解释 | 生活类比 |
|----|-----------|----------|
| **Metric（指标）** | 随时间变化的数字，如「延迟 2ms」「CPU 80%」 | 体温计读数 |
| **Agent（采集器）** | 定期去设备上「量一量」，把数字发出去 | 巡检员 |
| **Collector（收集器）** | 接收很多 Agent 的数据，清洗后转发到数据库 | 快递分拣中心 |
| **时序库** | 专门存「时间 + 数值」的数据库 | 带时间戳的 Excel 表 |
| **Dashboard（仪表盘）** | 把曲线画在网页上 | 股票 K 线图 |

### 0.3 学习路径建议

```
Day 1: 第 0～3 章（概念 + 装软件）→ Step 1 启动 Docker
Day 2: Step 2～6（理解配置，在 Grafana 里看到系统自带指标）
Day 3: Step 7（写 Go Agent）→ Step 8～9（联调 + PromQL 练习）
```

**遇到报错不要慌**：直接跳到 [Step 10 — 故障排查](#13-step-10--故障排查)，或对照 [附录 C — 术语表](#附录-c--术语表)。

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

**数据怎么走？（用大白话）**

1. 你的 **Go 程序**（Agent）每 30 秒去 ping 一下模拟设备、读一下 SNMP。
2. 读到的数字通过 **OTLP 协议** 发给 **OTel Collector**（一个 Docker 容器）。
3. Collector 把数据转成 Prometheus 格式，**推送** 到 **VictoriaMetrics**（时序库）。
4. 你在 **Grafana** 网页里写查询语句，看到延迟曲线。

**Phase 0 交付物**:

| 交付物 | 路径 |
|--------|------|
| Docker Compose 栈 | `deploy/phase0/docker-compose.yml` |
| OTel Collector 配置 | `deploy/phase0/otel-collector/config.yaml` |
| Grafana 数据源 | 自动 provisioning |
| PostgreSQL 设备表 | `deploy/phase0/postgres/init.sql` |
| Go 采集 Agent | `cmd/agent/`、`internal/collector/`、`internal/config/` |

**与 DCOS 的对应关系**（帮助理解「我们在重现什么」）:

| DCOS 概念 | Phase 0 对应 |
|-----------|--------------|
| `MonitorTask` 轮询 | Go Agent 定时 `Collect()` |
| `MonitorResult` / `Meta` | OTel Metric + attributes |
| `DataParser` 写库 | OTel Collector → VM remote_write |
| OOBS Web 看曲线 | Grafana Explore |

---

## 2. 核心概念详解（必读）

本章会稍长，但 **后面所有 Step 都建立在这些问题上**。建议边读边在纸上画数据流。

### 2.1 什么是「可观测性」？

**可观测性（Observability）** = 只看系统外部输出（指标、日志、链路），就能推断内部发生了什么。

运维/ SRE 日常排障顺序通常是：

```
Metrics 发现异常 → Logs 看细节 → Traces 定位慢路径
   （数字异常）      （报错文本）      （请求路径）
```

### 2.2 三类信号

| 信号 | 回答的问题 | 例子 | Phase 0 |
|------|-----------|------|---------|
| **Metrics（指标）** | 数值随时间怎么变？ | CPU 80%、Ping 2ms | ✅ 主线 |
| **Logs（日志）** | 某时刻发生了什么事件？ | `ERROR connection refused` | ❌ Phase 1 |
| **Traces（链路）** | 一次请求经过了哪些服务？ | API → DB → Cache | ❌ Phase 1 |

Phase 0 **只做 Metrics**：最简单、也最适合入门。

### 2.3 指标长什么样？

在 Prometheus / VictoriaMetrics 里，一条时间序列 = **指标名 + 标签 + 数值**：

```
hardware_ping_rtt_seconds{device_id="lab-snmpsim-01", site="lab"} 0.002
│                         │                                      │
│                         └── labels（标签/维度，用来区分不同设备）  └── 当前值
└── metric name（指标名）
```

**类比 Excel**：

| Excel | 监控世界 |
|-------|----------|
| 列名「延迟(ms)」 | metric name `hardware_ping_rtt_seconds` |
| 行标签「设备 A / 机房 lab」 | labels `device_id`, `site` |
| 单元格里的数字 | value `0.002` |
| 每一行还有「时间戳」 | 时序库自动记录 |

**为何需要 labels？** 同一指标名可以有多条序列：设备 A 的延迟、设备 B 的延迟，靠 `device_id` 区分。

### 2.4 OpenTelemetry（OTel）是什么？

**问题**：Java 用一种格式上报、Go 用另一种、Python 又一种 — 后端很难统一处理。

**OpenTelemetry** 是 CNCF 下的 **可观测性标准**，类似「各国插座统一成 USB-C」：

| 概念 | 说明 | 类比 |
|------|------|------|
| **SDK** | 嵌在 Agent/应用里，负责「产生」telemetry | 工厂里的传感器 |
| **OTLP** | OpenTelemetry Protocol，传输数据的「快递单格式」 | 标准快递箱 |
| **Collector** | 接收 → 处理 → 导出 | 快递分拣中心 |
| **Semantic Conventions** | 统一命名，如 `device.id` | 商品条码规范 |

**Phase 0 用到的端口**:

| 端口 | 协议 | 用途 |
|------|------|------|
| `4317` | gRPC | Agent 默认把指标推到这里 |
| `4318` | HTTP | OTLP 的 HTTP 备选 |
| `8888` | HTTP | Collector 自身的运行指标 |

**gRPC 和 HTTP 的区别（入门够用）**：

- **HTTP**：浏览器访问网页用的协议，文本/JSON 为主。
- **gRPC**：Google 出的高性能 RPC，二进制、适合服务间大量传数据。OTel 默认用 gRPC 传指标。

Agent 连 Collector 时写 `localhost:4317`，**不要**加 `http://` 前缀（那是 HTTP 的写法）。

### 2.5 Prometheus 与 VictoriaMetrics

**Prometheus** 经典模型是 **Pull（拉）**：Prometheus Server 每隔一段时间去目标机器的 `/metrics` 网页抓数据。

**VictoriaMetrics（VM）** 兼容 PromQL 查询语法，但写入更适合 **Push（推）** — Agent/Collector 主动 POST 过来。压缩好、吞吐高，ObservaForge 选它做主时序库。

| 操作 | URL |
|------|-----|
| 写入（remote_write） | `POST /api/v1/write` |
| 即时查询 | `GET /api/v1/query?query=...` |
| 范围查询（画曲线） | `GET /api/v1/query_range?query=...&start=...&end=...&step=15s` |
| 列出已有指标名 | `GET /api/v1/label/__name__/values` |

**PromQL** = Prometheus Query Language，用来问时序库「给我某指标过去 5 分钟的平均值」等。Step 4 和 Step 9 会练。

### 2.6 Docker 与 Docker Compose（入门）

**Docker** 把应用和它依赖的环境打包成 **镜像（Image）**，运行起来叫 **容器（Container）**。

**Docker Compose** 用一个 YAML 文件描述「要起哪些容器、端口、卷」，一条命令全部启动。

| 概念 | 说明 |
|------|------|
| **镜像 Image** | 只读模板，如 `grafana/grafana:11.3.0` |
| **容器 Container** | 镜像的运行实例，有独立进程和网络 |
| **端口映射** | `8428:8428` = 宿主机 8428 → 容器内 8428 |
| **Volume（卷）** | 持久化磁盘，容器删了数据还在 |
| **网络** | 同一 compose 里的容器可通过 **服务名** 互访，如 `http://victoria-metrics:8428` |

**重要**：在 **宿主机**（你的 Windows）上访问用 `localhost:8428`；在 **Grafana 容器内** 访问 VM 要用 `victoria-metrics:8428`（服务名，不是 localhost）。

### 2.7 SNMP 入门（Phase 0 会用到）

**SNMP**（Simple Network Management Protocol）是网络设备上常用的「读指标」协议。

| 概念 | 说明 |
|------|------|
| **Agent** | 设备上响应 SNMP 请求的进程（不是我们的 Go Agent，名字撞了） |
| **OID** | 对象标识符，像树形地址，如 `1.3.6.1.2.1.1.3.0` = sysUpTime |
| **Community** | v2c 的「口令」，Phase 0 模拟器用 `public` |
| **Get** | 读一个或多个 OID 的当前值 |

Phase 0 没有真实交换机，用 **snmpsim** 容器模拟 `127.0.0.1:1161` 上的 SNMP Agent（端口 **1161** 而非标准 161，避免和系统冲突）。

### 2.8 Go 语言速览（Step 7 前必读）

若你已有 Java/Python 经验，对照下表即可；完全新手请配合 Step 7 逐行看。

| Go 语法 | 含义 | 示例 |
|---------|------|------|
| `package xxx` | 文件属于哪个包（类似 Java package） | `package main` |
| `import (...)` | 导入依赖 | `import "fmt"` |
| `func Name(...) 返回类型` | 函数 | `func Add(a, b int) int` |
| `type X struct { ... }` | 结构体，字段集合 | `type Config struct { Interval time.Duration }` |
| `:=` | 短变量声明，类型自动推断 | `x := 3` |
| `*` | 指针；`&x` 取地址 | 函数参数传大对象时常用指针 |
| `[]T` | 切片，动态数组 | `[]Target` |
| `if err != nil` | Go 错误处理惯例 | 几乎每步都检查 `err` |
| `go xxx()` | 启动 goroutine（轻量线程） | Phase 0 未用，后续会遇 |
| `defer` | 函数退出前执行 | 用于关闭连接、`Shutdown` |
| `context.Context` | 传递取消信号、超时 | `ctx, cancel := context.WithTimeout(...)` |

**Go Module（模块）**：Go 1.11+ 的依赖管理，根目录 `go.mod` 声明模块路径和依赖版本，类似 Node 的 `package.json`。

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

1. 安装 [Docker Desktop](https://www.docker.com/products/docker-desktop/)，安装时勾选 **Use WSL 2**。
2. 打开 Docker Desktop → **Settings → Resources → Memory**，设为 **≥ 4GB**。
3. 验证：

```powershell
docker version
docker compose version
```

| 命令 | 期望结果 | 若失败 |
|------|----------|--------|
| `docker version` | Client 和 Server 都有版本号 | 启动 Docker Desktop |
| `docker compose version` | ≥ 2.20 | 更新 Docker Desktop |

> **注意**：现代 Docker 用 `docker compose`（中间有空格），不是旧版 `docker-compose`（带连字符）。

#### Go 1.22+

1. 安装：https://go.dev/dl/
2. 验证：

```powershell
go version
# 期望: go version go1.22.x 或更高

go env GOPATH GO111MODULE
```

| 变量 | 说明 |
|------|------|
| `GOPATH` | Go 工作区，默认 `%USERPROFILE%\go` |
| `GO111MODULE` | 应为 `on`（模块模式） |

#### Git

```powershell
git clone https://github.com/<your-org>/observa-forge.git
cd observa-forge
```

若已有仓库，直接进入项目根目录即可。

#### 可选工具

```powershell
winget install PostgreSQL.psql     # psql 客户端（也可只用 docker exec）
```

### 3.3 克隆后目录结构

```
observa-forge/
├── deploy/phase0/          ← Phase 0 基础设施（已提供）
├── docs/
│   ├── ObservaForge-Architecture.md
│   └── Phase0-Getting-Started.md   ← 本文档
├── cmd/agent/              ← Step 7 编写/运行
├── internal/
│   ├── collector/          ← 采集逻辑
│   └── config/             ← 配置
├── go.mod / go.sum         ← Go 依赖
└── README.md
```

---

## 4. Step 1 — 启动基础设施

### 4.1 进入部署目录

```powershell
cd c:\work\observa-forge\deploy\phase0
```

路径请按你本机实际位置修改。

### 4.2 拉取镜像并启动

**首次建议前台启动**（能看到实时日志，Ctrl+C 可停止）：

```powershell
docker compose up
```

熟悉后可用后台模式：

```powershell
docker compose up -d
```

| 参数 | 含义 |
|------|------|
| `-d` / `--detach` | 后台运行 |
| `--pull always` | 每次拉最新镜像 |
| `up` | 创建网络、卷、容器并启动 |

第一次会下载几个镜像（几百 MB 到 1GB+），视网络情况而定，请耐心等待。

### 4.3 检查容器状态

```powershell
docker compose ps
```

期望 **STATE 均为 `running`**：

| 容器名 | 端口 | 说明 |
|--------|------|------|
| `of-vm` | `8428` | VictoriaMetrics 时序库 |
| `of-otel-collector` | `4317-4318`, `8888` | OTel Collector |
| `of-grafana` | `3000` | Grafana 网页 |
| `of-postgres` | `5432` | PostgreSQL |
| `of-snmpsim` | `1161/udp` | SNMP 模拟器 |

查看某个服务日志：

```powershell
docker compose logs otel-collector --tail 50 -f
```

| 参数 | 含义 |
|------|------|
| `--tail 50` | 只看最后 50 行 |
| `-f` | 持续跟踪（Ctrl+C 退出） |

### 4.4 健康检查

```powershell
# VictoriaMetrics
curl http://localhost:8428/health
# 期望: OK

# OTel Collector 自身指标（能访问说明 Collector 在跑）
curl http://localhost:8888/metrics

# Grafana
curl http://localhost:3000/api/health
# 期望: {"database":"ok",...}
```

PowerShell 若 `curl` 行为异常（Windows 上可能是 Invoke-WebRequest 别名），可用：

```powershell
Invoke-WebRequest -Uri http://localhost:8428/health -UseBasicParsing
```

> **说明**：Collector 配置了 `13133` 健康检查端口，但 Phase 0 的 compose **未映射到宿主机**。在主机上请用 `8888/metrics` 或 `docker compose logs` 判断 Collector 是否正常。

---

## 5. Step 2 — 理解 Docker 与端口

配置文件：`deploy/phase0/docker-compose.yml`

### 5.1 整体结构（YAML 入门）

```yaml
name: observa-forge-phase0    # 项目名，影响默认网络名

services:                      # 下面每个 key 是一个服务
  victoria-metrics:            # 服务名（容器间 DNS 用这个名字）
    image: victoriametrics/... # 用哪个镜像
    ports:
      - "8428:8428"            # 宿主机:容器
    volumes:                   # 挂载文件或持久化卷
      - vm-data:/victoria-metrics-data

volumes:                       # 声明具名卷
  vm-data:
```

### 5.2 VictoriaMetrics

```yaml
command:
  - "-storageDataPath=/victoria-metrics-data"
  - "-retentionPeriod=7d"
  - "-httpListenAddr=:8428"
  - "-promscrape.config=/etc/prometheus/prometheus.yml"
```

| 参数 | 含义 |
|------|------|
| `-storageDataPath` | 数据存哪（Docker volume 持久化） |
| `-retentionPeriod=7d` | 只保留 7 天（实验够用） |
| `-httpListenAddr=:8428` | 监听 8428（查询 + 写入 + `/metrics`） |
| `-promscrape.config` | 内置抓取配置，Phase 0 用于 VM 自身和 OTel 指标 |

### 5.3 OTel Collector

```yaml
image: otel/opentelemetry-collector-contrib:0.109.0
command: ["--config=/etc/otelcol/config.yaml"]
```

| 项 | 说明 |
|----|------|
| `contrib` | **扩展版**镜像，含 `prometheusremotewrite`；核心版没有 |
| `--config` | 配置文件路径（启动必填） |

### 5.4 Grafana

```yaml
environment:
  GF_SECURITY_ADMIN_USER: admin
  GF_SECURITY_ADMIN_PASSWORD: observaforge
```

浏览器打开：**http://localhost:3000** → 用户名 `admin`，密码 `observaforge`

### 5.5 PostgreSQL

```yaml
environment:
  POSTGRES_USER: observa
  POSTGRES_PASSWORD: observaforge
  POSTGRES_DB: observa_forge
```

`init.sql` 在 **首次** 创建数据卷时自动执行，插入样例设备。

### 5.6 snmpsim（SNMP 模拟器）

模拟 **`127.0.0.1:1161`** 上的 SNMP。Go Agent 在 **宿主机** 运行，所以 Endpoint 写 `127.0.0.1:1161`（compose 已把 UDP 1161 映射到主机）。

测试 SNMP（可选，需网络工具容器）：

```powershell
docker run --rm -it --network observa-forge-phase0_default nicolaka/netshoot `
  snmpwalk -v2c -c public host.docker.internal:1161 1.3.6.1.2.1.1
```

| 参数 | 含义 |
|------|------|
| `-v2c` | SNMP 版本 2c |
| `-c public` | Community 口令 |
| `1.3.6.1.2.1.1` | system 组 OID（含 sysDescr、sysUpTime） |

---

## 6. Step 3 — OTel Collector 配置详解

配置文件：`deploy/phase0/otel-collector/config.yaml`

### 6.1 四段式结构

Collector 配置固定分成四块 + service：

```yaml
receivers:    # 数据从哪来
processors:   # 中间怎么处理
exporters:    # 数据往哪去
extensions:   # 健康检查等插件
service:      # 把上面拼成 pipeline
```

**Pipeline** = 有向流水线：

```
otlp → memory_limiter → batch → attributes → prometheusremotewrite → VictoriaMetrics
                                            → debug（打印到日志，方便调试）
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
| `0.0.0.0` | 监听所有网卡（容器内必须这样，否则外部连不进来） |
| 宿主机 Agent | 连接 `localhost:4317` |

### 6.3 processors

#### batch — 批量发送

```yaml
batch:
  timeout: 5s
  send_batch_size: 512
```

| 字段 | 含义 |
|------|------|
| `timeout` | 最多等 5 秒就发一批（即使没满） |
| `send_batch_size` | 单批最多 512 个点 |

**为何需要**：每条 metric 单独 POST 会让 VM 压力巨大；批量可显著提吞吐。

#### memory_limiter — 防止 OOM

超过内存上限会 **反压（backpressure）**，暂时拒绝新数据，避免 Collector 被系统 Kill。

#### attributes — 打标签

```yaml
attributes:
  actions:
    - key: observaforge.phase
      value: phase0
      action: insert
```

给所有经过的 metric 插入 label `observaforge_phase=phase0`（导出到 Prometheus 时 `.` 变 `_`），便于在 Grafana 里过滤实验数据。

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
| `victoria-metrics` | **Docker 服务名**，不是 localhost |
| `insecure: true` | Phase 0 无 HTTPS，跳过 TLS 校验 |

Collector 会把 OTLP 格式 **自动转换** 为 Prometheus remote write（Protobuf + Snappy）。

### 6.5 验证 Collector 收到数据

启动 Agent 后：

```powershell
docker compose logs otel-collector -f
```

应看到 `Metrics` / `debug` 相关输出。

查 Collector 自身指标：

```powershell
curl http://localhost:8888/metrics | Select-String "otelcol_receiver"
```

| 指标 | 含义 |
|------|------|
| `otelcol_receiver_accepted_metric_points` | 成功接收的点数 |
| `otelcol_exporter_sent_metric_points` | 成功导出点数 |
| `otelcol_exporter_send_failed_metric_points` | 导出失败（应为 0） |

---

## 7. Step 4 — VictoriaMetrics 与 PromQL

### 7.1 查询 API

**即时查询** — 当前这一刻的值：

```powershell
curl --globoff "http://localhost:8428/api/v1/query?query=up"
```

**范围查询** — 过去一段时间，用于画曲线：

```powershell
$end = [int][double]::Parse((Get-Date -UFormat %s))
$start = $end - 3600
curl --globoff "http://localhost:8428/api/v1/query_range?query=up&start=$start&end=$end&step=15"
```

| API | 用途 |
|-----|------|
| `/api/v1/query` | 一个时间点 |
| `/api/v1/query_range` | 时间范围 + 步长 `step` |

PowerShell 里 URL 含 `{}` 时加 `--globoff`，避免花括号被 shell 误解析。

### 7.2 查看已有指标

Agent 跑起来之后：

```powershell
curl "http://localhost:8428/api/v1/label/__name__/values"
curl --globoff "http://localhost:8428/api/v1/series?match[]={observaforge_phase=\"phase0\"}"
```

### 7.3 PromQL 入门

| 表达式 | 类型 | 含义 |
|--------|------|------|
| `hardware_ping_success` | 即时向量 | 所有设备的 ping 成功指标 |
| `hardware_ping_success{device_id="lab-snmpsim-01"}` | 带过滤 | 只查一台设备 |
| `hardware_ping_rtt_seconds[5m]` | 范围向量 | 过去 5 分钟每个采样点 |
| `avg_over_time(hardware_ping_rtt_seconds[5m])` | 函数 | 5 分钟内 RTT 平均值 |
| `rate(counter[1m])` | 函数 | Counter 每秒增速（用于「累计计数」类指标） |

Phase 0 Agent 上报后常见的 metric 名（OTel 的 `.` 会变成 `_`，单位可能加后缀）：

| 查询名 | 含义 |
|--------|------|
| `hardware_ping_success` | Ping 是否成功（1/0） |
| `hardware_ping_rtt_seconds` | 往返延迟（秒） |
| `hardware_snmp_success` | SNMP 是否成功 |
| `hardware_snmp_sysuptime_seconds` | 设备运行时间 |

---

## 8. Step 5 — Grafana 可视化

### 8.1 登录与数据源

1. 打开 http://localhost:3000
2. `admin` / `observaforge`
3. **Connections → Data sources → VictoriaMetrics** 应已自动配置

数据源 URL 为 `http://victoria-metrics:8428`（**容器内**网络）。若改成 `localhost`，Grafana 会连到自己容器内部，必然失败。

### 8.2 Explore 快速查指标

1. 左侧 **Explore**（compass 图标）
2. 数据源选 **VictoriaMetrics**
3. 输入 PromQL：`hardware_ping_rtt_seconds`
4. **Run query**

右上角时间范围选 **Last 15 minutes**，并确认 Agent 已在运行。

### 8.3 创建 Dashboard 面板

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

**Panel 4 — Collector 吞吐**:

```promql
rate(otelcol_receiver_accepted_metric_points[1m])
```

| 选项 | 建议 |
|------|------|
| Min interval | `15s` |
| Legend | `{{device_id}} - {{monitor_identify}}` |
| Unit（RTT） | seconds (s) |

---

## 9. Step 6 — PostgreSQL 元数据骨架

Phase 0 只验证 **元数据表存在**；Phase 2 的 Control Plane 会读写此库。Agent 暂时 **不连数据库**，但 `device_id` 应与表中 `device_code` 一致，便于以后关联。

### 9.1 连接

```powershell
docker exec -it of-postgres psql -U observa -d observa_forge
```

### 9.2 验证

```sql
SELECT * FROM devices;
-- 期望: lab-snmpsim-01 / SNMP Simulator
```

| 列 | 含义 |
|----|------|
| `device_code` | 设备唯一编码（= Agent 的 device_id） |
| `node_class` | 设备类型 |
| `endpoint` | IP:Port |
| `site` | 机房/站点 |

退出：`\q`

---

## 10. Step 7 — 编写 observa-agent（Go 详解）

本章是 Phase 0 **核心**：从零理解并实现采集器。若仓库已有代码，建议 **先删或改名** `cmd/agent` 和 `internal/`，再跟着敲一遍。

### 10.1 程序要做什么？（伪代码）

```
每隔 30 秒:
  对每个目标设备:
    1. TCP 连接 host:port，测是否通、耗时多少 → 记为 ping 指标
    2. SNMP Get sysDescr、sysUpTime → 记为 snmp 指标
    3. 通过 OTLP 发给 Collector
```

### 10.2 初始化 Go 模块

在项目 **根目录**：

```powershell
cd c:\work\observa-forge

go mod init github.com/observa-forge/observa-forge
```

`go mod init` 后面的路径是 **模块名**，import 自己代码时要用它，例如：

```go
import "github.com/observa-forge/observa-forge/internal/config"
```

### 10.3 安装依赖

```powershell
go get go.opentelemetry.io/otel@v1.32.0
go get go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@v1.32.0
go get go.opentelemetry.io/otel/sdk/metric@v1.32.0
go get github.com/gosnmp/gosnmp@v1.38.0
```

| 依赖 | 用途 |
|------|------|
| `otel` | OpenTelemetry API（创建 Gauge、Record 值） |
| `otlpmetricgrpc` | 经 gRPC 把指标发到 Collector |
| `sdk/metric` | MeterProvider、PeriodicReader（定时导出） |
| `gosnmp` | SNMP 客户端 |

### 10.4 创建目录

```powershell
mkdir -Force cmd\agent, internal\collector, internal\config
```

Go 约定：

- `cmd/xxx/main.go` — 可执行程序入口
- `internal/yyy` — 仅本项目内 import 的包（外部项目不能引用）

### 10.5 配置文件 `internal/config/config.go`

**作用**：集中管理 OTLP 地址、采集间隔、目标设备列表；支持环境变量覆盖。

```go
package config

import (
	"os"
	"time"
)

// Target 表示一个被监测设备
type Target struct {
	DeviceID      string // 设备 ID，会写到 metric label
	Endpoint      string // host:port，如 127.0.0.1:1161
	SNMPCommunity string // SNMP v2c community
	Site          string // 机房/站点 label
}

// Config 是 Agent 全局配置
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
```

**语法说明**：

| 代码 | 解释 |
|------|------|
| `type Target struct { ... }` | 定义结构体；字段大写开头 = 包外可访问 |
| `[]Target` | Target 的切片（动态列表） |
| `30 * time.Second` | 常量乘法，得到 `time.Duration` |
| `envOr(...)` | 读环境变量，没有则用默认值 |
| `time.ParseDuration("30s")` | 解析 `30s`、`5m` 这类字符串 |

### 10.6 采集逻辑 `internal/collector/collector.go`

**作用**：真正去「探测设备」；与 OTel 无关，方便单元测试。

```go
package collector

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/gosnmp/gosnmp"
)

// PingResult 表示一次连通性探测结果
type PingResult struct {
	Success bool
	RTT     float64 // 秒
}

// TCPProbe 用 TCP 连接代替 ICMP Ping（Windows 上 Ping 常需管理员权限）
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
```

**为何用 TCP 而不是 ICMP Ping？**

- 标准 `ping` 用 ICMP，Windows 上常需管理员权限。
- snmpsim 在 **1161/UDP** 监听 SNMP，但 **TCP 连接同一端口** 也能判断「端口是否可达、网络是否通」，Phase 0 足够。

```go
type SNMPSystem struct {
	SysDescr  string
	SysUpTime float64 // 秒
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
		"1.3.6.1.2.1.1.1.0", // sysDescr — 系统描述字符串
		"1.3.6.1.2.1.1.3.0", // sysUpTime — 运行时间（百分之一秒）
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
```

**SNMP 细节**：

- `sysUpTime` 单位是 **centiseconds**（1/100 秒），所以要 `/ 100`。
- `defer params.Conn.Close()`：`defer` 保证函数返回前关闭连接，避免泄漏。
- `&gosnmp.GoSNMP{...}`：`&` 取结构体地址，传给需要指针的 API。

```go
func splitHostPort(hostPort string) (string, uint16) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort, 161 // 没写端口则用 SNMP 默认 161
	}
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return host, 161
	}
	return host, uint16(p)
}
```

`(string, uint16)` 是 **多返回值**，Go 常见写法。

### 10.7 主程序 `cmd/agent/main.go`（逐段讲解）

#### 10.7.1 import 与 main 入口

```go
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
```

- `package main` + `func main()` = 程序入口。
- `sdkmetric "..."`：**import 别名**，避免和 API 包 `metric` 重名。

#### 10.7.2 优雅退出（Signal）

```go
func main() {
	cfg := config.Default()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
```

| 代码 | 含义 |
|------|------|
| `context.Background()` | 根 context |
| `signal.NotifyContext` | 按 Ctrl+C 或收到 SIGTERM 时，`ctx` 会被 cancel |
| `defer cancel()` | main 结束时释放 signal 监听 |

后续 `for` 循环里监听 `<-ctx.Done()`，就能 **优雅停止** 而不是强杀。

#### 10.7.3 创建 MeterProvider（OTel 核心）

```go
	mp, err := newMeterProvider(ctx, cfg.OTLPEndpoint)
	if err != nil {
		log.Fatalf("meter provider: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mp.Shutdown(shutdownCtx)
	}()
```

**MeterProvider** = OTel 里「指标工厂 + 导出调度」的总管。`Shutdown` 会把缓冲区里还没发的 metric Flush 出去。

`newMeterProvider` 实现：

```go
func newMeterProvider(ctx context.Context, otlpEndpoint string) (*sdkmetric.MeterProvider, error) {
	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(otlpEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	// ...

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(15*time.Second),
		)),
	)
	otel.SetMeterProvider(mp)
	return mp, nil
}
```

| 组件 | 作用 |
|------|------|
| `otlpmetricgrpc.New` | 创建 gRPC 导出器，目标 `localhost:4317` |
| `WithInsecure()` | 不启用 TLS（Phase 0 本地实验） |
| `resource` | 给所有 metric 附加服务名 `observa-agent` 等 **资源属性** |
| `PeriodicReader` | 每 15 秒把内存中的指标批量 Export 一次 |
| `otel.SetMeterProvider` | 设为全局默认，后面 `Meter()` 都用它 |

#### 10.7.4 注册 Gauge 指标

```go
	meter := mp.Meter("observa-agent")

	pingSuccess, _ := meter.Float64Gauge("hardware.ping.success",
		metric.WithDescription("Ping/connect probe success (1=ok, 0=fail)"))
	pingRTT, _ := meter.Float64Gauge("hardware.ping.rtt",
		metric.WithDescription("Probe round-trip time"),
		metric.WithUnit("s"))
	// ... snmpSuccess, snmpUpTime 类似
```

**Gauge** = 可上可下的「瞬时值」（对比 **Counter** 只增不减）。

- 指标名用 `.` 分隔，是 OTel 惯例；到 Prometheus 会变成 `_`。
- `Record(ctx, value, attrs)` 在采集时调用（见下）。

#### 10.7.5 定时采集循环

```go
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
```

| 语法 | 说明 |
|------|------|
| `time.NewTicker(30s)` | 每 30s 往 `ticker.C` 发信号 |
| `select` | 多路复用，类似 switch，等 channel 就绪 |
| 先 `runCollect` 一次 | 启动后立即采第一轮，不用干等 30s |

#### 10.7.6 runCollect — 采集并 Record

```go
func runCollect(ctx context.Context, cfg config.Config, ...) {
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
		// SNMP 类似，monitor.identify 用 "SNMPSystem"
	}
}
```

**Attributes** = OTel 的 labels。导出后大致变为：

- `device.id` → `device_id`
- `device.site` → `device_site` 或 `site`（取决于 exporter 映射；Grafana 里以实际 label 名为准，可用 `/api/v1/series` 查看）

### 10.8 运行 Agent

```powershell
# 终端 1：确保 Docker 栈在跑
cd c:\work\observa-forge\deploy\phase0
docker compose up -d

# 终端 2：运行 Agent
cd c:\work\observa-forge
go run ./cmd/agent
```

期望输出（约每 30 秒一行）：

```
observa-agent started interval=30s otlp=localhost:4317
collect device=lab-snmpsim-01 ping_ok=true rtt=0.001 snmp_ok=true sysUpTime=12345.6
```

**环境变量（可选）**:

| 变量 | 默认 | 含义 |
|------|------|------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC 地址 |
| `OBSERVA_INTERVAL` | `30s` | 采集间隔 |
| `OBSERVA_SNMP_TARGET` | `127.0.0.1:1161` | SNMP 目标 |
| `OBSERVA_DEVICE_ID` | `lab-snmpsim-01` | device label |
| `OBSERVA_SITE` | `lab` | site label |
| `OBSERVA_SNMP_COMMUNITY` | `public` | SNMP community |

PowerShell 设置示例：

```powershell
$env:OBSERVA_INTERVAL = "15s"
go run ./cmd/agent
```

### 10.9 指标命名对照

| OTel Metric | 类型 | 主要 Labels | 含义 |
|-------------|------|-------------|------|
| `hardware.ping.success` | Gauge | `device.id`, `device.site` | 1=通，0=不通 |
| `hardware.ping.rtt` | Gauge | 同上 | 往返秒数 |
| `hardware.snmp.success` | Gauge | 同上 | SNMP 是否成功 |
| `hardware.snmp.sysuptime` | Gauge | 同上 | 运行时间（秒） |

导出到 VM 后常见名称：

```
hardware_ping_success
hardware_ping_rtt_seconds
hardware_snmp_sysuptime_seconds
```

### 10.10 常见问题（写 Go 时）

| 现象 | 原因 | 处理 |
|------|------|------|
| `cannot find module` | 没在项目根目录 / 没 `go mod init` | 回到根目录执行 `go mod tidy` |
| import 路径报错 | 模块名与 `go.mod` 不一致 | 检查 `module github.com/...` 与 import 前缀 |
| SNMP 一直 false | snmpsim 没起 / 端口错 | `docker compose ps`，确认 1161 |
| OTLP connection refused | Collector 没起 | `docker compose up -d` |

---

## 11. Step 8 — 联调验证清单

按顺序打勾：

### 11.1 基础设施

- [ ] `docker compose ps` 五个容器均为 `running`
- [ ] `curl http://localhost:8428/health` 返回 `OK`
- [ ] `curl http://localhost:8888/metrics` 有 OTel Collector 指标
- [ ] Grafana http://localhost:3000 可登录

### 11.2 SNMP 模拟器

- [ ] snmpsim 容器 running（可选：snmpwalk 见 Step 5.6）

### 11.3 Agent → Collector → VM

- [ ] `go run ./cmd/agent` 无报错
- [ ] Collector 日志有 metric 导出信息
- [ ] VM 能查到数据：

```powershell
curl --globoff "http://localhost:8428/api/v1/query?query=hardware_ping_success"
```

返回 JSON 里 `"value":[..., "1"]` 表示成功。

### 11.4 Grafana

- [ ] Explore 中 `hardware_ping_rtt_seconds` 有曲线
- [ ] Dashboard 至少 3 个 Panel 正常

### 11.5 PostgreSQL

- [ ] `SELECT * FROM devices` 有 `lab-snmpsim-01`

---

## 12. Step 9 — PromQL 实验题

在 Grafana Explore 里验证答案。

### 练习 1 — 过滤

```promql
hardware_ping_rtt_seconds{device_id="lab-snmpsim-01"}
```

若 `device_id` 不匹配，先运行：

```promql
{__name__=~"hardware_ping.*"}
```

查看实际 label 名。

### 练习 2 — 布尔判断

Ping 失败时值为 0：

```promql
hardware_ping_success == 0
```

### 练习 3 — 5 分钟平均 RTT

```promql
avg_over_time(hardware_ping_rtt_seconds[5m])
```

| 概念 | 含义 |
|------|------|
| `[5m]` | 范围向量，过去 5 分钟 |
| `avg_over_time` | 对范围内每个点求平均 |

### 练习 4 — Collector 接收速率

```promql
rate(otelcol_receiver_accepted_metric_points[1m])
```

`rate` 适用于 **Counter**（只增不减的计数器）。

### 练习 5 — 关联思考

若 Ping 失败 **且** SNMP 也失败，更可能是网络/设备问题；若 Ping 成功 SNMP 失败，可能是 community 或 SNMP 配置问题。Phase 3 AIOps 会用类似模式做关联规则。

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
| endpoint | 应为 `localhost:4317`，**无** `http://` |

### 13.2 VM 查不到 metric

| 可能原因 | 排查 |
|----------|------|
| Collector 导出失败 | `docker compose logs otel-collector` 搜 `error` |
| 指标名不对 | `curl http://localhost:8428/api/v1/label/__name__/values` |
| Agent 未运行 | 确认 `go run` 进程在 |
| label 过滤太严 | 先用 `{__name__=~"hardware_.*"}` |

### 13.3 SNMP 采集失败

| 可能原因 | 排查 |
|----------|------|
| snmpsim 未启动 | `docker compose ps snmpsim` |
| 端口错误 | 应用 **1161**，不是 161 |
| community 错误 | 应为 `public` |
| 防火墙 | 允许 UDP 1161 |

### 13.4 Grafana 无数据

| 可能原因 | 排查 |
|----------|------|
| 数据源 URL 错 | 容器内必须是 `http://victoria-metrics:8428` |
| 时间范围 | 选 **Last 15 minutes** |
| Agent/Collector 未跑 | 回到 11.3 |

### 13.5 停止与清理

```powershell
cd c:\work\observa-forge\deploy\phase0

docker compose down      # 停容器，保留数据卷
docker compose down -v   # 连卷一起删，清空实验数据
```

---

## 14. Phase 0 完成标准与下一步

### 14.1 完成标准（能口头回答）

1. **OTLP 是什么？** Agent 与 Collector 之间的标准传输协议（常见 gRPC 4317）。
2. **为何用 batch processor？** 合并多条 metric 再写入，减少请求、提高吞吐。
3. **VM 与 Prometheus 查询兼容吗？** 兼容 PromQL 和 `/api/v1/query`。
4. **metric 名与 labels 是什么？** 名=测什么；labels=哪台设备/哪个维度。
5. **数据流？** Agent → Collector → VictoriaMetrics → Grafana。

### 14.2 建议纳入 Git 的内容

```
deploy/phase0/
cmd/agent/
internal/collector/
internal/config/
go.mod / go.sum
docs/Phase0-Getting-Started.md
```

### 14.3 Phase 1 预览

| 任务 | 说明 |
|------|------|
| Kafka Bridge | Java Agent → OTLP |
| XML → YAML | 迁移监测定义 |
| Alertmanager | 告警规则 |
| Loki | Syslog/Trap 日志 |

---

## 附录

### 附录 A — 端口速查

| 端口 | 服务 | 协议 |
|------|------|------|
| 3000 | Grafana | HTTP |
| 4317 | OTel OTLP | gRPC |
| 4318 | OTel OTLP | HTTP |
| 5432 | PostgreSQL | TCP |
| 8428 | VictoriaMetrics | HTTP |
| 8888 | OTel self-metrics | HTTP |
| 1161 | snmpsim | UDP |

### 附录 B — 参考链接

- [OpenTelemetry Collector 配置](https://opentelemetry.io/docs/collector/configuration/)
- [VictoriaMetrics Quick Start](https://docs.victoriametrics.com/quick-start/)
- [PromQL 基础](https://prometheus.io/docs/prometheus/latest/querying/basics/)
- [Go 官方教程（Tour of Go）](https://go.dev/tour/)
- [gosnmp 文档](https://github.com/gosnmp/gosnmp)
- [ObservaForge 架构设计](./ObservaForge-Architecture.md)

### 附录 C — 术语表

| 术语 | 解释 |
|------|------|
| **Agent** | 本项目中指 Go 采集进程 observa-agent |
| **Attribute / Label** | 指标的维度标签 |
| **Collector** | OTel Collector，中转站 |
| **Gauge** | 可增可减的指标类型 |
| **OTLP** | OpenTelemetry 传输协议 |
| **OID** | SNMP 对象标识符 |
| **PromQL** | Prometheus 查询语言 |
| **remote_write** | Prometheus 兼容的推送写入 API |
| **Time series** | 一条 metric+labels 随时间变化的序列 |
| **Volume** | Docker 持久化存储 |

### 附录 D — Go 文件对照清单

完成 Step 7 后，你应有这些文件：

```
cmd/agent/main.go           # 入口：OTel 初始化 + 定时采集
internal/config/config.go   # 配置与环境变量
internal/collector/collector.go  # TCP 探测 + SNMP
go.mod                      # 模块与依赖
go.sum                      # 依赖校验和（go get 后自动生成）
```

验证编译：

```powershell
go build -o observa-agent.exe ./cmd/agent
./observa-agent.exe
```

---

*文档版本：Phase 0 零基础增强版 — 与仓库 `cmd/agent`、`internal/*` 源码对齐。*
