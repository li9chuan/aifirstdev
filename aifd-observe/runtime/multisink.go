package runtime

import (
	"context"
	"errors"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

// MultiSink 将同一批事件写入多个 Sink。
type MultiSink []contracts.Sink

// Write 将批次转发到每个非空 Sink，并合并所有写入错误。
func (m MultiSink) Write(ctx context.Context, events []contracts.Event) error {
	var joined error
	for _, sink := range m {
		if sink == nil {
			continue
		}
		if err := sink.Write(ctx, events); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}
