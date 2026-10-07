package app

import (
	"fmt"
	"net/http"

	"github.com/li9chuan/aifirstdev/aifd-auth/token"
	authproxy "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth/proxy"
	proxynats "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/nats"
	proxyws "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/websocket"
	proxyc "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	natsgo "github.com/nats-io/nats.go"
)

type downlinkCloser interface {
	proxyc.DownlinkSubscriber
	Close() error
}

type natsConnProvider interface {
	NatsConn() *natsgo.Conn
}

// ProxyApp 持有 proxy 入口需要的 Gateway、Delivery 和 HTTP handler。
type ProxyApp struct {
	Gateway  *proxyruntime.Gateway
	Delivery *proxyruntime.Delivery
	Handler  http.Handler
	Downlink downlinkCloser
}

// NewProxyApp 创建 websocket proxy 入口；local bus 使用示例桥，NATS bus 使用真实下行订阅。
func NewProxyApp(bus rpc.Bus, verifier *token.Verifier, configs ...proxyws.FileConfig) (*ProxyApp, error) {
	if bus == nil {
		return nil, fmt.Errorf("rpc bus is nil")
	}
	if verifier == nil {
		return nil, fmt.Errorf("token verifier is nil")
	}
	gateway := proxyruntime.NewGateway(nil, proxyruntime.NewRPCUpstream(bus))
	delivery := proxyruntime.NewDelivery()
	var downlink downlinkCloser
	if localBus, ok := bus.(*rpc.LocalBus); ok {
		downlink = NewLocalDownlinkBridge(localBus, gateway, delivery)
	} else if provider, ok := bus.(natsConnProvider); ok {
		downlink = proxynats.NewDownlink(provider.NatsConn(), gateway, delivery, proxynats.Config{})
	}
	wsCfg := proxyws.Config{
		Gateway:         gateway,
		Delivery:        delivery,
		Downlink:        downlink,
		LoginHandler:    proxyc.NewLoginHandler(authproxy.Authenticate(bus, verifier)),
		DuplicatePolicy: proxyws.DuplicateKickOld,
	}
	if len(configs) > 0 {
		var err error
		wsCfg, err = configs[0].Apply(wsCfg)
		if err != nil {
			return nil, err
		}
	}
	handler := proxyws.NewServer(wsCfg)
	return &ProxyApp{Gateway: gateway, Delivery: delivery, Handler: handler, Downlink: downlink}, nil
}

// Close 停止示例本地 downlink 桥。
func (a *ProxyApp) Close() error {
	if a != nil && a.Downlink != nil {
		return a.Downlink.Close()
	}
	return nil
}
