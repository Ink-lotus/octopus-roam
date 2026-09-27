# 上游 v0.13.8 与 AxonHub 同步结果记录

本轮按 plan/upstream-sync-v0.13.8.md 执行，采用 SDD（每阶段独立子代理实现 + 任务审阅）在 test 分支就地完成，未建 worktree。

## 起始与基线
- 起始 HEAD：438c0c6（分支 test，工作区干净）
- 基线预检（U0）：go test / go vet / pnpm lint / pnpm build / git diff --check 全绿，基线健康
- 上游目标：bestruirui/octopus 9357a322（v0.13.8）；AxonHub 目标 v0.0.0-20260923093423-859c3e4ed944

## 实际吸收的上游提交
- aa07f77（厂商预设修复）→ U1，commit 597b9a9：web/src/lib/channel-presets.tsx 6 个厂商地址，与上游 diff 逐字一致。附带发现 DeepSeek 的 OpenAI Chat/Responses 随上游去 /v1（超计划 §4.1 摘要，忠实上游）。
- 758b5825（AxonHub 依赖升级）→ U2，commit c3c8710：go.mod/go.sum 精确升级至 …859c3e4ed944，无间接依赖变化，全量测试绿、Transformer/Relay 无行为变化。
- ecb49234（思考等级 + 日志增强）→ U3，commit a208910：reasoning_effort 主线——handler.go 入站读取（失败留空不改转发）、state.go 加 RequestState.ReasoningEffort + json、log.ts + Item.tsx 展示（空不显示）。后端 red-green（新增 internal/relay/reasoning_test.go，三协议归一 + 空值兼容）。

## 部分吸收 / 明确排除
- ecb49234 的**重试错误历史改造**（errorRounds/displayError/visibleError/删 requestFailed）：与本分支阶段 2-5 的 rounds/telemetry/requestFailed 体系冲突，按计划 §4.3.4「只保留兼容部分」**未引入**。
- ecb49234 的 **LogMetrics 布局重排**：按计划 §4.3.5「不顺带重构日志组件」**排除**。
- 276ec893（创建渠道错误 toast）：本项目 a09c556 已在创建+更新 mutation 实现，**只验证不重复合并**（U4 四验收项全过）。
- 0e1c3fc6、9357a322（版本号提交）：跳过，main.go 保留 // Version v0.13.6。

## AxonHub 依赖差异
- github.com/looplj/axonhub/llm：v0.0.0-20260909170523-3786f2c5c8de → v0.0.0-20260923093423-859c3e4ed944
- diff 仅 go.mod 1 行 + go.sum 2 行；新旧版本 /go.mod 哈希相同，无依赖图传播；未跑 go mod tidy（go.sum 保留旧版本 2 行残留，无害）。

## 验证结果（U5 全量验收，HEAD a208910）
- go test ./... 全绿（relay 34.4s、handlers/model/op/webhook ok）
- go test -race ./internal/relay ./internal/server/handlers 无竞争（CGO=1 + mingw64）
- go vet 干净、pnpm lint / build 通过、git diff --check 干净、git status 干净
- 六项确认全相符：AxonHub 版本、main.go v0.13.6、无 DB/备份/API 枚举变化、无重复 toast、无未授权依赖、无游离产物

## 遗留问题 / 待决
- 未做浏览器端到端 toast 验收（U4）：需完整起后端+DB+前端，按计划 §4.4 降级为静态+代码核查。
- U3 端到端仅覆盖 OpenAI Chat；Responses/Anthropic 在 transformer 层验证（brief「至少覆盖」已满足，处理器读取与协议无关）。
- go.sum 保留 AxonHub 旧版本 2 行（未 tidy，无害）；如需洁净需单独 tidy（可能触无关依赖）。
- 上游错误历史改造 + LogMetrics 布局未吸收；如需对齐属后续独立工作（要处理与阶段 2-5 的整合）。
- 集成动作（合并/推送/删分支）未执行，留待持有者决定。

## 新基线
- 分阶段提交：597b9a9(U1) → c3c8710(U2) → a208910(U3)；本收尾记录提交为最终基线提交。
- Windows race 验收需将 mingw64（D:\tool\mingw64\bin）前置到 PATH（cgo 依赖）。
