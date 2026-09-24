# Multipart Gateway 015

Go1.27.1、Chi5.2.1 的 HTTP 服务，提供严格的流式 multipart/form-data 解析。

- POST /v1/forms/{form}：流式解析字段与文件，按出现顺序生成摘要。文件只保留元信息（name、filename、size）和完整字节的 SHA-256，不落盘、不写固定临时目录。
- 支持引号/转义参数、boundary 跨任意 chunk、重复字段、同名字段与文件混排、UTF-8 文本。
- 限制：请求体 4MiB、64 个 part、文本字段 1MiB、单文件 3MiB；超限返回 413，不保留部分结果。
- 解析失败（提前 EOF、错误 boundary、非法 Content-Type、重复终止符、路径穿越/NUL 文件名、非 UTF-8 字段等）返回稳定 JSON 错误和 4xx，不发布半成品；重新上传失败时保留该 form 上次成功结果。
- 客户端取消时立即停止读取和摘要计算；成功结果一次性原子发布，并发读取不会看到新旧字段混合。
- GET /v1/forms/{form}/latest 读取最新成功结果，GET /healthz 健康检查。

检查与运行：

- \u0060go test ./...\u0060、\u0060go vet ./...\u0060、\u0060go build ./...\u0060
- 服务：\u0060go run ./cmd/server\u0060（ADDR 默认 :18115）
- 示例：\u0060go run ./cmd/demo\u0060
