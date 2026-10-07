# aifd-plugins/mail

`aifd-plugins/mail` 是 AIFD 官方可选邮件插件。它通过 `runtime/service.Module`
和 `runtime/root.Module[T]` 显式组合进业务入口，不在 import 时自动注册 route、
启动 worker 或创建 Redis 连接。

## 负责

- 个人邮件、全局邮件和分组邮件的基础数据模型。
- 客户端邮箱 RPC：列表、正文详情、已读、领取、删除已读。
- 内部发信 RPC：`Mail.Add`、`Mail.AddBatch`。
- 模板邮件与纯文本自定义邮件的互斥内容模型及基础校验。
- Redis/Memory Store、近期 ttlcache、插件内 snowflake mailID。

## 不负责

- 业务 GM 参数解析、配置表模板解释、背包发奖规则、触发信号和业务 logstat。
- Redis client 创建、关闭、重连和部署编排。
- 把邮件状态塞进 `aifd-runtime` 核心。

## 持久化模型

生产真相源是 `store.RedisStore`。`ttlcache` 只缓存近期 mailbox、global/group mail，
淘汰不会落盘，也不能影响正确性。所有写入必须先成功写入 Store，再更新或失效缓存。

领取附件通过 `mail.Awarder` 注入，业务实现必须按 `rootID + mailID` 保证幂等。

## 邮件内容

`MailBase` 支持两种互斥的内容来源：

- 模板邮件：`template_id > 0`，`custom_content` 必须为空，`content_list` 作为模板参数。
- 自定义邮件：`template_id = 0`，设置 `custom_content.title/body`，`content_list` 必须为空。

自定义邮件标题和正文不能为空，标题最多 100 个 Unicode 字符，正文默认最多 5000 个
Unicode 字符，业务可通过 `service.Options.MaxBodyRunes` 显式配置。正文按纯文本处理，消费端不得将其解释为 HTML。`item_list` 附件与内容
来源无关，两种邮件均可携带。

`custom_content` 使用新增字段号 4；已有模板邮件协议保持 wire-compatible。

## 长正文读取与缓存一致性

`MceMailList.summary_only=true` 返回不含正文的 MailData，并提供最多 160 字符的 preview。
`MceMailGet` 按当前认证 root 查询完整正文，不能由请求指定收件人；读取详情不自动标记已读。
summary_only 未设置时保留既有列表协议，长正文消费端应使用摘要列表和详情组合。

Store 增加 `PersonalMailboxVersion`。Manager 读取时校验持久化版本，变化后重载；Redis
以事务读取正文、元数据和版本，避免混合快照。其他 Store 实现需要适配新方法。
接入方的消息路由、客户端和界面消费端需配套适配；现存邮件无需迁移。
发送仍不支持业务去重键。邮箱容量默认 100，正文上限配置不改变容量或过期规则。
