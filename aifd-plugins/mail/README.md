# aifd-plugins/mail

官方可选邮件插件，通过 service/user module 显式组合。应用的每个邮件命名空间只能运行一个 Mail owner；同进程和分进程部署使用相同的状态归属。

## 所有权与接入

- service module 独占个人邮箱、共享邮件池、处理游标、领取 CD 与状态。
- user module 只认证用户、解析当前收件资格，再通过 Session.Forward 转交 Mail。
- 普通发奖能力由 Awarder 注入 service。同进程调用本地资产方法，分进程适配普通发奖 RPC。
- 构造函数只检查依赖。启动顺序为 Recover → RegisterRPC → Start。
- 关闭时先停止新请求，排空已接纳操作并 Flush；之后应用再关闭 DB、Bus、Redis client。

service.NewModule 现在返回 (*Module, error)，通过 Options.DB 注入 aifd-rdb，而不接收外部 Mailbox。user.Options 只保留 GroupResolver，不再注入邮箱、Awarder 或静态 RealmID。应用入口只创建一个 owner，不把其 Manager 传入 Game。

GroupResolver.MailGroupKeys 每次从可信用户状态返回当前所属的组列表，例如 `["L1", "server:1"]`。框架只对组名做精确匹配，不解析其中的业务含义。客户端 MceMailList.group_keys 已废弃并被忽略，不能扩大资格。未配置 resolver 时仍能访问本人的个人邮件和全局邮件；组邮件必须匹配解析器返回的组。解析失败则拒绝操作。

组资格使用本次请求入口取得的业务快照。Mail 不额外回调业务查询每封邮件的资格，也不要求 Awarder 二次校验组；需要固定历史领奖权时仍发送 PERSONAL。

Game 客户端 handler 使用 Forward 而非同步 Request 等待 Mail，让出当前 UID worker。否则 Mail 再向相同 Game UID 请求发奖会形成等待环。同进程发奖适配器直接调用本地方法，不向当前 worker 同步请求自己。

## 邮件类型与共享处理

- PERSONAL：调用方固定 UID；后续变更等级、公会或游戏服不撤销邮件资格。
- GLOBAL：全部用户，不附加组或游戏服条件。
- GROUP：一封邮件只对应一个非空 group_key，当前用户属于这个组即可访问。
- 当前资格用于 List/Get/Read/Claim/DeleteRead。离组后隐藏共享邮件，再加入时保留已读、领取和 CD。

共享邮件只保存一份，通过 List 拉取生成个人副本，不遍历全量用户，也不进行全量用户推送。有效期内后来满足条件或新注册的用户也可接收。需要固定历史领奖人的奖励，应由业务固定 UID 后发送个人邮件。

批量选组时使用 Mail.AddBatch，为每个组分别构造一条 GROUP 请求，各自生成独立 MailID。属于多个目标组的用户会收到多封，可以分别领取。即使内容相同，也不跨组或跨批次去重。按游戏服发送也使用普通组，例如 `server:1`；需要“1 服 L1”时，由业务定义并解析一个明确的组名，Mail 不组合条件。

```go
req := &mailpb.MailAddBatchRequest{MailList: []*mailpb.MailAddRequest{
    {Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: "L0", Base: &mailpb.MailBase{TemplateId: 1}},
    {Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: "L1", Base: &mailpb.MailBase{TemplateId: 1}},
}}
```

共享进度直接使用 MailboxMeta.last_global_mail_id 和每个组的 MailGroupCursor，不保存组合条件游标。删除直接 HDEL。离组、暂时没有有效组邮件或旧快照都不会清空游标，重新入组不重发已处理邮件。后来加入此前未处理的组，仍能收到该组尚未过期的历史邮件。组名应复用于业务群体；邮箱元数据保留曾处理组的进度。

共享同步处理完整快照，不受客户端分页限制。共享 ID 在同一写锁内生成、入队和发布；恢复只读取共享 HSET 并重建 int64 索引，不使用旧 ZSET 或 float64 分数。恢复后的新 ID 必须大于已有共享最大 ID；运行环境仍需遵守 Snowflake 时钟约束。

List 的共享快照只包含全局邮件和当前可信组，不扫描无关组。收件类型和 group_key 在发送后不变，个人副本保留这些字段；Get/Read/Claim/DeleteRead 直接检查副本，不再创建共享池快照。旧 PERSONAL 共享副本在加载时按有效共享源恢复目标。

after_mail_id 是邮件 ID 分页条件，不是可靠投递订阅游标。登录、收件资格变化和主动刷新必须从 0 重新获取。

## 领取：Mail CD 与状态

领取入口只有 Mail.Claim，不要求 Game 保存领奖收据或提供幂等实现。

1. 在邮箱锁内检查有效期、资格、附件和 claimed。
2. 已领取则跳过；RPC 进行中或 CD 未到期则返回忙。
3. 将当前用户副本的 claim_retry_at_unix_ms 更新为当前时间加 CD，保存意图被 RDB 接收后设置内存进行中标记。
4. 释放 owner 锁，执行一次普通 AwardMail。
5. 成功回包后标记 read/claimed 并保存；失败或超时保持未领取。两者均清除进行中标记，CD 不回退。
6. RPC 已超时且 CD 已到期时，允许客户端再次尝试。Mail 没有后台发奖重试任务。

共享母邮件的 CD 始终为 0。每批发奖共用一个超时预算，未开始处理的邮件不写 CD。客户端断开不会直接丢弃已接纳操作的完成结果。进行中的邮箱被缓存引用固定，不允许淘汰，容量裁剪和物理过期清理延后至领取结束。

领取整批只取得并固定一次邮箱引用，begin/finish 复用该对象，发奖时释放邮箱锁；不会逐封再次加载或 acquire 邮箱。

Awarder 必须遵守 Context；nil 结果表示业务成功，不能仅表示消息已发送。未配置或 nil AwarderFunc 返回错误且不修改 CD。晚到回包使用已有 RPC inbox 超时清理机制，不增加持久化尝试号或令牌。

**此流程不提供跨服务发奖幂等。**Game 已加资产但回包丢失时，CD 后重试可能再次加资产。邮件或 Game 使用异步保存时也存在进程强杀丢失未落盘状态的窗口。

## 持久化与缓存

每封邮件一个 HashSaver[*MailData]，元数据一个 HashSaver[*MailboxMeta]，沿用现有 key 和 protobuf 编码：

| Key | Field | 内容 |
| --- | --- | --- |
| aifd:mail:user:<uid>:data | mailID 十进制字符串 | MailData |
| aifd:mail:user:<uid>:meta | meta | MailboxMeta |
| aifd:mail:shared:data | mailID 十进制字符串 | MailData |

Prefix 可以配置。RDB 接收入队后的内存对象为运行期权威状态，入队成功不等于已经持久化。正常 Close/Flush 排空队列；强杀可能丢失近期邮件、CD、已读、领取或部分游标进度。不同 field/key 之间没有事务保证。

同 UID 首次加载合并。热点操作不查询 Redis 版本；缓存淘汰先阻止新访问，在锁外 Flush，成功后移除。失败保留原对象；容量满且无法安全淘汰时返回错误。

RDB 权限、类型等非重试写入错误保留最终意图，后台不自动重试，也不写 replay journal。后续显式 Flush 对旧失败意图各尝试一次，只有实际写入成功或被新的最终意图替代并完成后才算解决。错误不会因前一次已回传而消失，淘汰和 Close 重试不会加载旧状态覆盖内存。

Saver 不设置整个邮箱 HSET 的 TTL，避免短期邮件使长期邮件提前消失。首次加载旧邮箱时先清除遗留 key TTL，再读取正文与元数据，防止加载期间 key 到期；过期使用邮件 end_time_unix，由共享池/已加载邮箱定时清理和冷邮箱加载时清理。冷邮箱可能保留过期数据至下次访问。

默认邮箱容量 100、缓存 10,000、空闲 20 分钟、邮件有效期 20 天、清理间隔 5 分钟、ClaimCD 5 秒、AwardTimeout 3 秒。容量仍移除最早邮件，不承诺永久保管未领奖励。

## 协议与发送结果

客户端的 List/Get/Read/Claim/DeleteRead 消息保持，新增内部 Mail.List/Get/Read/Claim/DeleteRead；UID 来自 RPC Session，内部请求只携带可信 group_keys 和操作参数。这里的 group_keys 是用户所属组列表，与发送请求的单个 group_key 含义不同。

MailAddRequest.group_key 使用新字段 8，MailData.group_key 使用新字段 12。已发布的 target_realms、发送目标 group_keys 保留原编号和类型并标记 deprecated，用于识别旧数据；新发送请求携带这些废弃字段会被拒绝，不能静默忽略筛选。

返回码：OK=0、Invalid=1、NotFound=2、Internal=3、Partial=4、Busy=5、Timeout=6。MseMailClaim 返回本次确认成功的 ID，以及未成功邮件中最早的 retry_at_unix_ms；时间到期仍受进行中状态约束。

Mail.Add/AddBatch 先完成全部输入校验，个人 UID 按首次出现顺序去重，执行时遇错停止。mail_id_list 在失败回包中仍保留成功前缀；已有成功时为 Partial。Batch 的目标按原请求顺序展开，共享项占一个目标。调用方必须处理成功前缀，不能直接将整批失败当作没有写入。跨请求发送去重由业务负责。

模板与自定义内容互斥；自定义正文为纯文本。标题最多 100 个 Unicode 字符，正文默认 5000，可通过 MaxBodyRunes 调整。摘要列表只返回最多 160 字符的 preview；Get 获取完整正文且不自动标记已读。

## 从 v0.4.2 升级

- 停止全部旧 Mail 写入者并备份，再启动新 owner，禁止混跑。
- 原邮件与已读/领取标记保留，新 CD 字段默认 0。
- 旧 PERSONAL 类型的共享副本由有效共享源 MailID 恢复资格。
- 直接沿用旧全局游标和按组游标。旧数据只有一个目标组且没有游戏服限制时，恢复为单个 group_key；旧 PERSONAL 邮件仍按固定 UID 处理。
- 尚有效的旧多组共享邮件、带游戏服限制的共享邮件无法在保留原语义的同时自动变成单组。恢复时报错并指出 MailID，切换前应显式转换或等待过期，不能通过丢弃限制扩大收件人。已过期的旧邮件可正常清理。
- 上一版未发布方案的组合游标已移除，字段 3 保留禁用；若用该草案写入过测试元数据，需要先显式处理，加载不会将其静默当作空进度。
- 旧 version 和 ZSET/SET 索引停止读写，可保留，不需要扫描删除。
- 旧数据不能精确区分已删除与历史漏投，默认不重发旧游标以内邮件；历史漏奖由业务根据结算事实补发。
- 新 owner 使用 group_key 和新领取状态字段后，不支持直接降级接回旧 owner。回退需要对应旧数据快照。

## 验证

快速验证运行 go test ./aifd-plugins/mail/... ./aifd-rdb/...；并发检查加 -race。

真实 Redis 测试由 AIFD_REDIS_SERVER 指定可执行文件，或从 PATH 寻找 redis-server。设置 AIFD_MAIL_REDIS_REQUIRED=1 后缺失环境直接失败。测试启动独立端口与临时目录，使用 AOF，并验证 Redis/owner 重启；不会接入现有实例。

TestRealRedisHashRecoveryCDAndDelete 检查实际 HSET/HDEL、旧 TTL 接管、单组目标、独立组游标、CD 和 Redis/owner 重启恢复。mailbox 的真实 Redis 回归另覆盖加载跨越旧 TTL 边界、反复 HSET/HDEL 权限失败、修复后淘汰及 Close 重试。TestRealRedisNATSOwnerProcessRestartAndTimeoutRetry 启动真实 NATS 与独立 Mail 测试进程，Game 使用单 worker 和普通非幂等发奖。测试覆盖批量组邮件、换组及重新入组、丢回包、CD 到期重试、晚到旧回包及删除状态重启恢复，不把重复发奖边界描述为幂等保证。

相关规则见 [Mail owner 接入规则](../../aifd-rules/skills/runtime-module-development/references/mail-owner.md)。
