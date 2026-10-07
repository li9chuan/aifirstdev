---
name: service-entrypoint-development
description: 开发或调整服务入口、CLI 入口、启动链、配置加载、信号处理、adapter 注册和进程级 wiring 时使用。适用于 aifd-tools、示例 cmd、proxy/runtime 服务壳或业务服务入口，不用于 feature 内部业务逻辑。
---

# 服务入口开发

这个 skill 处理进程级启动链，不处理 feature 内部业务规则。

## 工作流

1. 先判断入口类型：CLI、service、gateway、worker、example。
2. 画出启动链：参数解析、配置加载、依赖构造、adapter 注册、服务启动、信号退出。
3. 把业务规则留在 feature 或 runtime module；入口只做 wiring。
4. 启动失败要有明确错误上下文，退出路径要释放连接、sink、goroutine 和文件句柄。
5. 测试优先覆盖参数解析、依赖缺失、配置错误和优雅退出。

## 边界

- CLI 命令可以调用 `aifd-tools` 中的工作流入口，但不隐藏业务 migration。
- 服务入口可以组合 `rpc`、`rdb`、`proxy`、`observe`、`runtime`，但不 import 这些模块的 internal 包。
- 示例入口按示例目标展示接线：`minigame` 覆盖完整游戏后端框架链路，`desktool` 聚焦桌面工具入口；示例不承诺生产部署治理能力。
- 配置加载和热更策略应交给 `aifd-config` 或业务配置模块，不在入口层散落解析规则。

## Split Service 模板

- `cmd/minigame` 或 `cmd/allinone` 可以作为 all-in-one 开发入口，允许组装 proxy、game host、world host、local RPC、
  memory rdb、memory config 和 memory observe。
- `cmd/proxy` 只组 WebSocket proxy、LoginHandler、下行订阅和外部 RPC bus；不创建 game/world 业务服务。
- `cmd/proxy` 可以显式组装 proxy-broker embedded NATS server，并让自身 RPC bus 使用
  in-process NATS connection；broker 生命周期关闭顺序应排在 proxy 和 rpc-bus 之后。
- `cmd/game` 只组 root runtime、root Store、root module、业务 game route 和外部 RPC bus；不监听客户端连接。
- `cmd/world` 只组 service lifecycle、service module、恢复/启动顺序和外部 RPC bus；不创建 proxy/game。
- `cmd/game`、`cmd/world` 和其他业务服只作为 NATS client 连接 broker 集群，不隐式启动
  embedded broker。
- split-mode 入口默认使用 NATS RPC bus；没有 NATS 的测试必须 `t.Skip`，不要伪装成本地回环。
- 每个入口都应显式创建自己的 DB、observe sink 和 config 视图；`DefaultDB` 只能作为兼容路径，不作为新入口主路径。

## Config / Observe 模板

- feature 自己定义配置读取 helper，例如 `CounterLimit()`，入口只传入已解释后的 config 视图。
- feature 自己注册 validator，并把配置语义留在业务项目；入口不解析具体玩法字段。
- feature 自己定义指标名和维度，例如 `counter.add`、`result=limit`；observe sink 只作为出口注入。
- 内存 config/observe 可用于示例和测试，真实 Luban、Redis、Prometheus、日志 sink 后续由 adapter 替换。

## 验证

- Go module 内入口变更优先跑受影响包测试或 `go test ./...`。
- 文档或规则入口变更只需结构和内容检查。
- 无法启动真实外部依赖时，用 local/memory adapter 验证 wiring。

## 相关文档

- [skills 索引](../README.md)
- [工具入口](../../../aifd-tools/README.md)
- [Minigame 接线示例](../../../aifd-examples/minigame/README.md)
