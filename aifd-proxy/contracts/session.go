package contracts

import (
	"errors"

	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

const (
	// These fields exist only inside trusted service RPC, never in MsgHead.
	HeaderProxyInstanceID = "proxyInstanceID"
	HeaderConnectionID    = "clientConnectionID"
	HeaderCallerKind      = "proxyCallerKind"
	HeaderSessionDeadline = "proxySessionDeadline"
	CallerClient          = "client"
	CallerProxy           = "proxy"
)

var ErrClientSessionMissing = errors.New("trusted client session reference is missing")

// ClientCaller is the narrow invocation metadata required by connection leases.
// Business rpc.Session satisfies it without exposing lower transport objects.
type ClientCaller interface {
	UID() int64
	Header(string) string
}

// ClientSessionRef reads the reference injected by an authenticated gateway.
func ClientSessionRef(caller ClientCaller) (*proxypb.ClientSessionRef, error) {
	if caller == nil || caller.UID() <= 0 || caller.Header(HeaderCallerKind) != CallerClient {
		return nil, ErrClientSessionMissing
	}
	ref := &proxypb.ClientSessionRef{ProxyInstanceId: caller.Header(HeaderProxyInstanceID), ConnectionId: caller.Header(HeaderConnectionID)}
	if !ValidSessionRef(ref) {
		return nil, ErrClientSessionMissing
	}
	return ref, nil
}

// ValidSessionRef rejects partial connection identities.
func ValidSessionRef(ref *proxypb.ClientSessionRef) bool {
	return ref.GetProxyInstanceId() != "" && ref.GetConnectionId() != ""
}
