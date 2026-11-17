package app

import (
	"context"
	"fmt"
	"net/http"

	authproxy "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth/proxy"
	proxylocal "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/local"
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
	server   *proxyws.Server
}

// NewProxyApp 创建 websocket proxy 入口；local bus 使用示例桥，NATS bus 使用真实下行订阅。
func NewProxyApp(bus rpc.Bus, accountApp *AccountApp, configs ...proxyws.FileConfig) (*ProxyApp, error) {
	if bus == nil {
		return nil, fmt.Errorf("rpc bus is nil")
	}
	if accountApp == nil || accountApp.Verifier == nil || accountApp.Service == nil {
		return nil, fmt.Errorf("account authority is nil")
	}
	delivery := proxyruntime.NewDelivery()
	gateway := proxyruntime.NewGateway(nil, proxyruntime.NewRPCUpstream(bus, delivery))
	var downlink downlinkCloser
	if localBus, ok := bus.(*rpc.LocalBus); ok {
		downlink = proxylocal.NewDownlink(localBus, gateway, delivery)
	} else if provider, ok := bus.(natsConnProvider); ok {
		downlink = proxynats.NewDownlink(provider.NatsConn(), gateway, delivery, proxynats.Config{})
	}
	wsCfg := proxyws.Config{
		Bus:           bus,
		Gateway:       gateway,
		Delivery:      delivery,
		Downlink:      downlink,
		LoginHandler:  proxyc.NewLoginHandler(authproxy.Authenticate(bus, accountApp.Verifier, accountApp.Service)),
		AuthCheckMode: proxyc.AuthCheckPerRequest, AuthRevalidator: authproxy.Revalidator{Sessions: accountApp.Service},
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
	if err := handler.Validate(); err != nil {
		return nil, err
	}
	combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/demo/auth" {
			accountApp.serveDemoAuth(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
	return &ProxyApp{Gateway: gateway, Delivery: delivery, Handler: combined, Downlink: downlink, server: handler}, nil
}

// Close 停止示例本地 downlink 桥。
func (a *ProxyApp) Close() error {
	if a != nil {
		if a.server != nil {
			_ = a.server.Close(context.Background())
		}
	}
	if a != nil && a.Downlink != nil {
		return a.Downlink.Close()
	}
	return nil
}
