# R1 执行范围与开工审计

日期：2026-10-07。当前分支：`test`。基线：`6fa07227ddcd928916ce98147785bcb0dffc8e5b`；开工时工作区干净，M1、M2、M3 均已提交。

依据：[R1 里程碑](versions/v1.0.0-independent-release.md)、[版本路线图](version-roadmap.md)、[备份格式设计](../docs/superpowers/specs/2026-09-26-backup-format-identity-design.md)。用户已明确授权本文件的代码、测试和文档修改，随后授权[加密实现](../docs/backup-encryption-design.md)及[停用上游更新](r1-release-preflight.md)。以下开工发现保留为审计历史；最新状态见 [r1-result.md](r1-result.md)。

## V100-01：备份身份与兼容审计

**产出定义**：导出携带 `product: "octopus-solo"`、`version: 1000`；导入先校验产品身份，只接受无标识的 `0`/`5` 和本产品的 `1000`。无标识 `6`、未来版本、外来或大小写/空白不符的标识均拒绝。旧配置默认值、新配置往返和重复导入有测试证据。

**材料位置**：`internal/model/{backup,channel,group,setting,webhook}.go`、`internal/op/backup.go`、`internal/db/migrate/`、`internal/server/handlers/setting.go`、备份格式设计。

**改动边界**：

- `internal/model/backup.go`：新增产品标识字段。
- `internal/op/backup.go`：替换常量和校验，导出写入标识；修复导入时 `enabled: false` 被 GORM 默认值改为 `true` 的问题，分别覆盖新增记录和已有记录冲突时的语义。
- 新增 `internal/op/backup_test.go`；复用 `internal/op/group_test.go` 的现有 `TestMain`，不按旧计划重复新增入口。
- `internal/op/channel_automation_test.go`、`internal/op/group_route_defaults_test.go`：更新新格式构造，旧备份场景使用明确的上游版本 `5`。
- `internal/webhook/service_test.go`：版本断言移入备份测试，保留 Webhook 配置持久化和往返检查。
- 必要时新增 `internal/server/handlers/backup_test.go` 验证裸 JSON、响应包裹、multipart、拒绝后无写入以及现有管理认证。若发现解码漏洞，先给出复现与局部修正再修改 `setting.go`。
- 新增 `plan/r1-migration-audit.md` 记录字段、默认值、迁移、导入语义和未满足项。
- 测试只使用隔离临时 SQLite；本步骤不需要新增编号迁移或修改业务数据库。

**必要检查**：先验证拒绝矩阵失败，再落地校验；验证序列化导出往返、旧 `0`/`5` 导入、重复导入、新配置保留、禁用渠道、零模型渠道、空分组、失效成员处理与事务回滚。执行 `go test ./internal/db/... ./internal/op ./internal/server/handlers ./internal/webhook -count=1`。

**命令和影响**：用 `apply_patch` 对上述文件做局部补丁，`gofmt -w` 格式化有变更的 Go 文件。备份格式由 `6` 改为 `1000`，无标识 `6` 导入将被拒绝，旧构建也不能导入新格式。已有数据库数据由新构建直接读取，不自动转换旧 JSON。回滚须保留升级前构建及其数据库副本。

## V100-02：全量回归和构建

**产出定义**：按里程碑核对 Relay、路由策略、模型刷新、分组同步、协议限制、日志筛选、SSE、自动更新、并发限制和 Webhook。测试成功与未实测项分别记录，不能用构建成功代替运行验收。

**材料位置**：`internal/` 现有测试、`web/`、`.github/workflows/{build,release}.yaml`、`scripts/build.sh`。

**改动边界**：生成临时构建产物和 `plan/r1-result.md`；发现范围外缺陷时记录复现和建议修复范围。

**必要检查与命令**：

```powershell
# 仅当前命令进程使用已有模块缓存和工作区构建缓存。
$env:GOCACHE = Join-Path (Get-Location) '.tmp-go-cache'
$env:GOPROXY = 'off'
go test ./... -count=1
go test -race ./internal/relay ./internal/op ./internal/task ./internal/server/handlers -count=1
go vet ./...
go build -trimpath -tags=jsoniter -o "$env:TEMP/octopus-r1.exe" .
pnpm --dir web lint
pnpm --dir web build --outDir "$env:TEMP/octopus-r1-build" --emptyOutDir false
git diff --check
```

**影响**：测试使用临时数据，前端输出到临时目录，构建不触发发布。完整安装/升级冒烟需要可运行的前端嵌入产物与隔离实例，须在结果中说明实际覆盖程度。

## V100-03：使用与升级文档

**产出定义**：README 标明 fork 来源、S1 上游同步基线和独立版本线；记录新设置默认值、匹配规则、空模型行为、自动更新影响、日志状态、排障和数据库回滚流程。

**材料位置**：`README.md`、`README_zh.md`、`internal/model/setting.go`、`internal/conf/`、各里程碑验收记录。

**改动边界**：局部修改两份 README、`plan/version-roadmap.md`、`plan/versions/v1.0.0-independent-release.md`；新增 `CHANGELOG.md`、`docs/upgrade-r1.md`、迁移审计与结果记录。文档中的配置示例按真实实现修订，不修改实际环境配置。

**必要检查**：对照源码核对默认值、命令、路径和兼容矩阵，检查文档链接与 `git diff --check`。

**命令和影响**：`apply_patch` 局部修改或新增文档。修正 R1 与路线图中要求无标识 `6` 导入成功的旧验收项，明确当前发布状态。

## V100-04：发布前准备与授权边界

只读检查已确认 `release.yaml` 仅监听 `main` 的 `main.go` 修改；连字符版本判为预发布，预发布不覆盖 Docker `latest`。三项原计划修复已在当前基线中，无需重复修改 CI。

批量代码和文档授权不包含以下操作：创建/切换产品分支、commit、推送、修改默认分支、修改 `main.go` 版本、标签和 Release、生产数据库备份或升级。需先备好最终差异、发布说明、测试结果以及明确的目标分支/版本/仓库，再逐项取得授权。

## 开工发现与待决事项

1. **备份敏感字段契约冲突**：`DBExportAll` 导出完整 `ChannelKeys`、`APIKeys`、`WebhookEndpoints`，其中包含明文凭据和认证头。导出接口有管理员认证，但没有文件加密；现有 Webhook 测试明确要求认证头可往返。R1 当前验收写“敏感字段不会因为备份导出泄露明文”，不能把现状记为通过。需要选择保留完整 JSON 并明确其保管要求，或另行设计用户口令加密备份；直接脱敏会破坏恢复能力。此决策不妨碍身份隔离的实现与回归。
2. **跨数据库验收**：当前 PATH 没有 `docker`、`mysql`、`psql`，未发现可用测试实例的配置证据。MySQL/PostgreSQL 迁移实测暂缺，不能沿用 SQLite 结果宣称通过；新增工具或建立外部数据库需另定范围。
3. **旧设计与现状差异**：`internal/op` 已有数据库夹具；M1/M3 测试新增了备份版本引用，必须同步更新。实现以当前代码为准。
4. **导入解码隐患**：`decodeDBDump` 仍使用表字段枚举判断响应包裹，遗漏 `ChannelKeys`、`ChannelGrants`，也尚未检查产品标识；需要在 handler 测试中验证混合元数据的处理，避免新身份校验被解码步骤改变。
5. **本地测试闸门**：现有 CI 构建流程不执行 Go 测试。身份版本测试会成为本地必跑项；本次准备范围不包含修改 CI。
6. **已复现：禁用状态丢失**：隔离临时 SQLite 导入无标识 `5` 号备份，备份中渠道和 Key 的 `enabled` 均为 `false`，读取数据库后均为 `true`。模型上的 `gorm:"default:true"` 使结构体零值在创建时被默认值替换。需修复备份写入路径，并验证新增禁用渠道/Key、已有渠道配置冲突策略、已有 Key 覆盖策略及其他带默认值的布尔字段。当前不能通过“隐藏/禁用渠道导入保持兼容”验收。

## 开工基线检查

| 检查 | 2026-10-07 结果 |
| --- | --- |
| `git status --short` | 开工时干净 |
| `go test ./... -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/relay ./internal/op ./internal/task ./internal/server/handlers -count=1` | 通过 |
| `pnpm --dir web lint` | 通过 |
| `pnpm --dir web build --outDir "$env:TEMP/octopus-r1-build" --emptyOutDir false` | 通过，含 TypeScript 检查；仅构建插件耗时提示 |
| `go build -trimpath -tags=jsoniter -o "$env:TEMP/octopus-r1.exe" .` | 通过，见下方缓存说明 |
| `git diff --check` | 通过；本次仅新增执行方案，另按文件内容检查 |

Go 发布构建首次受系统模块缓存缺少 `json-iterator` 且不可写影响，改用已有工作区模块缓存后完成：当前进程设置 `GOMODCACHE=<workspace>/.tmp-go-mod`、`GOPROXY=file:///D:/tool/cache/go-mod/cache/download`、`GOSUMDB=off`，只从本地系统缓存读取当前 `go.mod` 锁定的缺失模块，未联网或变更依赖。Go 构建使用当前 `static/out`，本次临时前端产物尚未嵌入；因此不将其记为完整安装/升级冒烟验收。

使用 `build/r1-audit-*/main.go` 中的一次性审计程序运行真实 `db.InitDB`、`DBImportIncremental` 和 JSON 序列化往返，数据库创建于临时目录并在退出时关闭、清理；仅包含合成凭据，未接触业务数据库。结果：

| 场景 | 实测结果 |
| --- | --- |
| 禁用渠道与禁用 Key 导入 | **失败**：两者均被启用 |
| 旧渠道缺少自动跟随字段 | `true`，符合兼容默认值 |
| 旧渠道缺少协议掩码 | `14`，允许全部现有协议 |
| 旧空分组缺少模式/匹配模式 | `manual` / `keyword` |
| 导出后 JSON 往返并重复导入 | 成功 |
| 分组成员引用不存在的授权 | 整次导入被拒绝，事务回滚，新分组未残留；未自动清理失效成员 |

状态：批量实现已获授权并执行；开工缺陷修复与最终验证见 [r1-result.md](r1-result.md)。发布条件尚未全部满足。
