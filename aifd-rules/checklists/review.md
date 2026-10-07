# 评审检查清单

由[项目与 Feature 代码评审 skill](../skills/runtime-feature-review/SKILL.md)按本次审查范围选用；
职责依据见[模块边界](../../docs/module-boundary.md)，编码细则见[项目级编码约束](../skills/project-coding-constraints/SKILL.md)。
局部变更只检查相关项；发布、原生桌面等检查在涉及对应交付时使用。

- 变更是否保持模块依赖方向？
- 职责目录是否仍由框架根 Go module 统一发版；业务项目是否通过根 Go module 锁定整套框架版本；独立消费验证是否关闭 workspace 和本地替换？
- 基础设施包是否避免 import runtime 包？
- 新增小工具是否先尝试归属现有模块，而不是进入 `share` / `common` / `utils`？
- 如果考虑未来 `aifd-core`，是否满足无领域语义、无运行时依赖、至少两个框架模块稳定复用、有明确窄包名？
- `aifd-account`、`aifd-auth`、`aifd-proxy` 的边界是否保持：账号中心实现、signed login ticket、连接与登录接入不互相绑死？
- observe 变更是否保持热路径写入异步或本地缓冲？
- proxy 变更是否保持认证 session 路由与 owner 状态分离？
- robot 变更是否把业务 manager 和具体行为 action 留在框架外？
- schema 变更是否避免 import 生成的 pb 包或具体业务 proto 文件？
- config 变更是否避免 import 生成的 cfgtable 包或具体 Luban 表定义？
- 生成文件是否与手写 feature 代码分离？
- 存储恢复和 save/delete 路径是否用 memory adapter 做过测试？
- config reload 是否在发布部分状态前失败并保留旧 bundle？
- lifecycle 代码是否拒绝在恢复完成前写入状态？
- Git 跟踪内容是否排除了构建产物、临时安装器、凭据和运行日志，同时保留示例配置及可重建的源码绑定？
- 是否在不携带本地缓存和旧产物的源码副本中完成构建与验证，并分别记录通过、失败、跳过和未验证项？
- README、模块清单和 Agent 规则是否与当前实现职责一致，且没有混入业务项目专属执行流程？
- 规则是否有明确归属，并可从首页、模块 README 或相关 skill 找到；相关依据、模板和清单是否使用有效相对链接，避免重复维护同一规则？
- 发布内容是否排除了外部试验项目的专名、内部路径、业务参数与评估记录；清理记录本身是否留在发布范围之外？
- 开源首页是否给出可执行的快速开始、真实模块地址和许可证；独立消费项目是否能在没有本地 workspace/replace 的情况下获取发布依赖？
- Wails 的 CLI、Go module、前端 runtime 和 lock 是否一致，绑定是否重新生成；原生绑定失败是否明确报错而非切换到模拟后端？

## 相关文档

- [规则与资料入口](../README.md)
