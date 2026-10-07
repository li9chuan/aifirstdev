package contracts

import (
	"time"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
)

// RouteKind 区分 client、service 和 admin 入口。
type RouteKind string

const (
	RouteKindClient RouteKind = "client"
	RouteKindServer RouteKind = "server"
	RouteKindAdmin  RouteKind = "admin"
	RouteKindStream RouteKind = "stream"
)

// Route 是已注册的 RPC 入口。
type Route struct {
	Name    string
	Kind    RouteKind
	Service base.ServiceRole
	Handler Handler
	Queue   string
	Timeout time.Duration
}

// Registry 存储 route 注册。
type Registry interface {
	Register(Route) error
	Routes() []Route
}
