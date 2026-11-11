# Lumberjack v2.2.1 内部副本

来源：[natefinch/lumberjack v2.2.1](https://github.com/natefinch/lumberjack/tree/v2.2.1)。
复制上游 `lumberjack.go`、`chown.go`、`chown_linux.go`，许可证保留在 [LICENSE](LICENSE)。
本目录是 `aifd-log` 的内部实现，不是独立 Go module，也不向业务暴露。

上游 v2 的 `Close` 只关闭文件，没有终止归档清理协程的公开接口。本地仅补齐生命周期：

- `Close` 成为终止操作，停止接受写入和切分；重复、并发关闭等待同一次关闭完成。
- 在原有互斥锁内关闭清理通知通道，排空已提交的清理任务并等待 worker 退出。
- 保持上游大小切分、归档命名、保留清理、gzip 和平台文件所有权处理逻辑。

升级上游时核对这几处差异，并运行本包及 `aifd-log` 的关闭、失败重试、轮转与压缩测试。
