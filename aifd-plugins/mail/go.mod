module github.com/li9chuan/aifirstdev/aifd-plugins/mail

go 1.26.0

require (
	github.com/li9chuan/aifirstdev/aifd-contracts v0.1.0
	github.com/li9chuan/aifirstdev/aifd-observe v0.1.0
	github.com/li9chuan/aifirstdev/aifd-rpc v0.1.0
	github.com/li9chuan/aifirstdev/aifd-runtime v0.1.0
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/jellydator/ttlcache/v3 v3.4.0
	github.com/redis/go-redis/v9 v9.20.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/li9chuan/aifirstdev/aifd-rdb v0.1.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.15.0 // indirect
)

replace github.com/li9chuan/aifirstdev/aifd-contracts => ../../aifd-contracts

replace github.com/li9chuan/aifirstdev/aifd-observe => ../../aifd-observe

replace github.com/li9chuan/aifirstdev/aifd-rpc => ../../aifd-rpc

replace github.com/li9chuan/aifirstdev/aifd-runtime => ../../aifd-runtime

replace github.com/li9chuan/aifirstdev/aifd-rdb => ../../aifd-rdb
