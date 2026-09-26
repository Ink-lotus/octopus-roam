# 备份格式产品标识与版本命名空间分离

- 日期：2026-09-26
- 状态：设计已确认，待实现
- 归属：`R1` / **V100-01 数据迁移与备份兼容审计**（第六条实现内容，不另起任务）
- 风险等级：**L3**（schema 与持久化）→ 完整 red-green TDD
- 设计基线：分支 `test`，HEAD `1507b28c7ecee6e47618c294dab2df61960e5c28`

> 实现开工前必须用 `git rev-parse HEAD` 复核基线。`S1` 上游同步会替换基线；若 `S1` 已完成，以其建立的 commit 为准，并重读本文档「相关文件」节列出的全部文件确认结构未变。

## 1. 问题

`model.DBDump` 的 `version` 字段是一个**被两个独立演进的产品共享的单一整数命名空间**。上游 bestruirui/octopus 与本项目都在其中自增，而备份文件里没有任何字段能说明「这个数字属于谁」。

已核实的事实：

| 项 | 值 | 位置 |
| --- | --- | --- |
| 上游 v0.13.6 的版本号 | `dbDumpVersion = 5` | `git show 4a54b59:internal/op/backup.go:16` |
| 上游的导入校验 | 接受 `0` 和 `5` | 同上第 76 行 |
| **上游 v0.13.8 的版本号** | **仍是 `dbDumpVersion = 5`，校验仍接受 `{0,5}`，导出 13 张表无 `WebhookEndpoints`** | 2026-09-26 抓取 `raw.githubusercontent.com/bestruirui/octopus/v0.13.8/internal/op/backup.go` |
| 本项目当前版本号 | `dbDumpVersion = 6`、`legacyDBDumpVersion = 5` | `internal/op/backup.go:17-18` |
| 本项目的导入校验 | 接受 `0`、`5`、`6` | `internal/op/backup.go:82` |
| 升到 6 的提交 | `b8abecf`（阶段 4 通用 Webhook） | 导出新增 `WebhookEndpoints` 表 |

### 1.1 正向风险（静默导入）

`model.DBDump` 只有 `Version` 和 `ExportedAt` 两个元数据字段，14 张表全部带 `omitempty`，而 Go 的 JSON 解码默认忽略未知字段。因此上游将来把自己的 `dbDumpVersion` 升到 `6` 时：

- 两边的 6 号备份含义不同；
- 两者都能通过 `internal/op/backup.go:82` 的校验；
- 缺失的表解码成 nil，与「该表本来就空」无法区分；
- **整个过程不报任何错**，静默导入语义不同的数据。

### 1.2 反向风险（上游静默丢数据）

本项目当前导出的 6 号文件，今天会被上游导入器拒绝（它只接受 `0`/`5`）。但上游一旦自行升到 `6`，它就会**静默接受**我方文件并丢弃 `webhook_endpoints` 等它不认识的表。

这条风险决定了本项目 `dbDumpVersion` 的取值不只是内部记账：选一个上游永远到不了的值，能让保护变成**双向**的。

### 1.3 次生风险（同步污染）

上游同步时若带入上游的 `dbDumpVersion` 常量或校验白名单，会改变本项目的判别语义。现有防护分三层：

| 层 | 状态 |
| --- | --- |
| 人工检查点 | **已有**。`plan/upstream-sync-v0.13.8.md:167` 的确认项含「没有数据库迁移、备份格式或 API 枚举变化」，第 200 行把相关情形列为立即停下条件，第 208 行强制报告备份兼容性 |
| git 合并冲突 | 我方常量与上游取值不同，上游若改动该区域会产生**可见冲突**，不是静默合并 |
| 自动闸门 | **缺**，由本设计的 §7.2 补上 |

**`S1` 的具体风险已核实为零。** 上游 v0.13.8 的 `internal/op/backup.go` 仍是单行 `const dbDumpVersion = 5`，校验仍是 `dump.Version != 0 && dump.Version != dbDumpVersion`，导出 13 张表、无 `WebhookEndpoints`——与 fork 基线 v0.13.6 在版本逻辑上一字未改。上游侧无改动即无冲突即无污染。

该结论**仅对 `S1` 成立**，前提是它按 `plan/upstream-sync-v0.13.8.md` 钉死的 v0.13.8 执行；若执行时改同步更晚的上游版本，必须重新核实该文件。

`S2` 及以后的同步都发生在 `R1` 之后，届时 §7.2 的自动闸门已在位。因此**不需要为 §7.2 提前单独落地**，它随本设计一起进 `R1` 即可。

附带好处：实现本设计后，我方该区域是 const 块 + 两个 map 变量，与上游的单行常量结构差异显著，未来同步时更容易触发可见冲突而非静默合并。

## 2. 成功标准

1. 本项目导出的备份，将来能被本项目**确定性地**识别为自己的；
2. 任何**语义不可判定**的备份都要报错，而不是静默导入；
3. `S1` 同步时若有人带入上游常量或往白名单加上游新版本，测试必须红。

要拒绝的是**歧义**（撞号的未来上游版本），不是上游本身：已知形态的上游备份（`0`、`5`）继续照常导入。

## 3. 已确认的前提

**存量无标识 6 号备份可放弃。** 用户已确认此类文件只存在于其本人的测试环境，不需要保证可导入。这消除了整个「内容启发式判定」分支。

## 4. 方案：产品标识字段 + 版本号跳段

两者都做。

### 4.1 数据结构

`internal/model/backup.go`：

```go
type DBDump struct {
	Product    string    `json:"product"` // 产品标识; 导出恒为 octopus-solo, 缺失即视为上游或遗留格式。
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	...
}
```

`Product` **不带 `omitempty`**——始终可见地写出来是它作为标识的全部意义。

### 4.2 常量

`internal/op/backup.go`。私有即可，没有其他包需要它们：

```go
const (
	dumpProduct   = "octopus-solo"
	dbDumpVersion = 1000 // 刻意跳段以脱离上游命名空间。
)

// upstreamDumpVersions 是无产品标识文件的接受白名单: fork 点 (上游 v0.13.6) 及之前上游用过、
// 且本项目已逐表审计过语义的版本。上游此后自增的版本语义未知, 一律拒绝。
// S1 同步警示: 不要带入上游的 dbDumpVersion, 也不要往这个白名单加上游新版本。
var upstreamDumpVersions = map[int]struct{}{0: {}, 5: {}}
var soloDumpVersions = map[int]struct{}{dbDumpVersion: {}}
```

现有的 `legacyDBDumpVersion` 常量删除。它的名字暗示「上一代」，而真实语义是「上游已审计版本白名单」，由 `upstreamDumpVersions` 取代。

`soloDumpVersions` 目前只有一个元素，用 `== dbDumpVersion` 也够。选 map 是为了与 `upstreamDumpVersions` 形式对称——两个白名单长得一样，读的人不用分别理解；升到 1001 时也只改一处。这是可维护性取舍，不是必需。

跳段到 `1000` 的作用是解决 §1.2：`1000` 不在上游的 `{0,5}` 里，因此上游导入器会**明确拒绝**我方文件，即使它将来升到 6、7、8。只加 `product` 字段做不到这点——上游不认识 `product`，会直接忽略它。

### 4.3 判别函数

抽一个纯函数，`DBImportIncremental` 开头调用，替换现在的 `internal/op/backup.go:78-84`：

```go
func validateDumpOrigin(dump *model.DBDump) error
```

含 `nil` 检查。抽纯函数的理由是测试成本：判别逻辑不碰数据库，测它就不该需要 DB 夹具（这正是现有版本断言寄居在 `internal/webhook/service_test.go` 的根源）。

四个互斥分支，全部确定性 accept/reject，**无静默路径**：

| 输入 | 判定 |
| --- | --- |
| `nil` | reject |
| `Product == "octopus-solo"` | version ∈ `soloDumpVersions` → accept，否则 reject |
| `Product == ""` | version ∈ `upstreamDumpVersions` → accept，否则 reject |
| 其它 `Product` | reject |

**优先级：标识 > 版本。** 显式的外来标识直接否，即使其版本号落在上游白名单内。

**`Product` 用精确相等比较，不做 trim、不忽略大小写。** 标识是机器写入的字段而非用户输入，宽松匹配只会扩大歧义面。退化行为是安全的：`" octopus-solo "` 或 `"Octopus-Solo"` 既不等于 `""` 也不等于 `dumpProduct`，落入「外来 product」分支被拒，而不是被误认。

### 4.4 导出端

`DBExportAll` 填 `Product: dumpProduct`，`Version: dbDumpVersion`。其余导出逻辑不变。

### 4.5 与 `decodeDBDump` 的兼容（已核实无冲突）

`internal/server/handlers/setting.go:183-204` 有一个包裹回退：当 `Version == 0` 且枚举到的 12 张表全空时，尝试把文件当成被 `{code,message,data}` 包裹的 API 响应重新解一遍。（`DBDump` 共 14 个表字段，该枚举漏了 `ChannelKeys` 和 `ChannelGrants`——见 §12.2。）

三种情形都走对：

- 包裹的上游 5 号 → 外层解出 `Version 0`、表全空 → 触发回退重解 → 命中上游白名单，accept；
- 包裹的我方 1000 号 → 同上，正确重解后 accept；
- 全空的**新装**我方备份（`version 1000`）→ 不满足 `Version == 0`，不触发回退，直接 accept。

因此 `setting.go` 本次**零改动**。该函数里那段 12 路 `len(...) == 0` 枚举是已知的维护隐患（每次新增表都要同步加一行），但不在本次范围。

### 4.6 handler 与前端

- `internal/server/handlers/setting.go` 已把 `DBImportIncremental` 的 error 映射为 400（第 158-162 行），**不改**。
- 前端零改动：`web/src/api/setting.ts:65` 与 `web/src/components/modules/setting/Backup.tsx:18` 只读 `rows_affected`，不碰 `version`/`exported_at`。新增 `product` 对前端透明。
- locale 零改动：`web/src/locales/*.json` 只有通用的 `import.failed`，后端错误原文透传给用户，与今天 `unsupported dump version: %d` 的行为一致。

## 5. 错误语义

四条错误全部由 `validateDumpOrigin` 返回、`DBImportIncremental` 透出。文案用英文，与 `backup.go` 现有错误风格一致。

| 分支 | 文案 |
| --- | --- |
| 无标识 + `6` | `unsupported backup: version 6 without product marker is a pre-release octopus-solo format and is no longer accepted; re-export from a current build` |
| 无标识 + 其它非白名单 | `unsupported backup: version %d has no product marker (accepted upstream versions: 0, 5)` |
| 外来 product | `unsupported backup: product %q is not octopus-solo` |
| 我方 product + 版本越界 | `unsupported octopus-solo backup version %d` |

6 号单独一条文案，成本是一个 `if`，收益是把「为什么这个文件突然导不进去了」说清楚，省掉一次翻代码。

## 6. 刻意不做（YAGNI）

- **不做表清单（manifest）。** 「缺失表解码成 nil 与本来就空无法区分」的根因是版本号不可信。版本号一旦可信，「这一代格式该有哪些表」就由版本号完全确定，manifest 是冗余的，而且会变成第二处需要同步维护的真相。
- **不做内容启发式。** 存量已授权放弃（§3）；且 `WebhookEndpoints` 带 `omitempty`，没配 webhook 的用户（常态）导出时该键根本不出现，「含 `webhook_endpoints` ⇒ 本项目」这条召回率本就很低。
- **不用 `DisallowUnknownFields` 当判别手段。** 上游升到 5 号靠的是 `api_keys.supported_models` 的字段语义变化而非新增表，说明「出现未知表」不是可靠信号；而严格解码会让手改过的备份直接失败（`backup.go:86-87` 的注释明确预期用户会手改备份），代价大于收益。

## 7. 测试矩阵（red-green）

**TDD 判定：L3（schema 与持久化），走完整 red-green。**

新建 `internal/op/backup_test.go`。同包内已有 `channel_test.go`，且它不需要 DB 夹具——纯判别测试可照此模式，无需 `TestMain`。

### 7.1 第一批红：纯判别（无 DB）

| 输入 | 期望 | 这条测什么 |
| --- | --- | --- |
| `{solo, 1000}` | accept | 基线 |
| `{solo, 6}` | reject | 带标识的文件必须携带我方合法版本 |
| `{solo, 0}` | reject | 同上；`0` 尤其要拒 |
| `{solo, 1001}` | reject | 未来我方版本不得提前放行 |
| `{"", 0}` | accept | 上游白名单 |
| `{"", 5}` | accept | 上游白名单 |
| `{"", 6}` | reject，文案含重新导出引导 | §3 放弃存量的那条 |
| `{"", 7}` | reject | 未来上游 |
| **`{"", 1000}`** | reject | **标识是必要条件而非装饰**——手改掉 `product` 的我方文件不能靠版本号蒙过去 |
| **`{"octopus", 5}`** | reject | **优先级定义：标识 > 版本** |
| `nil` | reject | — |

### 7.2 第二批红：常量钉死（即 `S1` 防护）

`TestDumpConstantsAreIndependentOfUpstream`：

- `dbDumpVersion == 1000`
- `dumpProduct == "octopus-solo"`
- `upstreamDumpVersions` **恰好**是 `{0, 5}`——断言长度 + 成员，拦住往里加 `6` 的手

测试里必须写注释说明它存在的理由，否则将来有人会觉得这是废话测试顺手删掉。它是 `S1` 唯一的自动防线。

### 7.3 第三批红：导出端（需 DB 夹具）

- 导出结果带 `Product == "octopus-solo"`、`Version == 1000`；
- **序列化往返**：`DBExportAll` → `json.Marshal` → `json.Unmarshal` → `validateDumpOrigin` 通过 → `DBImportIncremental` 成功。

往返能抓到 json tag 拼错，同时直接覆盖 V100-01 验收的「新版本导出的备份可以再次导入」。

需 DB 的测试要求 `internal/op` 包有 DB 夹具，而该包目前没有——`channel_test.go` 是纯测试，无 `TestMain`。因此**新建 `internal/op/main_test.go`**，照 `internal/webhook/service_test.go:20-41` 的模式：临时目录 + `db.InitDB("sqlite", ...)` + `op.InitCache()`，`m.Run()` 后 `db.Close()` 并清理目录。

副作用：`channel_test.go` 的纯测试此后也在 DB 已初始化的环境下运行。它们不读 DB，行为不变。

### 7.4 现有断言的迁移

`internal/webhook/service_test.go:154-176` 的三组断言（接受 `0,5`、拒绝 `4,7`、导出版本 `== 6`）会被本次改动全部推翻，必须改。趁改的时候**搬到 `internal/op/backup_test.go`**——它们从一开始就不该在 webhook 包里。

`internal/webhook/service_test.go` 保留「导出含 `webhook_endpoints` 且 headers 完整」这一条（第 171-173 行），那才是它自己的关注点。测试函数名 `TestWebhookEndpointHeadersPersistAndOldBackupsRemainImportable` 需相应改名。

### 7.5 红绿顺序

1. 写 §7.1 + §7.2 测试 → 编译失败即红
2. 加 `Product` 字段、常量、`validateDumpOrigin`，改 `DBImportIncremental` 调用点 → 绿
3. 写 §7.3 导出端测试 → 红
4. `DBExportAll` 写 `Product`、版本改 `1000` → 绿
5. 迁移 §7.4 的 webhook 断言 → 绿
6. 全量验证

## 8. 验证

```
go vet ./...
go test ./internal/op/... ./internal/webhook/...
go build -trimpath -tags=jsoniter .
```

最后一条与 CI（`.github/workflows/build.yaml:50`）一致。

**风险：CI 不跑 `go test`。** `build.yaml` 只有 `go build`，仓库根目录没有 golangci 配置。因此 §7.2 的常量钉死测试是**本地闸门而非 CI 闸门**。后续上游同步（`S2` 及以后，均在本设计落地之后）若由子代理执行，派发时必须在「必要检查」里显式写明 `go test ./internal/op/...`，否则这条防线不会自动触发。

把 `go test ./...` 加进 `build.yaml` 能根治，但那是修改 CI，属需单独授权的操作，不在本设计范围内。

## 9. 文档同步

### 9.1 `plan/versions/v1.0.0-independent-release.md` — V100-01

**实现内容**新增第六条：

> - 备份格式加产品标识字段并把版本号迁出上游命名空间，判别逻辑对不可判定的文件确定性报错（见 `docs/superpowers/specs/2026-09-26-backup-format-identity-design.md`）。

**验收**第一条改写为：

> - 上游 `0`/`5` 号备份导入成功；无标识 6 号（阶段 4/5 预发布格式）被确定性拒绝并给出重新导出引导。

原文「从上游当前版本、**阶段 4/5 备份**和 `v0.x` 备份导入成功」与本设计直接矛盾——阶段 4/5 备份就是无标识 6 号，本设计明确拒绝它。不改写会让 R1 验收卡在一条刚刚有意废弃的要求上。

### 9.2 `plan/version-roadmap.md`

三处：

| 位置 | 改动 |
| --- | --- |
| 第 52 行 | 备份格式当前值 `dbDumpVersion = 6` → `1000`（实现完成时改） |
| 第 57 行 | 「这一撞号的修法属高风险改动，在 `R1` 的迁移审计中单独设计」→ 指向本文档 |
| 第 79 行 | `v0.1.0` 判定条件里「上游版本与阶段 4/5 备份导入成功」→ 同 §9.1 的改写措辞 |

## 10. 落地时机

- **命中强制升 minor 判据 #1**（`dbDumpVersion` 递增——跳段也是递增）。不得作为 patch 发布，进 `R1` 或之后的能力包。
- **不触发判据 #2**。这是备份**文件格式**的改动，不碰数据库结构，`internal/db/migrate/` 零新增。归类 L3 是因为它是持久化格式契约，不是因为要动迁移。
- **与 `S1` 的时序**：实现排在 `S1` 之后（路线图执行顺序 `S1` → `M1` → `M2` → `M3` → `R1`）。设计本身提前完成是有价值的——`S1` 的报告要求回答「是否需要迁移、备份兼容处理或回滚」（`plan/upstream-sync-v0.13.8.md:208`），有本文档在手，执行者撞上上游改备份格式时知道走哪条路，而不必临场发明方案。`S1` 期间的污染风险见 §1.3，已核实为零。

## 11. 相关文件

| 文件 | 改动 |
| --- | --- |
| `internal/model/backup.go` | 新增 `Product` 字段 |
| `internal/op/backup.go` | 常量、`validateDumpOrigin`、`DBExportAll`、`DBImportIncremental` 调用点 |
| `internal/op/backup_test.go` | 新建 |
| `internal/op/main_test.go` | 新建（`TestMain` + sqlite 夹具，§7.3） |
| `internal/webhook/service_test.go` | 迁出版本断言、改函数名 |
| `plan/versions/v1.0.0-independent-release.md` | V100-01 实现内容 + 验收 |
| `plan/version-roadmap.md` | 三处（§9.2） |
| `internal/server/handlers/setting.go` | **零改动**（已核实，§4.5 / §4.6） |
| `internal/db/migrate/` | **零改动**（§10） |
| `web/` | **零改动**（§4.6） |

## 12. 遗留风险

1. **CI 不跑测试**（§8）——常量钉死防线依赖人或派发提示。
2. **`decodeDBDump` 的 12 路表枚举**（`setting.go:183-195`）——每次新增表都要同步加一行，漏加会让包裹格式的回退判断失准。本次不动，建议在 V100-01 的其他条目里单独记一笔。
3. **无标识 6 号被拒后无自动迁移路径**——用户只能用旧构建重新导出。已授权放弃（§3），仅靠错误文案引导。
