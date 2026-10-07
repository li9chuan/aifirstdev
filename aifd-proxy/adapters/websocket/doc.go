// Package websocket 提供客户端 websocket 连接 adapter。
//
// Adapter 只处理 binary frame、proxy packet codec、登录状态和上下行转发；业务登录 PB
// 解析、token 校验和玩法逻辑必须留在业务 LoginHandler 或后端服务中。
package websocket
