# 备份格式产品标识与版本命名空间分离 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给备份文件加产品标识字段并把 `dbDumpVersion` 迁出上游命名空间，使导入对语义不可判定的文件确定性报错而非静默接受。

**Architecture:** `model.DBDump` 新增不带 `omitempty` 的 `Product` 字段，导出恒写 `"octopus-solo"`；`dbDumpVersion` 从 `6` 跳到 `1000`（上游永远到不了的段，顺带让上游导入器明确拒绝我方文件）。导入侧把现有的行内版本校验抽成纯函数 `validateDumpOrigin`，按「标识 > 版本」四分支判别：带我方标识查我方白名单，无标识查上游白名单 `{0,5}`，外来标识直接拒。

**Tech Stack:** Go（版本见 `go.mod`）、gorm、`encoding/json`；测试用 `db.InitDB("sqlite", ...)` 起临时库。

**Spec:** `docs/superpowers/specs/2026-09-26-backup-format-identity-design.md`

## Global Constraints

- `dumpProduct = "octopus-solo"`，`dbDumpVersion = 1000`，`upstreamDumpVersions = {0, 5}`，`soloDumpVersions = {1000}`。
- `Product` 字段 tag 为 `json:"product"`，**不带 `omitempty`**。
- `Product` 用精确相等比较：不 trim、不忽略大小写。
- 判别优先级：**标识 > 版本**。外来标识直接拒，即使版本落在上游白名单内。
- 删除 `legacyDBDumpVersion` 常量，由 `upstreamDumpVersions` 取代。
- 错误文案用英文，逐字取自 spec §5：
  - 无标识 + `6`：`unsupported backup: version 6 without product marker is a pre-release octopus-solo format and is no longer accepted; re-export from a current build`
  - 无标识 + 其它非白名单：`unsupported backup: version %d has no product marker (accepted upstream versions: 0, 5)`
  - 外来 product：`unsupported backup: product %q is not octopus-solo`
  - 我方 product + 版本越界：`unsupported octopus-solo backup version %d`
- **零改动**：`internal/server/handlers/setting.go`、`internal/db/migrate/`、`web/`。（Task 2 会在 `internal/server/handlers/` **新增测试文件**——那不是对 `setting.go` 的改动，spec §4.6 的「零改动」指非测试源码。）
- 风险等级 **L3**（schema 与持久化）→ 每个涉及行为的任务走完整 red-green。
- **刻意不做**（spec §6，实现时不要"顺手加上"）：不加表清单/manifest；不加任何内容启发式判定（例如「含 `webhook_endpoints` ⇒ 本项目」）；不给导入解码启用 `DisallowUnknownFields`。
- **不动 `main.go` 的版本注释。** 本改动命中强制升 minor 判据 #1，但改 `main.go` 的版本注释本身就是一次对外发布操作（`release.yaml` 会自动打标签、推远程、建 Release、覆盖 Docker `latest`），必须单独授权，不属本计划范围。
- 全量验证：`go vet ./...`、`go test ./internal/op/... ./internal/webhook/... ./internal/server/handlers/...`、`go build -trimpath -tags=jsoniter .`

## Review Focus

spec 声明了行为但没有任务的测试覆盖到的五类输入，按「最可能咬到人」排序。每条都已落到 Task 2 的步骤里。

1. **包裹格式（`{code,message,data}`）的我方 1000 号备份。** spec §4.5 只做了分析论证、零测试，而用户直接保存 API 响应当备份是真实路径；结论若错，导入会报「无标识 + 1000」而不是正常导入。
2. **`"product": null`。** JSON null 解码成 `""`，落入无标识分支被拒。行为正确但无测试钉住，将来有人给字段加指针类型就会静默改变。
3. **`product` 带空白或大小写差异**（`" octopus-solo "`、`"Octopus-Solo"`）。spec §4.3 明文规定精确匹配，但矩阵里没有这两条；若实现者顺手加了 `strings.TrimSpace` 或 `EqualFold`，判别面就被放宽了。
4. **导出 JSON 的原始字节里确实含 `"product":"octopus-solo"`。** 结构体往返能间接抓到 json tag 写错，但断言原始字节能直接定位，也拦住将来有人给字段加 `omitempty`。
5. **判别失败时数据库零写入。** spec §4.3 要求在 `DBImportIncremental` 开头调用，隐含「拒绝的导入不得留下任何行」；调用点放错位置会让外来备份部分落库。

---

### Task 1: 把备份版本断言迁到 `internal/op`

现有断言寄居在 `internal/webhook/service_test.go`，而它们测的是 `internal/op` 的代码。先做纯迁移、保持现有行为断言不变全绿，Task 2 的红才是无歧义的（只有新契约在变）。这一步同时建立 `op` 包的 DB 夹具，Task 2 依赖它。

**Files:**
- Create: `internal/op/main_test.go`
- Create: `internal/op/backup_test.go`
- Modify: `internal/webhook/service_test.go:126-177`

**Interfaces:**
- Consumes: `db.InitDB(dbType, dsn string, debug bool) error`、`db.Close() error`、`op.InitCache() error`（同包内直接 `InitCache()`）
- Produces: `internal/op` 包的 `TestMain` 夹具（临时目录 sqlite + 缓存初始化），Task 2 的 DB 相关测试直接用

- [ ] **Step 1: 建 `internal/op/main_test.go` 的 `TestMain(m *testing.M)`**

照 `internal/webhook/service_test.go:20-41` 复制：临时目录 → `db.InitDB("sqlite", filepath.Join(dir, "octopus.db"), false)` → `InitCache()` → `m.Run()` → `db.Close()` + 清理目录，每级失败各自回滚已做的部分再 `os.Exit(1)`。改三处：临时目录前缀用 `octopus-op-test`，错误文案里的 `webhook` 改 `op`，`op.InitCache()` 改成同包调用 `InitCache()`。

- [ ] **Step 2: 在 `internal/op/backup_test.go` 写迁移后的断言**

断言的是**当前**行为，此时应当全绿：

```go
func TestBackupVersionGate(t *testing.T) {
	for _, version := range []int{0, 5, 6} {
		if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: version}); err != nil {
			t.Fatalf("import backup version %d: %v", version, err)
		}
	}
	for _, version := range []int{4, 7} {
		if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: version}); err == nil {
			t.Fatalf("unsupported backup version %d was accepted", version)
		}
	}
	dump, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatalf("export database: %v", err)
	}
	if dump.Version != 6 {
		t.Fatalf("backup version = %d, want 6", dump.Version)
	}
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatalf("reimport exported backup: %v", err)
	}
}
```

- [ ] **Step 3: 运行，确认全绿**

Run: `go test ./internal/op/... -run TestBackupVersionGate -v`
Expected: PASS（夹具能起、现有行为未变）

- [ ] **Step 4: 从 `internal/webhook/service_test.go` 删掉已迁移的断言**

删除第 154-170 行（两个 version 循环、`DBExportAll` 后的 `dump.Version != 6` 检查）和第 174-176 行（重导入）。保留第 171-173 行的 `dump.WebhookEndpoints` 断言——那是 webhook 自己的关注点，它仍需要前面的 `DBExportAll` 调用。函数改名 `TestWebhookEndpointHeadersPersistAndOldBackupsRemainImportable` → `TestWebhookEndpointHeadersPersist`。

- [ ] **Step 5: 运行两个包，确认全绿**

Run: `go test ./internal/op/... ./internal/webhook/...`
Expected: PASS，且 webhook 包无「declared and not used」类编译错误（删断言后可能留下未用的导入）

- [ ] **Step 6: Commit**

```bash
git add internal/op/main_test.go internal/op/backup_test.go internal/webhook/service_test.go
git commit -m "test: 备份版本断言迁至 internal/op 包"
```

---

### Task 2: 产品标识与版本命名空间分离

**Files:**
- Modify: `internal/model/backup.go:7-9`
- Modify: `internal/op/backup.go:14-19`（常量）、`:25-28`（导出戳记）、`:78-84`（导入判别）
- Modify: `internal/op/backup_test.go`（Task 1 建的文件，此处重写为新契约）
- Create: `internal/server/handlers/setting_test.go`

**Interfaces:**
- Consumes: Task 1 的 `internal/op` `TestMain` 夹具；`internal/server/handlers` 已有的 `TestMain`（`group_route_test.go:25`）
- Produces:
  - `model.DBDump.Product string` — tag `json:"product"`，无 `omitempty`
  - `op.dumpProduct = "octopus-solo"`（`const`）、`op.dbDumpVersion = 1000`（`const`）
  - `op.upstreamDumpVersions`、`op.soloDumpVersions` — 均为 `map[int]struct{}`
  - `op.validateDumpOrigin(dump *model.DBDump) error` — 含 `nil` 检查

- [ ] **Step 1: 把 `internal/op/backup_test.go` 重写为新契约的失败测试**

替换 `TestBackupVersionGate`，新增五个测试。矩阵覆盖 spec §7.1 全部条目，外加 Review Focus #3 的两条：

```go
func TestValidateDumpOrigin(t *testing.T) {
	cases := []struct {
		name    string
		product string
		version int
		wantErr bool
	}{
		{"solo current", "octopus-solo", 1000, false},
		{"solo prerelease 6", "octopus-solo", 6, true},
		{"solo zero version", "octopus-solo", 0, true},
		{"solo future version", "octopus-solo", 1001, true},
		{"unmarked legacy zero", "", 0, false},
		{"unmarked upstream 5", "", 5, false},
		{"unmarked prerelease 6", "", 6, true},
		{"unmarked future upstream 7", "", 7, true},
		{"unmarked solo version", "", 1000, true},
		{"foreign product upstream version", "octopus", 5, true},
		{"foreign product solo version", "some-fork", 1000, true},
		{"product with surrounding space", " octopus-solo ", 1000, true},
		{"product with different case", "Octopus-Solo", 1000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateDumpOrigin(&model.DBDump{Product: c.product, Version: c.version})
			if (err != nil) != c.wantErr {
				t.Fatalf("validateDumpOrigin(%q, %d) error = %v, wantErr = %v", c.product, c.version, err, c.wantErr)
			}
		})
	}
}

func TestValidateDumpOriginRejectsNilDump(t *testing.T) {
	if err := validateDumpOrigin(nil); err == nil {
		t.Fatal("nil dump was accepted")
	}
}

func TestValidateDumpOriginGuidesPrereleaseReexport(t *testing.T) {
	err := validateDumpOrigin(&model.DBDump{Version: 6})
	if err == nil || !strings.Contains(err.Error(), "re-export") {
		t.Fatalf("error = %v, want message containing \"re-export\"", err)
	}
}

// Review Focus #2
func TestNullProductDecodesAsUnmarked(t *testing.T) {
	var dump model.DBDump
	if err := json.Unmarshal([]byte(`{"product":null,"version":1000}`), &dump); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := validateDumpOrigin(&dump); err == nil {
		t.Fatal("null product with solo version was accepted")
	}
}

// 这个测试存在的唯一理由是拦住上游同步污染: 上游 internal/op/backup.go 至 v0.13.8 仍是
// const dbDumpVersion = 5 且导入只接受 {0,5}。同步时若有人把上游取值或白名单带进来,
// 本项目的判别语义会静默改变且不报错。不要因为"断言常量等于自己"看着多余就删掉它。
// 详见 docs/superpowers/specs/2026-09-26-backup-format-identity-design.md §1.3。
func TestDumpConstantsAreIndependentOfUpstream(t *testing.T) {
	if dumpProduct != "octopus-solo" {
		t.Fatalf("dumpProduct = %q, want \"octopus-solo\"", dumpProduct)
	}
	if dbDumpVersion != 1000 {
		t.Fatalf("dbDumpVersion = %d, want 1000", dbDumpVersion)
	}
	if len(upstreamDumpVersions) != 2 {
		t.Fatalf("upstreamDumpVersions = %v, want exactly {0, 5}", upstreamDumpVersions)
	}
	for _, v := range []int{0, 5} {
		if _, ok := upstreamDumpVersions[v]; !ok {
			t.Fatalf("upstreamDumpVersions missing %d: %v", v, upstreamDumpVersions)
		}
	}
	if len(soloDumpVersions) != 1 {
		t.Fatalf("soloDumpVersions = %v, want exactly {1000}", soloDumpVersions)
	}
	if _, ok := soloDumpVersions[dbDumpVersion]; !ok {
		t.Fatalf("soloDumpVersions missing dbDumpVersion: %v", soloDumpVersions)
	}
}
```

- [ ] **Step 2: 运行，确认编译失败即红**

Run: `go test ./internal/op/...`
Expected: FAIL — 编译错误，`model.DBDump` 无 `Product` 字段、`validateDumpOrigin`/`dumpProduct`/`upstreamDumpVersions`/`soloDumpVersions` 未定义

- [ ] **Step 3: 加 `Product` 字段到 `internal/model/backup.go` 的 `DBDump`**

放在 `Version` 之前作为第一个字段，tag `json:"product"`，注释说明「产品标识; 导出恒为 octopus-solo, 缺失即视为上游或遗留格式」。

- [ ] **Step 4: 在 `internal/op/backup.go` 替换常量块**

删除 `legacyDBDumpVersion`。`const` 块留 `dumpProduct = "octopus-solo"` 和 `dbDumpVersion = 1000`，后者注释「刻意跳段以脱离上游命名空间」。新增两个包级 `var`：`upstreamDumpVersions = map[int]struct{}{0: {}, 5: {}}`、`soloDumpVersions = map[int]struct{}{dbDumpVersion: {}}`。`upstreamDumpVersions` 上方写注释：它是无标识文件的接受白名单（fork 点及之前上游用过且本项目已逐表审计过语义的版本），并附 `S1` 同步警示「不要带入上游的 dbDumpVersion，也不要往这个白名单加上游新版本」。保留原有第 14-15 行讲版本 5 语义变化的注释。

- [ ] **Step 5: 实现 `validateDumpOrigin(dump *model.DBDump) error`**

四分支按序：`dump == nil` → `fmt.Errorf("empty dump")`；`dump.Product == dumpProduct` → 查 `soloDumpVersions`；`dump.Product == ""` → 查 `upstreamDumpVersions`，命中 `6` 时返回 pre-release 专用文案；其它 → 外来 product 文案。文案逐字用 Global Constraints 里的四条。`Product` 用 `==` 比较，不引入 `strings` 的任何规范化。

- [ ] **Step 6: 改 `DBImportIncremental` 的判别调用点**

用 `if err := validateDumpOrigin(dump); err != nil { return nil, err }` 替换第 78-84 行的 `nil` 检查和版本检查。位置必须在渠道配置规范化循环**之前**——判别失败不得进入任何写入或规范化。

- [ ] **Step 7: 导出端写入标识**

`DBExportAll` 的 `&model.DBDump{...}` 字面量加 `Product: dumpProduct`。

- [ ] **Step 8: 运行，确认判别与常量测试转绿**

Run: `go test ./internal/op/...`
Expected: PASS

- [ ] **Step 9: 写导出端与零写入的失败测试**

追加到 `internal/op/backup_test.go`。第二个测试的 `bytes.Contains` 断言是 Review Focus #4，第三个是 Review Focus #5：

```go
func TestDBExportAllStampsProductAndVersion(t *testing.T) {
	dump, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatalf("export database: %v", err)
	}
	if dump.Product != "octopus-solo" || dump.Version != 1000 {
		t.Fatalf("exported product/version = %q/%d, want \"octopus-solo\"/1000", dump.Product, dump.Version)
	}
}

func TestExportedDumpRoundTrips(t *testing.T) {
	dump, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatalf("export database: %v", err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("marshal dump: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"product":"octopus-solo"`)) {
		t.Fatalf("exported JSON missing product marker: %s", raw)
	}
	var decoded model.DBDump
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}
	if err := validateDumpOrigin(&decoded); err != nil {
		t.Fatalf("round-tripped dump rejected: %v", err)
	}
	if _, err := DBImportIncremental(t.Context(), &decoded); err != nil {
		t.Fatalf("reimport round-tripped dump: %v", err)
	}
}

func TestRejectedImportWritesNothing(t *testing.T) {
	var before int64
	if err := db.GetDB().Model(&model.Channel{}).Count(&before).Error; err != nil {
		t.Fatalf("count channels: %v", err)
	}
	dump := &model.DBDump{
		Product:  "some-fork",
		Version:  1000,
		Channels: []model.Channel{{ID: 9001}},
	}
	if _, err := DBImportIncremental(t.Context(), dump); err == nil {
		t.Fatal("foreign product dump was imported")
	}
	var after int64
	if err := db.GetDB().Model(&model.Channel{}).Count(&after).Error; err != nil {
		t.Fatalf("recount channels: %v", err)
	}
	if after != before {
		t.Fatalf("channel count changed from %d to %d on rejected import", before, after)
	}
}
```

- [ ] **Step 10: 运行**

Run: `go test ./internal/op/...`
Expected: PASS（Step 6-7 已经实现了所需行为；若 `TestRejectedImportWritesNothing` 失败，说明 Step 6 的调用点位置放错了）

- [ ] **Step 11: 写包裹格式兼容测试（Review Focus #1）**

新建 `internal/server/handlers/setting_test.go`，`package handlers`。该包已有 `TestMain`，此测试不需要 DB——`decodeDBDump` 是纯函数：

```go
func TestDecodeDBDumpUnwrapsAPIResponseEnvelope(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantProduct string
		wantVersion int
	}{
		{"wrapped solo dump", `{"code":0,"message":"","data":{"product":"octopus-solo","version":1000}}`, "octopus-solo", 1000},
		{"wrapped upstream dump", `{"code":0,"message":"","data":{"version":5}}`, "", 5},
		{"bare solo dump", `{"product":"octopus-solo","version":1000}`, "octopus-solo", 1000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dump model.DBDump
			if err := decodeDBDump([]byte(c.body), &dump); err != nil {
				t.Fatalf("decodeDBDump: %v", err)
			}
			if dump.Product != c.wantProduct || dump.Version != c.wantVersion {
				t.Fatalf("product/version = %q/%d, want %q/%d", dump.Product, dump.Version, c.wantProduct, c.wantVersion)
			}
		})
	}
}
```

- [ ] **Step 12: 运行**

Run: `go test ./internal/server/handlers/... -run TestDecodeDBDumpUnwrapsAPIResponseEnvelope -v`
Expected: PASS。若 `wrapped solo dump` 失败，说明 spec §4.5 的兼容论证有误——停下报告，不要改 `setting.go` 绕过。

- [ ] **Step 13: 全量验证**

Run:
```bash
go vet ./...
go test ./internal/op/... ./internal/webhook/... ./internal/server/handlers/...
go build -trimpath -tags=jsoniter .
```
Expected: 全部 PASS。`go build` 需要 `web/dist` 已存在（前端嵌入）；若本地没构建过前端，改跑 `go vet ./...` 并在报告中说明 `go build` 未验证的原因。

- [ ] **Step 14: Commit**

```bash
git add internal/model/backup.go internal/op/backup.go internal/op/backup_test.go internal/server/handlers/setting_test.go
git commit -m "feat: 备份格式加产品标识并将版本号迁出上游命名空间"
```

---

### Task 3: 同步 V100-01 与路线图

纯文档。两处是**必须**的改写——现有措辞要求「阶段 4/5 备份导入成功」，与 Task 2 明确拒绝无标识 6 号直接矛盾，不改会让 `R1` 验收卡在一条刚被有意废弃的要求上。

**Files:**
- Modify: `plan/versions/v1.0.0-independent-release.md:24`（实现内容后追加）、`:36`（验收改写）
- Modify: `plan/version-roadmap.md:52`、`:57`、`:79`

**Interfaces:**
- Consumes: Task 2 落定的 `dbDumpVersion = 1000`
- Produces: 无代码接口

- [ ] **Step 1: V100-01 实现内容追加第六条**

在 `plan/versions/v1.0.0-independent-release.md` 第 24 行（`- 不删除已有旧迁移记录；新增迁移保持单调递增。`）之后追加：

```markdown
- 备份格式加产品标识字段并把版本号迁出上游命名空间，导入对不可判定的文件确定性报错（见 `docs/superpowers/specs/2026-09-26-backup-format-identity-design.md`）。
```

- [ ] **Step 2: V100-01 验收第一条改写**

第 36 行，原文 `- 从上游当前版本、阶段 4/5 备份和 \`v0.x\` 备份导入成功。` 改为：

```markdown
- 上游 `0`/`5` 号备份导入成功；无标识 6 号（阶段 4/5 预发布格式）被确定性拒绝并给出重新导出引导。
```

- [ ] **Step 3: 路线图三处**

`plan/version-roadmap.md`：

| 行 | 改动 |
| --- | --- |
| 52 | 表格里 `` `dbDumpVersion = 6` `` → `` `dbDumpVersion = 1000` `` |
| 57 | 整段替换（见下） |
| 79 | `上游版本与阶段 4/5 备份导入成功` → `上游 \`0\`/\`5\` 号备份导入成功且无标识 6 号被确定性拒绝` |

第 57 行原文是「注意 `dbDumpVersion` 已与上游分叉…这一撞号的修法属高风险改动，在 `R1` 的迁移审计中单独设计。」，替换为：

```markdown
`dbDumpVersion` 已与上游分叉，并在 `R1` 完成隔离：本项目的备份带 `product: "octopus-solo"` 标识、版本号跳段至 `1000`，导入按「标识 > 版本」判别，无标识文件只接受上游的 `0` 和 `5`。设计见 [backup-format-identity-design.md](../docs/superpowers/specs/2026-09-26-backup-format-identity-design.md)。
```

- [ ] **Step 4: 人工核对**

确认三份文档里不再有「阶段 4/5 备份导入成功」这类与新契约矛盾的表述：

Run: `git grep -n "阶段 4/5 备份" -- plan/`
Expected: 无输出，或仅剩描述历史的上下文（逐条判断，不是机械替换）

- [ ] **Step 5: Commit**

```bash
git add plan/versions/v1.0.0-independent-release.md plan/version-roadmap.md
git commit -m "docs: 同步 V100-01 与路线图的备份格式判据"
```

---

## 收尾

按全局规则，最后一个 task 完成后跑全量测试再停下报告，**不做**合并、推送、删分支。报告需含：改动文件、`dbDumpVersion` 与判别契约的最终形态、全部验证命令及结果、`go build` 若未跑说明原因、遗留风险（spec §12 三条）。

spec 与本计划均未 commit，是否提交待授权。
