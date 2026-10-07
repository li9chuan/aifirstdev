package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	aifdclient "github.com/li9chuan/aifirstdev/aifd-client"
	"github.com/li9chuan/aifirstdev/aifd-client/adapters/proxyws"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
)

const defaultTimeout = 10 * time.Second

// Options configures the minigame business client.
type Options struct {
	URL             string
	Timeout         time.Duration
	AwaitTimeout    time.Duration
	MaxMessageBytes int64
}

// Client is a small minigame-facing wrapper over aifd-client.
type Client struct {
	rpc     *aifdclient.Client
	timeout time.Duration

	authMu   sync.Mutex
	authWait []chan pbmsg.MseAuth
}

// New connects to the minigame proxy and starts the client read loop.
func New(ctx context.Context, opts Options) (*Client, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("minigame client url is empty")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	transport, err := proxyws.NewWebSocketTransport(ctx, proxyws.WebSocketConfig{
		URL:             opts.URL,
		MaxMessageBytes: opts.MaxMessageBytes,
		DialTimeout:     opts.Timeout,
	})
	if err != nil {
		return nil, err
	}
	awaitTimeout := opts.AwaitTimeout
	if awaitTimeout <= 0 {
		awaitTimeout = opts.Timeout
	}
	rpcClient, err := aifdclient.New(transport, aifdclient.Options{AwaitTimeout: awaitTimeout})
	if err != nil {
		_ = transport.Close(context.Background())
		return nil, err
	}
	c := &Client{rpc: rpcClient, timeout: opts.Timeout}
	if err := rpcClient.On(c.handleAuth); err != nil {
		_ = rpcClient.Close()
		return nil, err
	}
	if err := rpcClient.Start(); err != nil {
		_ = rpcClient.Close()
		return nil, err
	}
	return c, nil
}

// Login sends MceAuth and waits for the MseAuth push returned by proxy login.
func (c *Client) Login(ctx context.Context, req *pbmsg.MceAuth) (pbmsg.MseAuth, error) {
	if c == nil || c.rpc == nil {
		return pbmsg.MseAuth{}, fmt.Errorf("minigame client is nil")
	}
	if req == nil {
		return pbmsg.MseAuth{}, fmt.Errorf("minigame auth request is nil")
	}
	wait := make(chan pbmsg.MseAuth, 1)
	c.authMu.Lock()
	c.authWait = append(c.authWait, wait)
	c.authMu.Unlock()
	if err := c.rpc.Call(req); err != nil {
		c.removeAuthWait(wait)
		return pbmsg.MseAuth{}, err
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		c.removeAuthWait(wait)
		return pbmsg.MseAuth{}, ctx.Err()
	case rsp := <-wait:
		if rsp.GetRet() != auth.RetOK {
			return rsp, fmt.Errorf("minigame auth failed ret=%d", rsp.GetRet())
		}
		return rsp, nil
	}
}

// AddCounter sends MceCounterAdd and waits for the typed MseCounterState reply.
func (c *Client) AddCounter(ctx context.Context, delta int32) (pbmsg.MseCounterState, error) {
	if c == nil || c.rpc == nil {
		return pbmsg.MseCounterState{}, fmt.Errorf("minigame client is nil")
	}
	done := make(chan struct{})
	var result pbmsg.MseCounterState
	var resultErr error
	if err := c.rpc.Call(&pbmsg.MceCounterAdd{Delta: delta}, func(rsp *pbmsg.MseCounterState, err error) {
		if err != nil {
			resultErr = err
		} else if rsp == nil {
			resultErr = fmt.Errorf("minigame counter response is nil")
		} else {
			result = *rsp
		}
		close(done)
	}); err != nil {
		return pbmsg.MseCounterState{}, err
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		return pbmsg.MseCounterState{}, ctx.Err()
	case <-done:
		return result, resultErr
	}
}

// Close closes the underlying client and proxy websocket transport.
func (c *Client) Close() error {
	if c == nil || c.rpc == nil {
		return nil
	}
	return c.rpc.Close()
}

func (c *Client) handleAuth(rsp *pbmsg.MseAuth) {
	if rsp == nil {
		return
	}
	c.authMu.Lock()
	waiters := c.authWait
	c.authWait = nil
	c.authMu.Unlock()
	for _, wait := range waiters {
		wait <- *rsp
		close(wait)
	}
}

func (c *Client) removeAuthWait(wait chan pbmsg.MseAuth) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	for i, candidate := range c.authWait {
		if candidate != wait {
			continue
		}
		copy(c.authWait[i:], c.authWait[i+1:])
		c.authWait[len(c.authWait)-1] = nil
		c.authWait = c.authWait[:len(c.authWait)-1]
		return
	}
}

func (c *Client) withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}
