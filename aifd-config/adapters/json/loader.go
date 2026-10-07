package json

import (
	"context"
	stdjson "encoding/json"
	"time"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

type document[T any] struct {
	Version  string          `json:"version"`
	LoadedAt time.Time       `json:"loaded_at,omitempty"`
	Manifest config.Manifest `json:"manifest,omitempty"`
	Data     T               `json:"data"`
}

// Loader 从 JSON 字节加载强类型配置包，用于小型配置、工具和测试。
type Loader[T any] struct {
	Data []byte
}

// NewLoader 基于字节创建 JSON Loader。
func NewLoader[T any](data []byte) *Loader[T] {
	return &Loader[T]{Data: append([]byte(nil), data...)}
}

// Load 将 JSON 解析为强类型配置包。
func (l *Loader[T]) Load(context.Context) (*config.Bundle[T], error) {
	return ParseBundle[T](l.Data)
}

// ParseBundle 解析包含 data 与 manifest 的 typed bundle JSON。
func ParseBundle[T any](data []byte) (*config.Bundle[T], error) {
	var doc document[T]
	if err := stdjson.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &config.Bundle[T]{
		Version:  doc.Version,
		LoadedAt: doc.LoadedAt,
		Data:     doc.Data,
		Manifest: doc.Manifest.Clone(),
	}, nil
}
