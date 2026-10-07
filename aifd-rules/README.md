# aifd-rules

## 定位

`aifd-rules` 保存使用 aiFirstDev 开发项目时面向 AI 的规则、技能、模板和评审检查清单。

## 负责

- 描述依赖方向和职责边界的 agent 规则。
- 面向 feature 开发的技能和模板。
- 面向框架与业务项目变更的评审检查清单。
- 面向 AI agent 的项目级和框架级 skill map。
- 面向最终交付的提交描述、验证说明和经验沉淀格式规则。
- 面向 Wails、React、Ant Design 桌面应用的开发期 AI 工具链规则。

## 不负责

- Runtime 行为、Go 包、生成代码或服务入口。
- 业务 feature 真相、proto schema、配置表或持久化模型。
- 属于 `aifd-tools` 的工具执行逻辑。

## 业务项目如何使用

业务项目可以复制、扩展或引用这些规则，让 AI agent 在实现 feature 时遵守相同的模块
边界和评审习惯。

使用 skill 时，先读取 `skills/README.md` 选择入口，再按具体修改面读取
`skills/<name>/SKILL.md`。

桌面 UI 开发时，`desktop-app-development` 负责应用边界，`ant-design-ui-development`
负责 Ant Design MCP、CLI、LLMs.txt 和 X Skills 的开发期使用规则。

产生文件修改并需要给提交描述时，最终交付中给出 `提交描述`。提交标题第一个
方括号写主模块标签，例如 `[aifd-rules]`。需要详细正文格式时使用 `svn-commit-description`。

## 更新边界

规则可以独立于 Go module 演进。规则变更应澄清流程或边界，不能暗中改变运行期行为。
