package luban

import (
	"context"
	"fmt"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

// BundleBuilder 基于业务项目拥有的 Luban 生成 cfgtable 构建完整配置包。
type BundleBuilder[T any] func(context.Context) (*config.Bundle[T], error)

// Loader 是 Luban/cfgtable typed bundle builder 的轻包装。
//
// aifd-config 不理解真实 Luban 表结构，也不把 cfgtable 全量映射为通用表模型。
// 业务项目负责在同一轮 builder 中构建 Data、派生索引与 manifest。
type Loader[T any] struct {
	BuildBundle BundleBuilder[T]
}

// Load 调用业务 builder 构建完整 Bundle。
func (l Loader[T]) Load(ctx context.Context) (*config.Bundle[T], error) {
	if l.BuildBundle == nil {
		return nil, fmt.Errorf("luban bundle builder is nil")
	}
	bundle, err := l.BuildBundle(ctx)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("luban bundle builder returned nil")
	}
	bundle.Manifest = bundle.Manifest.Clone()
	return bundle, nil
}
