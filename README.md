# Schema Registry 015

这是一个本地多租户JSON Schema注册服务骨架。初始代码提供规范化JSON、SHA-256指纹、版本注册、租户隔离、Chi HTTP路由和demo；完整兼容性、批量原子性和快照恢复由本题实现。

初始检查：`go test ./...`。服务入口：`go run ./cmd/server`，默认监听`http://127.0.0.1:18116`。