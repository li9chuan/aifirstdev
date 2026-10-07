package contracts

import "context"

// Loader 从文件、生成访问器或测试内存中加载已准备好的强类型配置包。
type Loader[T any] interface {
	Load(context.Context) (*Bundle[T], error)
}
