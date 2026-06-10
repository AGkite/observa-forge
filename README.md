# ObservaForge

ObservaForge 是一个基于 Go 语言的 AIOps 原生可观测性平台，对齐 OpenTelemetry / Prometheus / Grafana 生态。

## 文档

- [架构设计](docs/ObservaForge-Architecture.md)
- [Phase 0 实战指南](docs/Phase0-Getting-Started.md) — 本地环境搭建与第一个 Go 采集器

## 快速开始（Phase 0）

```powershell
# 1. 启动 VM + Grafana + OTel + PostgreSQL + snmpsim
cd deploy/phase0
docker compose up -d

# 2. 回到项目根目录，运行采集 Agent
cd ../..
go mod tidy
go run ./cmd/agent
```

- Grafana: http://localhost:3000 （`admin` / `observaforge`）
- VictoriaMetrics: http://localhost:8428

## 仓库

GitHub: [`observa-forge`](https://github.com/observa-forge/observa-forge)

## 核心组件

| 组件 | 说明 |
|------|------|
| `observa-agent` | 边缘采集 Agent |
| `observa-control` | 控制平面（调度、模型、告警） |
| `observa-aiops` | AIOps 引擎（聚合、收敛、根因分析） |
