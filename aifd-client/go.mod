module github.com/li9chuan/aifirstdev/aifd-client

go 1.26.0

require (
	github.com/li9chuan/aifirstdev/aifd-observe v0.1.0
	github.com/li9chuan/aifirstdev/aifd-proxy v0.1.0
	github.com/li9chuan/aifirstdev/aifd-rpc v0.1.0
	github.com/li9chuan/aifirstdev/aifd-schema v0.1.0
	github.com/coder/websocket v1.8.12
	google.golang.org/protobuf v1.36.9
)

require github.com/li9chuan/aifirstdev/aifd-contracts v0.1.0 // indirect

replace github.com/li9chuan/aifirstdev/aifd-observe => ../aifd-observe

replace github.com/li9chuan/aifirstdev/aifd-contracts => ../aifd-contracts

replace github.com/li9chuan/aifirstdev/aifd-proxy => ../aifd-proxy

replace github.com/li9chuan/aifirstdev/aifd-rpc => ../aifd-rpc

replace github.com/li9chuan/aifirstdev/aifd-schema => ../aifd-schema
