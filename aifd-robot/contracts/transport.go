package contracts

import (
	"context"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Transport is the narrow robot client-side connection abstraction.
type Transport interface {
	Send(context.Context, rpc.Message) error
	Recv(context.Context) (rpc.Message, error)
	Close(context.Context) error
}
