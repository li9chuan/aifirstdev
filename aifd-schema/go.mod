module github.com/li9chuan/aifirstdev/aifd-schema

go 1.26.0

require (
	github.com/li9chuan/aifirstdev/aifd-contracts v0.1.0
	github.com/li9chuan/aifirstdev/aifd-rpc v0.1.0
	google.golang.org/protobuf v1.36.9
)

replace github.com/li9chuan/aifirstdev/aifd-contracts => ../aifd-contracts

replace github.com/li9chuan/aifirstdev/aifd-rpc => ../aifd-rpc
