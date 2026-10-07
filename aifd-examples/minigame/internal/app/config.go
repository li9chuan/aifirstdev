package app

import (
	"context"
	"fmt"
	"time"

	cfgmemory "github.com/li9chuan/aifirstdev/aifd-config/adapters/memory"
	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
	cfgruntime "github.com/li9chuan/aifirstdev/aifd-config/runtime"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter"
)

const defaultCounterLimit int32 = 10

// CounterConfigBundle 是 minigame 示例拥有的强类型配置包数据。
type CounterConfigBundle struct {
	Limit int32
}

// ConfigApp 持有 minigame 的 typed bundle 配置管理器。
type ConfigApp struct {
	Loader  *cfgmemory.Loader[CounterConfigBundle]
	Manager *cfgruntime.Manager[CounterConfigBundle]
}

// NewConfigApp 创建并提交默认配置包。
func NewConfigApp(ctx context.Context) (*ConfigApp, error) {
	loader := cfgmemory.NewLoader(counterBundle("v1", defaultCounterLimit))
	manager, err := cfgruntime.NewManager(loader, config.ValidatorFunc[CounterConfigBundle](validateCounterConfig))
	if err != nil {
		return nil, err
	}
	result, err := manager.Reload(ctx)
	if err != nil {
		return nil, err
	}
	if !result.Applied {
		return nil, fmt.Errorf("minigame config reload rejected: %+v", result.Issues)
	}
	return &ConfigApp{Loader: loader, Manager: manager}, nil
}

// CounterLimit 返回 counter 当前配置上限。
func (a *ConfigApp) CounterLimit() int32 {
	if a == nil || a.Manager == nil {
		return defaultCounterLimit
	}
	bundle, ok := a.Manager.Current()
	if !ok {
		return defaultCounterLimit
	}
	if bundle.Data.Limit <= 0 {
		return defaultCounterLimit
	}
	return bundle.Data.Limit
}

// SetCounterLimit 替换内存配置并执行一次 prepare-then-commit reload。
func (a *ConfigApp) SetCounterLimit(ctx context.Context, limit int32) error {
	if a == nil || a.Loader == nil || a.Manager == nil {
		return fmt.Errorf("config app is nil")
	}
	a.Loader.SetBundle(counterBundle(fmt.Sprintf("limit-%d", limit), limit))
	result, err := a.Manager.Reload(ctx)
	if err != nil {
		return err
	}
	if !result.Applied {
		return fmt.Errorf("counter config reload rejected: %+v", result.Issues)
	}
	return nil
}

func counterBundle(version string, limit int32) *config.Bundle[CounterConfigBundle] {
	hash := fmt.Sprintf("counter-limit:%d", limit)
	return &config.Bundle[CounterConfigBundle]{
		Version:  version,
		LoadedAt: time.Now().UTC(),
		Data:     CounterConfigBundle{Limit: limit},
		Manifest: config.Manifest{
			DataHash: hash,
			Tables: map[string]config.TableManifest{
				counter.ConfigTableCounter: {
					Name:     counter.ConfigTableCounter,
					RowCount: 1,
					Hash:     hash,
				},
			},
			Labels: map[string]string{
				"example": "minigame",
			},
		},
	}
}

func validateCounterConfig(_ context.Context, bundle *config.Bundle[CounterConfigBundle]) []config.Issue {
	if bundle == nil {
		return []config.Issue{{
			Severity: config.IssueError,
			Table:    counter.ConfigTableCounter,
			Key:      counter.ConfigRowDefault,
			Field:    counter.ConfigFieldLimit,
			Message:  "counter config bundle is nil",
		}}
	}
	if bundle.Data.Limit <= 0 {
		return []config.Issue{{
			Severity: config.IssueError,
			Table:    counter.ConfigTableCounter,
			Key:      counter.ConfigRowDefault,
			Field:    counter.ConfigFieldLimit,
			Message:  "counter limit must be positive",
		}}
	}
	return nil
}
