# Multipart Gateway 015

Go1.27.1、Chi5.2.1的HTTP服务骨架。提供forms接口类型、默认限制、Content-Type基础校验、路由及示例入口；核心Multipart解析尚未实现。

默认限制为请求体4MiB、64个part、文本字段1MiB、单文件3MiB。POST /v1/forms/{form}解析并保存最新结果，GET /v1/forms/{form}/latest读取，GET /healthz检查服务。

运行go test ./...检查基础接口。服务入口go run ./cmd/server，ADDR默认:18115。示例入口go run ./cmd/demo，核心解析实现前返回not implemented。
