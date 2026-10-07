---
name: robot-module-development
description: 开发 aifd-robot 或业务 robot module 时使用。适用于 AgentModule、消息路由、callback cache、behavior action、scenario、run group、transport adapter、report 和 observe 统计。
---

# Robot 模块开发

这个 skill 处理测试机器人框架和业务 robot 扩展，不处理服务端 NPC 或玩法规则。

## 职责边界

- `aifd-robot` 拥有 agent、run group、transport、message route、callback、behavior、report。
- 业务项目在 `features/<name>/robot` 或 `tests/robot` 中实现具体 manager、action 和 scenario。
- 框架不内置 inventory、arena、guild 等业务 action 名称。
- 框架不内置 scene、entity、move、AOI、player、world 等游戏动作或领域名。
- 真实 proxy 连接是 adapter；单元测试优先使用 local transport。

## AgentModule 工作流

1. 在 `OnAttach` 注册消息 handler 和必要 callback。
2. 在 `OnLogin` 只做登录后初始化请求。
3. 在 `OnMessage` 处理兜底消息，不吞掉未知错误。
4. 在 `Tick` 做低频推进，避免阻塞和无限发送。
5. 在 `Actions` 暴露 behavior action factory，action 名称由业务项目注册。

## Scenario 与统计

- Scenario 描述人数、账号范围、tick 间隔、行为树、持续时间和目标 adapter。
- RunGroup 负责创建 agent、启动、关闭和汇总 report。
- callback 必须有超时清理，避免压测长跑后内存增长。
- Report 至少覆盖 login、send、receive、error、latency 和业务 custom counter。
- observe sink 用于压测指标，不作为业务结算真相。
- 业务 action 名称由业务项目注册；AIFD-robot 只保证编排、transport、callback、behavior、report 和统计输出。

## 验证

- 用 local transport 测试 agent 创建、登录、发送、接收和关闭。
- 测 callback 命中、超时清理和未知消息。
- 测 behavior sequence/selector/action 和业务 action 注册。
- 测 report 与 observe memory sink 记录。
