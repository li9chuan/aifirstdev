# Mail owner 接入规则

邮件由 `aifd-plugins/mail/service` 独占运行期权威状态，每个 Redis 命名空间只允许一个 owner。user module 只解析可信资格并 Forward，不持有 Manager，不直接保存邮件。queue group 只负责派发，不能当作 owner 选举。

## 收件与删除

- 固定历史领奖人时，业务固定 UID 后发 PERSONAL。GLOBAL 面向全部用户，GROUP 每封只使用一个 group_key；user.GroupResolver 提供当前用户所属组列表，不接受客户端自报资格。
- 组名是不透明的业务值，Mail 只做精确匹配，不设置独立游戏服筛选或组间组合条件。按服发送也用业务组，例如 server:1；组合人群需要业务定义自己的单个组名。
- 批量选组使用 Mail.AddBatch，每组一条请求、独立 MailID。用户属于多个目标组时允许收到并领取多封，不跨组按内容去重。
- 共享副本的 List/Get/Read/Claim/DeleteRead 都检查当前组资格。换组不得重置已读、领取和 CD；HDEL 后重新入组不能再次投递同一邮件。
- 游标沿用全局 last_global_mail_id 和按组 group_cursors。离组、空组或旧快照都不得清空进度；没有组合范围游标。
- 组资格取本次请求入口的业务快照，不默认增加逐封资格 RPC 或 Awarder 二次组校验。
- after_mail_id 是 ID 分页条件。登录、资格变化和刷新从 0 获取，不把它当作可靠增量订阅。

## 领取

- Mail 内部顺序为锁内检查并保存 CD、锁外普通发奖 RPC、成功后标记领取。CD 只属于个人副本。
- 整批领取固定一次邮箱引用，begin/finish 复用对象；不逐封重复 acquire。
- RPC 进行中或 CD 未到期时拒绝；RPC 超时且 CD 到期后允许客户端重试。没有后台自动发奖重试。
- 默认不要求 Game 幂等或领奖收据，也不提供跨服务资产事务。Game 已执行但丢失回包后，重试可能重复发奖；不得宣传为不重不漏。
- Awarder 必须遵守 Context，成功需要实际业务回包。nil Awarder 不能默认为发奖成功。
- user handler 用 Forward，让出 Game 的 UID worker。分进程调用普通发奖 RPC，同进程注入本地方法，避免同一 worker 同步请求自己。

## 保存与恢复

- 每封邮件一个 HashSaver，邮箱元数据另一个 HashSaver。邮箱协调锁先于 Saver 锁；RPC、Push、加载和 Flush 在锁外执行。
- 同 UID 首次加载合并，操作引用固定 canonical 对象。淘汰必须阻止新引用，锁外 Flush 成功后才能移除；失败保留原对象。
- List 只快照全局和当前组。邮件目标发送后不变，单封操作直接检查个人副本；旧 PERSONAL 共享副本在加载时按有效源恢复类型与 group_key，不能因缩小快照而扩大资格。
- RDB 非重试错误保留意图，只有显式 Flush 成功或新最终意图完成才解决；后台不自动重试，不写 replay journal。错误回传一次后不能清零并允许淘汰，Close 重试遵守相同边界。
- 保存成功表示 RDB 接收入队；普通关闭排空队列，强杀仍可能丢失近期状态。不得把多个 field/key 描述为原子提交。
- 不用单封邮件 TTL 控制整个 HSET。旧 key TTL 必须先清除再读取正文，逐封按 end_time_unix 清理。
- 升级停止旧写入者后再切换，直接保留旧全局和按组进度，不通过重发全部历史共享邮件修复未知漏投。
- 已发布的多组/游戏服字段保留原编号与类型并废弃，新发送拒绝使用。存量单组可直接恢复；尚有效的旧多组或限服共享邮件必须显式转换，禁止静默丢弃限制。未发布组合游标草案写入的元数据不能当作空进度加载。

接口、默认参数、升级要求和实际验证入口以 [插件 README](../../../../aifd-plugins/mail/README.md) 为准。
