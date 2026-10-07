// Package rpc 提供面向业务 feature 的 protobuf RPC 门面。
//
// 业务代码默认只注册 func(*Session) 或 func(*Session, *Req)，并通过 Session 显式
// Response、Error、Tell、Request 或 Forward。底层 Message、Bus、Route 等契约保留在
// contracts 子包，供 adapter、proxy、robot 和服务启动层使用。
package rpc
