# aifd-config Viper Adapter

`adapters/viper` 是可选配置入口，用于把 TOML、YAML、JSON、默认值和环境变量覆盖解码成业务或入口拥有的 typed config。

底层框架包不直接依赖 Viper。`aifd-proxy`、`aifd-rpc`、`aifd-rdb`、`aifd-observe` 和 `aifd-runtime` 只接收各自的 typed `Config` 或 `Options`。

## Example

```toml
[log]
level = "info"
stdout = true
dir = "./logs"

[rpc.nats]
servers = ["nats://127.0.0.1:4222"]
timeout = "3s"

[rdb.redis]
addrs = ["127.0.0.1:6379"]
password = ""
dial_timeout = "1s"
read_timeout = "1s"
write_timeout = "1s"

[proxy]
max_message_bytes = 4194304
duplicate_policy = "kick_old"
```

实例差异仍由命令行参数提供，例如 `--conf`、`--addr`、`--sid`、`--pprof` 和 `--metrics`。共享 TOML 不包含监听地址、实例编号、Redis DB index、Redis key prefix、WS path、ping topic 或 pong topic。

```go
cfg, err := viper.Load[AppConfig](ctx, viper.Options{
	ConfigFile: "proxy.toml",
	EnvPrefix:  "AIFD",
	EnvKeys: []string{
		"rpc.nats.servers",
		"rpc.nats.timeout",
	},
	Validator: func(out any) error {
		cfg := out.(*AppConfig)
		return cfg.Validate()
	},
})
```

业务项目可以不使用该 adapter，直接实现自己的 loader 或 typed config reader。

## 相关文档

- [Config 模块入口](../../README.md)
- [配置工作流](../../../aifd-rules/skills/config-table-workflow/SKILL.md)
