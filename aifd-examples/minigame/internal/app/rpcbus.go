package app

import (
	rpcnats "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
)

const defaultNATSURL = "nats://127.0.0.1:4222"

// NewNATSBus 创建拆分服务入口使用的 NATS RPC bus。
func NewNATSBus(serverURL string, serviceName string) (*rpcnats.Bus, error) {
	return NewNATSBusWithConfig(rpcnats.FileConfig{}, serverURL, serviceName)
}

// NewNATSBusWithConfig 创建使用共享 NATS client 配置并允许业务入口 CLI 覆盖 URL 的 RPC bus。
func NewNATSBusWithConfig(fileCfg rpcnats.FileConfig, serverURL string, serviceName string) (*rpcnats.Bus, error) {
	cfg := ServiceFileConfig{RPC: RPCFileConfig{NATS: fileCfg}}
	natsCfg, err := cfg.NATSConfig(serviceName, serverURL)
	if err != nil {
		return nil, err
	}
	return rpcnats.NewBus(natsCfg)
}
