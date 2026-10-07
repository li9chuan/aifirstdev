module github.com/li9chuan/aifirstdev/aifd-rpc

go 1.26.0

require (
	github.com/li9chuan/aifirstdev/aifd-contracts v0.1.0
	github.com/nats-io/nats.go v1.52.0
	google.golang.org/protobuf v1.36.9
)

require (
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
)

replace github.com/li9chuan/aifirstdev/aifd-contracts => ../aifd-contracts
