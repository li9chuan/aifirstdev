// Package nats 实现 NATS request/reply 和 JetStream adapter。
//
// 该 adapter 将 NATS 专属类型限制在边界层。除服务启动装配外，业务包应依赖 contracts.Bus，
// 而不是直接 import 该包。
package nats
