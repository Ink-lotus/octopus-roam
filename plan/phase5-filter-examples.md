# 阶段 5 交接文档：过滤示例和交互优化

权威契约：[`v1.0plan.md`](v1.0plan.md)「阶段 5：过滤示例和交互优化」。

## 完成情况

- 渠道高级设置的 `match_regex` 和系统设置的 `model_filter` 共用正则编辑区，保留直接编辑原始表达式。
- 添加按前缀保留、按后缀保留、包含关键词、排除关键词、排除 embedding/rerank 模型五类示例；每条示例都能复制。
- 界面说明使用 ECMAScript 正则，渠道过滤和全局过滤按 AND 同时生效，留空表示不过滤。
- 输入不合法表达式时显示格式错误；渠道表单阻止提交，全局设置在失焦时跳过保存。
- 渠道配置在 trim 后由后端 `regexp2` ECMAScript 编译校验，防止空白导致客户端与持久化值不一致。
- 全局过滤服务端拒绝或渠道保存失败时，界面通过错误 toast 告知用户。
- 简体中文、繁体中文和英文文案已同步；已删除不再引用的 `modelFilter.hint`。

## 验收结果

- `pnpm lint`
- `pnpm build --outDir "$env:TEMP\octopus-phase5-build" --emptyOutDir false`（包含 TypeScript 检查）
- `go test ./... -count=1`，使用仓库内临时 `GOCACHE` 与 `GOMODCACHE` 后通过
- `go test ./internal/op -count=1`
- `go vet ./internal/op`
- Node `RegExp` 编译全部五条示例
- 三份 locale JSON 解析
- `git diff --check`

没有 API、数据结构、数据库迁移或依赖变化。Go 测试专用缓存保留在工作区 `.tmp-go-cache/` 和 `.tmp-go-mod/`，等待授权清理。
