# 上游 v0.13.8 与 AxonHub 同步执行计划

这份文档供新会话执行，任务范围为吸收约定的上游改动、完成验证、建立新的本地 Git 基线并收尾。

## 1. 会话执行约定

新会话先读取并遵守用户的全局规则（`~/.claude/CLAUDE.md`）。仓库内没有 `AGENTS.md`，不要去找。

- 新会话直接执行本计划的全部阶段，负责集成、验收、建立基线和最终收尾。
- 本计划不指定子代理和模型档位。是否派发子代理由用户在执行时决定；未经点名时按全局规则处理。
- 各阶段串行执行，前一个阶段完成并验收后才能开始下一个。

## 2. 目标和完成边界

完成一次受控同步，目标基线为：

- Octopus 上游代码行为：bestruirui/octopus 的 9357a322，对应上游 v0.13.8；
- AxonHub：github.com/looplj/axonhub/llm v0.0.0-20260923093423-859c3e4ed944。

同步时保留当前 test 分支的阶段 2～5 实现和阶段 6 回退结果。完成上游同步、测试、建立新基线并报告后停止；`plan/versions` 下的规划及其实现由用户后续另行安排，本轮不修改或启动。

本轮不实现模型刷新、自动同步、生图、排序或 WebSocket；不改变数据库结构；不恢复 companion。

## 3. 当前基线事实

执行前必须重新读取文件和 Git 状态，不能假设以下内容仍未变化：

- 计划编写时分支为 test，HEAD 为 4aeef48；
- 当前产品代码基于上游 4a54b59（v0.13.6）并叠加本项目阶段提交；
- 当前 AxonHub 预计为 v0.0.0-20260909170523-3786f2c5c8de；
- web/src/components/modules/channel/Form.tsx 已由 a09c556 提供创建/更新失败 toast；
- main.go 的 Version v0.13.6 是上游发布标记，不应机械改为 v0.13.7 或 v0.13.8。

如果工作区已有未说明改动，先停止同步并向用户报告，不得覆盖或重置这些改动。

## 4. 上游改动取舍

### 4.1 吸收厂商预设修复 aa07f77ab8d12f691dd43baaa77cbe5766f4f012

手动更新 web/src/lib/channel-presets.tsx：

- Volcengine：根地址改为 https://ark.cn-beijing.volces.com，路径带 /api/v3；Anthropic 使用 /api/compatible/v1/messages；
- DeepSeek：Anthropic 使用 /anthropic/v1/messages；
- DashScope：根地址改为 https://dashscope.aliyuncs.com，路径带 /compatible-mode/v1 和 /apps/anthropic/v1；
- Moonshot：Anthropic 使用 /anthropic/v1/messages；
- 智谱：根地址改为 https://open.bigmodel.cn，使用 /api/paas/v4、/api/v1 和 /api/anthropic/v1；
- Azure OpenAI：Chat 和 Responses 使用 /openai/v1/...。

这些字段只影响新建渠道预填，不迁移已有渠道。

### 4.2 吸收 AxonHub 依赖升级 758b5825bd1c2a567c679cfdfcaba9252b963705

将 go.mod 和 go.sum 的 AxonHub 版本更新到目标版本。本次命令见 U2，影响包括依赖清单及 Transformer/Relay 的运行行为。

使用精确版本更新，先检查差异；除非构建明确要求，不执行会改动无关依赖的宽泛 go mod tidy。

### 4.3 手动移植思考等级和日志增强 ecb492342738579d68d237b3e5f4f007dc5d28a3

当前分支已经改过以下文件，不能直接覆盖：

- internal/relay/handler.go
- internal/relay/state.go
- web/src/api/log.ts
- web/src/components/modules/log/Item.tsx

按当前结构手动整合：

1. 入站请求解析成功后读取 ReasoningEffort；解析失败时保持空字符串，不改变转发流程；
2. 在 RequestState 和状态 JSON 中增加 reasoning_effort，保留现有重试、故障转移、取消和运行时字段；
3. 前端日志类型、卡片和详情展示同步增加思考等级；没有等级时不显示空标签；
4. 评估重试错误历史展示，只保留与当前状态流、筛选和 SSE 重连兼容的部分；
5. 日志布局调整限于该提交已有的展示改善，不顺带重构日志组件。

当前 AxonHub 已提供 llm.Request.ReasoningEffort，思考等级展示不应新增第三方库。

### 4.4 创建渠道错误 toast 只验证，不重复合并

上游 276ec893 只处理创建渠道接口失败。当前分支的 a09c556 已在创建和更新 mutation 上接入 onError，使用本地化 saveFailed 和后端错误详情。

验证：

- 创建接口失败时表单不关闭并显示 toast；
- 更新接口失败时现有提示仍正常；
- 渠道运行时调用错误不新增全局弹窗；
- 三份 locale 都有对应文案。

不要再次应用上游 Form.tsx 补丁。

### 4.5 明确跳过

- 0e1c3fc6844d15c5e36266811719ebe706cb84bb 和 9357a322ff1979bd2bf825ce8e2322e7144a8e1e 的版本号提交：不修改 main.go 的产品版本标记；
- AxonHub 中未被本轮明确使用的 UI、配额、端点自动识别、自动禁用、图片编辑等功能：只随依赖升级进入库，不主动接入；
- 远程推送、标签、PR、Release、分支切换和历史改写。

## 5. 执行阶段

### U0：只读预检和基线记录

执行：

    git status --short --branch
    git log --oneline --decorate -12
    git remote -v
    go list -m -f '{{.Path}} {{.Version}}' github.com/looplj/axonhub/llm
    rg -n "Version v|saveFailed|ReasoningEffort|reasoning_effort" main.go internal web/src

使用 tinyfish 查询上游提交和文件列表，固定本次对比范围为上述提交。记录起始 HEAD 和工作区状态；本计划文件若仍未跟踪，属于已知文档。确认原有改动归属和本轮文件范围后，运行：

    go test ./... -count=1
    go vet ./...
    pnpm --dir web lint
    pnpm --dir web build --outDir "$env:TEMP/octopus-upstream-prebuild" --emptyOutDir false
    git diff --check

如果基线检查失败，先记录现有问题，不能把失败归因于本轮同步。

### U1：厂商预设修复

单文件修改，改完即验收，再进入下一阶段。

检查预设生成的完整地址，至少覆盖 Volcengine、DashScope、智谱和 Azure；运行前端 lint 和构建。

### U2：AxonHub 依赖升级

执行精确版本升级，限定修改 go.mod、go.sum 以及编译所必需的最小范围。

    go get github.com/looplj/axonhub/llm@v0.0.0-20260923093423-859c3e4ed944
    go list -m -f '{{.Path}} {{.Version}}' github.com/looplj/axonhub/llm
    git diff -- go.mod go.sum
    go test ./... -count=1
    go vet ./...

如果出现 Transformer、Relay 或已有测试行为变化，先停止并分类报告；不要顺手修改产品逻辑掩盖依赖升级影响。

### U3：Relay 和日志手动移植

限定在四个 Relay/日志文件及必要测试文件内，逐项串行处理。必须保留当前阶段 2～5 的路由、重试、错误可见性和 SSE 行为。

至少覆盖：

- OpenAI Chat 的 reasoning_effort；
- OpenAI Responses 的 reasoning effort；
- Anthropic thinking 到统一等级的展示；
- 未提供等级和入站解析失败时的兼容行为；
- 重试中错误展示、成功后清除错误、取消状态和 SSE 重连。

### U4：创建渠道错误提示验收

检查当前实现，不新增代码。使用已有 ApiError 错误路径做浏览器手动验收，确认创建失败时 toast 出现且表单仍打开。无法启动完整服务时，至少完成 TypeScript、lint 和构建验证，并说明缺少浏览器验收。

### U5：同步后全量验收

    go test ./... -count=1
    go test -race ./internal/relay ./internal/server/handlers -count=1
    go vet ./...
    pnpm --dir web lint
    pnpm --dir web build --outDir "$env:TEMP/octopus-upstream-sync-build" --emptyOutDir false
    git diff --check
    git status --short --branch

同时确认：

- AxonHub 精确为 v0.0.0-20260923093423-859c3e4ed944；
- main.go 仍保留当前产品版本策略；
- 没有数据库迁移、备份格式或 API 枚举变化；
- 没有重复应用创建渠道 toast；
- 没有新增未授权依赖；
- 没有未说明的构建缓存或生成物。

### U6：建立新基线并收尾

验收通过后完成：

1. 新增 `plan/upstream-sync-v0.13.8-result.md`，记录起始 HEAD、实际吸收和跳过的提交、AxonHub 版本及依赖差异、验证结果和遗留问题。
2. 核对本轮实际修改文件，按明确的文件清单暂存同步代码、依赖文件、本计划和收尾记录。
3. 检查暂存差异后创建一次计划内的本地提交，作为本项目的新同步基线；提交说明使用中文。

   ```powershell
   git diff --cached --check
   git diff --cached --stat
   git diff --cached
   git commit -m "同步：吸收上游 v0.13.8 改动并建立新基线"
   git rev-parse HEAD
   git status --short --branch
   ```

4. 在最终报告中给出新基线的完整 commit SHA 和收尾记录路径，核对本轮改动均已提交，说明任何保留的原有改动。

新基线以本项目的本地提交为准，沿用当前产品版本标记。本轮任务至此结束。

## 6. 停止条件和最终报告

出现以下任一情况时停止当前阶段，不扩大修改：

- 工作区原有改动无法区分；
- AxonHub 升级需要修改大量无关产品逻辑；
- Relay 状态、重试、故障转移或 SSE 语义出现回归；
- 需要新增依赖、数据库迁移、远程操作或版本发布；
- 必要测试无法运行或连续失败且原因未分类。

最终报告必须包含：

- 实际吸收的上游提交和跳过的提交；
- AxonHub 当前版本和依赖差异；
- 修改文件、字段和接口变化；
- 是否需要迁移、备份兼容处理或回滚；
- 所有验证命令及结果；
- 新基线的分支、完整 commit SHA、工作区状态和收尾记录路径；
- 本轮遗留问题及其影响。

完成上游同步、测试、建立新基线并报告后停止，等待用户安排后续任务。
