---
name: feature-development
description: 开发业务项目 feature 时使用。适用于判断 feature 归 root、service 还是混合拥有，组织 features 目录，定义跨 feature 契约，接入 runtime、schema、config、rpc、rdb 和 robot 测试。
---

# Feature 开发

这个 skill 面向使用 aiFirstDev 的业务项目，不用于修改框架基础设施本身。

## 工作流

1. 先判断 feature 归 root、service，还是二者混合拥有。
2. 普通项目代码放在 `features/<name>`。
3. 共享内部状态前，先定义跨 feature 契约。
4. 当前主体根文档拥有的状态通过 root module 承载。
5. 全局、跨主体、排行或定时状态通过 service module 或插件 service module 承载。
6. 协议放业务项目 `proto/`；配置放业务项目配置源；生成物放 generated 目录。
7. 单元测试优先使用 memory/local adapter。

## 推荐目录

```text
features/<name>/
  feature.go
  root.go
  service.go
  rpc.go
  storage.go
  config.go
  events.go
  robot/
```

## 边界

- 业务 feature 可以依赖框架公开 API，不 import 框架 internal。
- 业务 feature 不修改基础设施模块来塞入自身规则。
- 若需要通用能力，先沉淀到对应框架模块的窄接口，再由业务项目接入。
- robot 测试代码属于业务项目 robot 包，不进入 `aifd-robot` 框架。
