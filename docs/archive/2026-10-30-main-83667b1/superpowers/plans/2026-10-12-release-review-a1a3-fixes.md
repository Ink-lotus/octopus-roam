# 复审 A1–A3 修复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal：** 按 `plan/release-review-20261012.md` 修掉 C1–C9 九项实现缺陷（A1），补齐本地真实更新链路与空库恢复/回滚验收并重建候选（A2），把复审结论写回发布计划与交接材料（A3）。

**Architecture：** 三层推进。先在 `internal/update`、`internal/dbprofile`、`cmd`、`scripts`、`web` 做局部补丁，每项先写能复现缺陷的失败测试；再扩展两个 PowerShell 验收脚本并在全新目录重建一次候选；最后只改文档，把"本地已收口"改写为"修复后重验，仍有待环境/待授权项"。Windows 更新链路的可测性靠把现有三个硬编码点（helper 进程句柄、父进程 PID、`syscall.Exec`）改成包内可注入变量实现，不改变生产行为。

**Tech Stack：** Go 1.26（`gorm.io/gorm` + `gorm.io/driver/{mysql,postgres}` DryRun、`go-sql-driver/mysql`、cobra、`httptest`）、Windows PowerShell 5.1 + PowerShell 7、React 19 + TanStack Query 5 + use-intl。

**Spec：** `plan/release-review-20261012.md`（第 1 节 C1–C9、第 2 节 P1–P6、第 5 节 A1/A2/A3 行）。

## Global Constraints

- 风险判定与 TDD 档位（按用户 CLAUDE.md «风险分级»）：C1/C2/C3/C7 为 L3，走完整 red-green；C4/C5/C6/C8 为 L2，补回归测试或定向验证；C9 为 L2 但仓库无 JS 测试运行器（`web/package.json` 无 vitest/jest），只做纯函数化 + `tsc`/`eslint`/locale 键一致性定向验证，**不新增依赖**；A3 文档为 L1，只做结构与链接检查。
- **全程不执行 commit。** 发布计划第一轮边界与既有结果书均记录"没有执行 commit"，`scripts/build-local.ps1` 也会把脏工作树标为 `-dirty`；本计划保持该边界。每个任务以"跑验证命令并记录输出"收尾，不以 commit 收尾。
- 不新增、不升级、不移除任何依赖；不改 `go.mod`/`go.sum`/`web/package.json`/`pnpm-lock.yaml`。
- 不改 `.github/workflows/`，不连接真实数据库，不访问真实发布源/镜像仓库，不做任何远端 Git 或托管平台操作。
- 不删除既有候选目录、既有证据、历史 `build/release-local/*` 或仓库外运行实例（`D:\tool\AI\octopus-roam-runtime\octopus-roam.exe`，PID 22324）。既有候选继续作为历史证据。
- 既有证据不得改写：新的验收写入**全新**的 `build/release-local/<新批次>/`，不追改任何已生成的哈希文件。
- Go 回归命令固定在仓库根目录显式指定包，避免扫到历史 `build/` 源码副本：`go test -tags=jsoniter . ./cmd ./internal/... ./static -count=1`；环境用 `GOTOOLCHAIN=local`、`GOPROXY=off`、`-mod=readonly`，缓存用仓库内 `.tmp-go-cache` / `.tmp-go-mod`。**不设 `GOSUMDB=off`**（复审 P5 要求统一口径）。
- 所有 PowerShell 写出的、会被 Go 解析的 JSON 一律 UTF-8 无 BOM；所有 Go 写出的、会被 PowerShell 读取的 JSON 一律显式按 UTF-8 读取。
- 数据库冒烟保持原有边界：证明写权限、不改变设置值、不清库、不删表、不扩大写入范围。
- `db-check` 的输出继续脱敏：只输出 `status`/`alias`/`type`/`checks`/`error` 分类/`duration_ms`，不得输出密码、DSN、主机、账号、库名或原始驱动错误。

## Review Focus

spec 蕴含但没有任务天然覆盖的输入/失败模式，按"最可能咬到人"排序，每条都已挂到对应任务的测试步骤：

1. `.update-ready.json` 由 PowerShell 7（`pwsh`，无 BOM）或被截断的半写入文件提供时，Go 端必须分别"正常识别为 ready"和"不把残缺内容当 ready" → Task 1 测试。
2. 中文运行目录下的**失败恢复**路径（不只是成功路径）：回滚后重启旧程序同样要拿到原样的中文参数和工作目录 → Task 2 测试覆盖成功与失败两种 case。
3. 带 `ca_file` 的 MySQL profile：改用 `NewConfig()` 后不能丢掉已注册的 TLS 名称、`charset/parseTime/loc` 参数 → Task 4 测试。
4. 合法离线 profile 与真实连接成功时 `db-check` 必须仍然退出 0 —— C6 的非零退出不能把成功路径一起带成失败 → Task 5 测试。
5. `-Target linux/arm64`（有 Linux 目标但不是 amd64）：Docker context 必须落在 `build/docker/linux/arm64/`，既不能失败也不能静默生成 amd64 路径 → Task 8 验证。

---

## Task 1：Windows 准备握手兼容 BOM（C1）

**Files:**
- Modify: `internal/update/core_apply.go`（helper 脚本内 4 处 `Set-Content`；`scheduleWindowsUpdate` 的签名、ready 解析）
- Modify: `internal/update/release.go:430`（`updateFailedState` 改用同一个 BOM 去除函数）
- Test: `internal/update/core_apply_handshake_test.go`（新建。**不要**用 `_windows_test.go` 结尾 —— 那是 Go 的隐式 GOOS 构建约束，会让其中的纯 Go 用例在非 Windows 上整体消失；平台跳过统一用运行期 `t.Skip`）

**Interfaces:**
- Consumes: 无。
- Produces:
  - `func trimJSONBOM(data []byte) []byte` —— 去掉 UTF-8 BOM 前缀后返回。
  - `func scheduleWindowsUpdate(execPath, newExecutable, stage string) (*exec.Cmd, error)` —— 返回已启动的 helper 进程，供调用方与测试回收；握手成功返回 `(cmd, nil)`。
  - `var currentProcessID = os.Getpid` —— 供 Task 10 注入父进程 PID。
  - helper 脚本内新增 PowerShell 函数 `Write-JsonUtf8([string]$Path, $Value)`，用 `[IO.File]::WriteAllText($Path, $json, (New-Object Text.UTF8Encoding $false))` 写无 BOM UTF-8。

- [ ] **Step 1: 写失败测试**

在新文件 `internal/update/core_apply_handshake_test.go` 写三个测试：

```go
func TestReadyFileParsesWithAndWithoutBOM(t *testing.T) {
    // 表驱动：{带 BOM 的 {"ready":true}} → ready=true, ok=true
    //        {无 BOM 的 {"ready":true}} → ready=true, ok=true
    //        {带 BOM 的 {"ready":false}} → ready=false, ok=true
    //        {被截断的 `{"ready":tr`}    → ok=false（不得当作 ready）
    // 断言对象：json.Unmarshal(trimJSONBOM(data), &result)
}

func TestHelperWritesReadyFileWithoutBOM(t *testing.T) {
    // 跳过条件：exec.LookPath("powershell.exe") 失败
    // 把 windowsUpdateHelper 写到临时文件，用 powershell.exe 加载它并只调用
    // Write-JsonUtf8 写 @{ready=$true}；断言文件前 3 字节不是 EF BB BF，
    // 且 json.Unmarshal 原始字节成功、ready=true。
}

func TestWindowsUpdateSchedulerCompletesReadyHandshake(t *testing.T) {
    // 跳过条件：runtime.GOOS != "windows" 或 powershell.exe 不可用
    // 准备 runtime 目录（octopus-roam.exe 用 buildUpdateFixture 的 "old"）
    // 与 stage 目录（octopus-roam.exe + octopus-roam.cmd + windows/ 四个脚本）
    // 调用生产函数 scheduleWindowsUpdate(execPath, newExecutable, stage)
    // 断言 err == nil（即 8 秒内完成握手），断言 ready 文件无 BOM 且 ready=true
    // 清理：cmd.Process.Kill() + cmd.Wait()，并断言 runtime 目录中的
    // octopus-roam.exe 仍是旧内容（握手阶段不得替换程序）
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -run 'TestReadyFileParsesWithAndWithoutBOM|TestHelperWritesReadyFileWithoutBOM|TestWindowsUpdateSchedulerCompletesReadyHandshake' ./internal/update -count=1 -v`
Expected: 编译失败（`trimJSONBOM`、`Write-JsonUtf8` 未定义；`scheduleWindowsUpdate` 返回值个数不符）。修到能编译后，`TestWindowsUpdateSchedulerCompletesReadyHandshake` 必须以"did not become ready"超时失败 —— 这就是 C1 的复现。

- [ ] **Step 3: 实现修复**

- 在 `core_apply.go` 加 `trimJSONBOM`，并在 `scheduleWindowsUpdate` 的 ready 轮询里改用 `json.Unmarshal(trimJSONBOM(data), &result)`；`release.go:430` 的内联 `bytes.TrimPrefix` 也改调 `trimJSONBOM`，全包只保留一处 BOM 规则。
- helper 脚本顶部加 `Write-JsonUtf8`，把 `$ReadyFile`（两处）和 `$stateFile`（两处）的 `| ConvertTo-Json | Set-Content ... -Encoding UTF8` 全部换成 `Write-JsonUtf8`。`$stateFile` 原有的 `-ErrorAction SilentlyContinue` 语义改为函数内 `try { ... } catch { }`，保持"写状态失败不影响主流程"。
- `scheduleWindowsUpdate` 改为返回 `(*exec.Cmd, error)`：成功握手返回 `(command, nil)`；`ErrUpdateBusy`、超时、helper 提前退出等失败路径返回 `(nil, err)`（失败路径仍先 kill/Wait 回收进程）。
- 新增 `var currentProcessID = os.Getpid`，`-ParentPid` 改用 `strconv.Itoa(currentProcessID())`。
- `updateCore` 的 Windows 分支改为 `if _, err := scheduleWindowsUpdate(...); err != nil { return err }`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/update -count=1`
Expected: PASS（Windows 相关用例实际执行，不是 skip；在输出中确认）。

- [ ] **Step 5: 记录验证输出**

把命令与完整输出追加到 `build/release-local/review-fix-<批次>/logs/task01-update-bom.log`（目录按 Task 11 的批次名建立，先建 `logs/`）。不 commit。

---

## Task 2：helper 参数按 UTF-8 读取，覆盖中文路径（C2）

**Files:**
- Modify: `internal/update/core_apply.go`（helper 脚本内 `$ArgumentsFile` 读取）
- Modify: `internal/update/release_test.go`（`runWindowsHelperCase` 改用中文目录/中文配置路径）
- Test: `internal/update/release_test.go` 既有 `TestWindowsUpdateHelperReplacesAndRecoversRealProcesses` 两个子用例

**Interfaces:**
- Consumes: Task 1 的 `Write-JsonUtf8`（state/ready 文件编码已统一）。
- Produces: 无新导出符号。

- [ ] **Step 1: 写失败测试**

改 `runWindowsHelperCase`：
- `root` 改为 `filepath.Join(t.TempDir(), "运行目录 with spaces")`，`prepared` 改为 `filepath.Join(t.TempDir(), "准备目录 with spaces")`，`workdir` 改为 `filepath.Join(root, "工作 目录")`。
- `arguments.Arguments` 改为 `[]string{"--config", filepath.Join(workdir, "配置 文件.json"), `quote"tail\`}`。
- 成功与失败两个 case 都断言重启结果的 `Cwd == workdir` 且每个 arg 逐字相等（失败 case 原本只断言 `Marker`/`Cwd`，补上 args 逐项比较）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -run TestWindowsUpdateHelperReplacesAndRecoversRealProcesses ./internal/update -count=1 -v`
Expected: FAIL —— helper 以系统 ANSI 解码 `.update-arguments.json`，`Cwd`/args 中的中文变成乱码，断言不匹配（C2 复现）。

- [ ] **Step 3: 实现修复**

helper 脚本把
`$restart = Get-Content -LiteralPath $ArgumentsFile -Raw | ConvertFrom-Json`
改为
`$restart = [IO.File]::ReadAllText($ArgumentsFile, [Text.Encoding]::UTF8) | ConvertFrom-Json`
（`ReadAllText` 带编码参数时仍会识别并剥掉 BOM，兼容 Go 写出的无 BOM UTF-8）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -run 'TestWindowsUpdateHelper' ./internal/update -count=1 -v`
Expected: PASS，两个子用例均实际执行。

- [ ] **Step 5: 记录验证输出** → `logs/task02-helper-unicode.log`。不 commit。

---

## Task 3：Unix exec 失败后恢复备份并以非零状态退出（C7）

**Files:**
- Modify: `internal/update/core.go`（`restartExecutable`）
- Modify: `internal/update/core_apply.go`（`replaceUnixExecutable` 返回备份目录；`updateCore` 传递）
- Modify: `internal/update/release.go`（`restartAfterUpdate` 变量类型；新增 `execProcess`）
- Modify: `internal/update/release_test.go`（两处 `restartAfterUpdate = func(string)` 改签名）
- Test: `internal/update/core_restart_test.go`（新建）

**Interfaces:**
- Consumes: `exitProcess`（`core_apply.go` 已有）、`testRuntimeGOOS`（`release.go` 已有）。
- Produces:
  - `func replaceUnixExecutable(execPath, newExecutable string) (string, error)` —— 第一个返回值是备份目录。
  - `func restartExecutable(execPath, backupDir string)`。
  - `func restoreUpdateBackup(execPath, backupDir string) error` —— 把 `backupDir/<base(execPath)>` 原子换回 `execPath`。
  - `var execProcess = syscall.Exec`。
  - `var restartAfterUpdate = restartExecutable`（类型变为 `func(string, string)`）。

- [ ] **Step 1: 写失败测试**

```go
func TestUnixRestartRestoresBackupAndExitsNonZeroWhenExecFails(t *testing.T) {
    // 注入 testRuntimeGOOS="linux"；execProcess 返回固定错误；
    // exitProcess 记录 code 而不退出；shutdown 不注册任何任务。
    // 准备 execPath（内容 "new"）与 backupDir/<base> （内容 "old"）。
    // 调用 restartExecutable(execPath, backupDir)
    // 断言：execPath 内容恢复为 "old"；execProcess 被调用两次（原地重试一次）；
    //      exitProcess 收到 1。
}

func TestUnixRestartExitsNonZeroWhenBackupIsMissing(t *testing.T) {
    // 同上但 backupDir 为空目录；断言 execProcess 只调用一次、exitProcess 收到 1、
    // execPath 内容保持 "new"（不伪造恢复）。
}

func TestUnixReplaceReturnsBackupDirectoryContainingPreviousExecutable(t *testing.T) {
    // 调用 replaceUnixExecutable，断言返回的目录里存在与替换前字节一致的旧程序。
}

func TestUnixRestartRecoveryWithRealProcesses(t *testing.T) {
    // 平台用例：runtime.GOOS == "windows" → t.Skip("Unix 进程恢复用例需要 Unix 宿主")
    // 在 Unix 上：构建 old/new 两个 fixture，把 new 换成不可执行文件，
    // 走 replaceUnixExecutable + restartExecutable，断言恢复后旧程序可被启动。
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -run 'TestUnixRe' ./internal/update -count=1 -v`
Expected: 编译失败（`execProcess`、`restoreUpdateBackup` 未定义，`replaceUnixExecutable` 返回值个数不符）；修到能编译后前两个用例因"没有恢复、没有非零退出"而 FAIL。第四个用例在本机 SKIP（必须作为 skip 如实记录）。

- [ ] **Step 3: 实现修复**

- `replaceUnixExecutable` 返回 `backupDir`；`updateCore` 接住并在 goroutine 里调用 `restartAfterUpdate(execPath, backupDir)`。
- `restartExecutable(execPath, backupDir)`：`shutdown.Shutdown()` 后，Windows 分支内的 `os.Exit(0)` 改为 `exitProcess(0); return`，分支条件 `runtime.GOOS` 改为 `testRuntimeGOOS`（与包内既有注入模式一致）；Unix 分支首次 `execProcess` 失败后调用 `restoreUpdateBackup`，恢复成功则再 `execProcess` 一次，仍失败则 `log.Errorf` 并 `exitProcess(1)`；恢复失败直接 `log.Errorf` + `exitProcess(1)`。
- `restoreUpdateBackup`：把备份复制到 `execPath + ".restore-<nano>"`，`os.Chmod` 到 0755，再 `os.Rename` 覆盖 `execPath`；任一步失败返回包装错误并清理临时文件。
- 删除 `core.go` 中已不需要的 `runtime` import（若 `testRuntimeGOOS` 替换后无其他引用）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/update -count=1 -v`
Expected: PASS；`TestUnixRestartRecoveryWithRealProcesses` 显示为 SKIP。

- [ ] **Step 5: 记录验证输出** → `logs/task03-unix-restart.log`，并在日志末尾明确写出哪条是 skip。不 commit。

---

## Task 4：MySQL profile 保留驱动默认认证能力（C3）

**Files:**
- Modify: `internal/dbprofile/profile.go:258`（`BuildConnectionSpec` 的 mysql 分支）
- Test: `internal/dbprofile/profile_test.go`

**Interfaces:**
- Consumes: 无。
- Produces: 无签名变化（`BuildConnectionSpec` 行为变化）。

- [ ] **Step 1: 写失败测试**

```go
func TestMySQLDSNPreservesDriverDefaultsAndTLS(t *testing.T) {
    // 用 validMySQLProfile() 生成 spec，再 mysqlDriver.ParseDSN(spec.DSN)
    // 断言：AllowNativePasswords == true
    //      CheckConnLiveness == true
    //      TLSConfig == "true"
    //      Params["charset"]=="utf8mb4" && Params["parseTime"]=="True" && Params["loc"]=="Local"
    //      DBName/User/Addr 与 profile 一致
}

func TestMySQLDSNWithCAFileKeepsRegisteredTLSName(t *testing.T) {
    // 在 t.TempDir() 下用 crypto/x509 + ecdsa 自签一张证书写成 PEM（合成材料，不连网）
    // profile 带绝对路径 ca_file；断言 spec.TLSName 非空、ParseDSN 后
    // TLSConfig == spec.TLSName、AllowNativePasswords 仍为 true
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -run TestMySQLDSN ./internal/dbprofile -count=1 -v`
Expected: FAIL —— `AllowNativePasswords=false`、`CheckConnLiveness=false`（C3 复现）。

- [ ] **Step 3: 实现修复**

mysql 分支改为 `config := mysqlDriver.NewConfig()`，随后逐项赋值 `User`/`Passwd`/`Net`/`Addr`/`DBName`/`Params`/`TLSConfig`，保留 CA 文件注册与 `config.TLSConfig = tlsName` 覆盖逻辑不变。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/dbprofile -count=1`
Expected: PASS（含既有 4 个用例）。

- [ ] **Step 5: 记录验证输出** → `logs/task04-mysql-dsn.log`。不 commit。

---

## Task 5：db-check 默认档案路径与失败退出码（C5 + C6）

**Files:**
- Modify: `cmd/dbcheck.go:39`（`Run` → `RunE`、`SilenceErrors`/`SilenceUsage`）、`:81`（`profilePath`）
- Test: `cmd/dbcheck_test.go`（新建）

**Interfaces:**
- Consumes: `dbprofile.LoadFile`、`classifyProfileError`（现有）。
- Produces:
  - `func profilePath() string` —— 默认 `filepath.Join(home, ".config", "octopus-roam", "db-profiles.json")`。
  - `var errDatabaseCheckFailed = errors.New("database check reported a failure")` —— 失败分支返回它，JSON 已先行输出。

- [ ] **Step 1: 写失败测试**

```go
func TestProfilePathDefaultsToUserDotConfig(t *testing.T) {
    // dbProfileFile = ""；home := t.Setenv("USERPROFILE"/"HOME", 临时目录) 后
    // 断言 profilePath() == filepath.Join(home, ".config", "octopus-roam", "db-profiles.json")
    // 并断言显式 dbProfileFile 时原样返回（t.Cleanup 复位包级变量）
}

func TestDatabaseCheckReturnsErrorForMissingProfile(t *testing.T) {
    // 捕获 os.Stdout 到 pipe；设置 --profile 指向不存在的合成路径、--alias 合成别名
    // 执行 databaseCheckCmd.RunE；断言 errors.Is(err, errDatabaseCheckFailed)
    // 断言 stdout 的 JSON 为 status=invalid_profile、error=profile_missing
    // 断言 databaseCheckCmd.SilenceErrors && databaseCheckCmd.SilenceUsage
}

func TestDatabaseCheckReturnsNilForValidOfflineProfile(t *testing.T) {
    // 写一份合成有效 mysql profile（合成主机/账号/密码），不带 --connect
    // 断言 err == nil 且 JSON status=profile_valid（Review Focus #4）
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -run 'TestProfilePath|TestDatabaseCheck' ./cmd -count=1 -v`
Expected: 编译失败（`errDatabaseCheckFailed`、`RunE` 未定义）；改到能编译后默认路径用例因 `os.UserConfigDir()` 指向 `%AppData%` 而 FAIL。

- [ ] **Step 3: 实现修复**

- `profilePath`：`os.UserHomeDir()` 成功时返回 `~/.config/octopus-roam/db-profiles.json`；失败时沿用 `./db-profiles.json` 兜底。
- 命令结构体加 `SilenceErrors: true, SilenceUsage: true`，`Run` 改 `RunE func(cmd *cobra.Command, args []string) error`：每个失败分支先 `writeDatabaseCheckResult` 再 `return errDatabaseCheckFailed`；`status = "passed"` 分支 `return nil`。`rootCmd.Execute()` 已有 `os.Exit(1)`，无需改 `cmd/root.go`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./cmd -count=1`
Expected: PASS。

- [ ] **Step 5: 记录验证输出** → `logs/task05-dbcheck-cli.log`。不 commit。

---

## Task 6：读写冒烟改用方言安全的标识符引用（C4）

**Files:**
- Modify: `cmd/dbcheck.go:137`（`runDatabaseSmoke` 的设置写入）
- Test: `cmd/dbcheck_smoke_test.go`（新建）

**Interfaces:**
- Consumes: Task 5 的 `errDatabaseCheckFailed`（同包，不直接依赖）。
- Produces: `func buildSettingTouchSQL(dialect gorm.Dialector, value string) (string, error)` —— 用 `gorm.Config{DryRun: true}` 生成"不改变取值"的设置更新语句，供测试跨方言核对；`runDatabaseSmoke` 使用同一套 GORM 构造实际执行。

- [ ] **Step 1: 写失败测试**

```go
func TestSettingTouchSQLQuotesReservedColumnPerDialect(t *testing.T) {
    // 表驱动：mysql.New(mysql.Config{DSN: 合成 DSN, SkipInitializeWithVersion: true})
    //        postgres.New(postgres.Config{DSN: "postgres://u:p@127.0.0.1:1/db?sslmode=disable"})
    //        sqlite 现用驱动
    // 对每个方言：sql, err := buildSettingTouchSQL(dialector, "10")
    // 断言 MySQL 结果含 "`key`" 且不含裸 " key "；PostgreSQL 结果含 `"key"`；
    // 三者都以 UPDATE 开头并包含 settings 表名。
    // DryRun 不建立连接（用例不得产生网络请求）。
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -run TestSettingTouchSQL ./cmd -count=1 -v`
Expected: 编译失败（`buildSettingTouchSQL` 未定义）—— 当前实现是 raw SQL，根本没有方言生成点（C4 的静态结论）。

- [ ] **Step 3: 实现修复**

- `buildSettingTouchSQL`：`gorm.Open(dialector, &gorm.Config{DryRun: true})` 后
  `db.Model(&model.Setting{}).Where(&model.Setting{Key: model.SettingKeyStatsSaveInterval}).Update("value", value)`，
  返回 `tx.Statement.SQL.String()`。结构体条件与 `Update(column, …)` 都由 GORM 按方言引用标识符。
- `runDatabaseSmoke`：先用同样的结构体条件 `Take` 出当前 `stats_save_interval` 的值，再用 `Model(...).Where(...).Update("value", 取出的值)` 写回同值，并检查 `RowsAffected == 1`。保持"证明写权限、不改变设置值、不新增行"的既有范围。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./cmd -count=1 -v`
Expected: PASS；在日志中确认用例未发起连接（无超时、耗时为毫秒级）。

- [ ] **Step 5: 记录验证输出** → `logs/task06-dialect-sql.log`。真库确认留给 C 组授权后执行，日志末尾注明"MySQL/PostgreSQL 真实执行待授权"。不 commit。

---

## Task 7：更新 UI 结束"准备重启"状态（C9）

**Files:**
- Modify: `web/src/api/update.ts`
- Modify: `web/src/components/modules/setting/Info.tsx:107-134`
- Modify: `web/src/locales/zh_hans.json`、`en.json`、`zh_hant.json`（`setting.info` 段）

**Interfaces:**
- Consumes: `useNowVersion`、`useLatestInfo`（现有）。
- Produces（`web/src/api/update.ts` 导出）：
  - `export type UpdatePhase = 'idle' | 'pending' | 'completed' | 'failed' | 'unconfirmed'`
  - `export interface UpdateAttempt { targetVersion: string; previousVersion: string; deadline: number }`
  - `export const UPDATE_CONFIRM_WINDOW_MS = 30000`
  - `export function resolveUpdatePhase(attempt: UpdateAttempt | null, nowVersion: string, status: UpdateStatus | undefined, now: number): UpdatePhase` —— 纯函数，判定顺序：无 attempt → `idle`；`status === 'update_failed'` → `failed`；`nowVersion` 非空且（等于 `targetVersion` 或不等于 `previousVersion`）→ `completed`；`now >= attempt.deadline` → `unconfirmed`；否则 `pending`。
  - `export function useUpdateProgress(): UpdatePhase` —— 内部用 `useNowVersion`/`useLatestInfo` 的数据加 1 秒 `setInterval` tick（仅在 `pending` 时启用），并把结果写回模块变量供轮询判定。
  - `useUpdateCore()` 的 `mutate` 变量变为 `{ targetVersion: string; previousVersion: string }`。
- 新增 locale 键：`setting.info.updateCompleted`、`setting.info.updateUnconfirmed`（三语）。

- [ ] **Step 1: 写判定表并落为纯函数签名**

本仓库无 JS 测试运行器（`web/package.json` 无 vitest/jest），新增运行器属于"新增依赖"门禁。因此先在 `web/src/api/update.ts` 的 `resolveUpdatePhase` 上方用注释固定判定表（5 条输入 → 期望相位，含"版本已变但不等于 target"= `completed`、"30 秒内版本未变"= `pending`、"超过 30 秒版本未变"= `unconfirmed`），作为后续引入运行器时的用例清单。**在交付说明中如实写出"该项没有自动化测试，只做类型/静态/人工检查"。**

- [ ] **Step 2: 确认当前缺陷（人工复现路径）**

Run: 阅读 `Info.tsx:112`（`disabled={updateCore.isPending || updateCore.isSuccess}`）与 `:126`（`updateCore.isSuccess && ...`）
Expected: 确认 `isSuccess` 一经为真不再清除，与后端版本变化无关 —— 即 C9。

- [ ] **Step 3: 实现**

- `update.ts`：用模块级 `let attempt: UpdateAttempt | null` 与 `let phase: UpdatePhase` 取代 `updateConfirmationUntil`；`refetchInterval` 改为 `() => (phase === 'pending' ? 1000 : 3600000)`；`useUpdateCore().onSuccess` 用 mutation 变量记录 `attempt`（`deadline = Date.now() + UPDATE_CONFIRM_WINDOW_MS`）并失效两个 query；导出 `resolveUpdatePhase`、`useUpdateProgress`，以及供测试/切页复位的 `resetUpdateAttempt()`。
- `Info.tsx`：`const phase = useUpdateProgress()`；按钮 `disabled={updateCore.isPending || phase === 'pending'}`；`onClick` 传 `{ targetVersion: latestInfo?.tag_name ?? '', previousVersion: backendNowVersion }`；消息区按 `phase` 分支渲染 `pending`/`completed`/`unconfirmed`/`failed`，`updateCore.isError` 保留。
- 三个 locale 文件在 `setting.info` 下加 `updateCompleted`、`updateUnconfirmed`（zh_hans 例："更新已完成，当前版本已切换。"／"30 秒内未确认到新版本，请稍后刷新页面或检查安装目录。"），英文与繁体同义。

- [ ] **Step 4: 运行检查确认通过**

Run（在 `web/` 下直接调用已有 `node_modules`，不走 pnpm 自动安装）：
```powershell
node web/node_modules/typescript/bin/tsc --noEmit --project web/tsconfig.json
node web/node_modules/eslint/bin/eslint.js .
node -e "const a=require('./web/src/locales/en.json'),b=require('./web/src/locales/zh_hans.json'),c=require('./web/src/locales/zh_hant.json');const k=o=>{const r=[];(function w(x,p){for(const n of Object.keys(x)){const q=p?p+'.'+n:n;typeof x[n]==='object'&&x[n]!==null?w(x[n],q):r.push(q)}})(o,'');return r.sort()};const [x,y,z]=[k(a),k(b),k(c)];const s=JSON.stringify;if(s(x)!==s(y)||s(x)!==s(z))throw new Error('locale keys differ');console.log('locale keys match: '+x.length)"
```
Expected: 三条命令退出码 0；locale 命令打印键数。

- [ ] **Step 5: 记录验证输出** → `logs/task07-update-ui.log`。不 commit。

---

## Task 8：本地构建按实际 Linux 目标生成 Docker context（C8）

**Files:**
- Modify: `scripts/build-local.ps1:282-302`（Docker context 段）、`:352`（`docker_context` 元数据）

**Interfaces:**
- Consumes: 循环中已有的 `$targets`、`$packageRoot`。
- Produces: `build-info.json` 的 `docker_context` 变为 `[ordered]@{ path; status; platforms = @(...) }`；`platforms` 为实际生成的 `linux/<arch>` 列表，无 Linux 目标时为空数组且 `status = 'not-applicable: no linux target was built'`。

- [ ] **Step 1: 写失败验证（先复现）**

Run:
```powershell
pwsh -NoProfile -File scripts/build-local.ps1 -OutputRoot <全新临时目录>\c8-before -Target windows/amd64 -Version v0.1.0-local.c8probe
```
Expected: 前面的 Windows 目标构建成功，随后在 `Copy-Item ... packages\linux-amd64\octopus-roam` 处抛错 —— C8 复现。日志留存。

- [ ] **Step 2: 实现修复**

- 把 Linux 目标映射到 Dockerfile 的 `TARGETPLATFORM` 目录：`amd64 → linux/amd64`、`arm64 → linux/arm64`、`386 → linux/386`、`arm → linux/arm/v7`（与 `scripts/build.sh:147-150` 一致）。
- 对 `$targets` 中每个 `linux/*` 生成 `docker-context\build\docker\<platform>\octopus-roam`；Dockerfile、entrypoint、LICENSE、compose、许可证报告仍各复制一次。
- 无 Linux 目标时：不创建 `build\docker`，仅写 `DOCKER-BUILD-STATUS.md` 说明"未构建 Linux 目标，Docker context 不适用"，并把 `docker_context.status` 设为 `not-applicable: no linux target was built`。
- `DOCKER-BUILD-STATUS.md` 的第一行由固定文案改为列出实际包含的平台。

- [ ] **Step 3: 运行验证确认通过**

Run（两次，均用全新目录）：
```powershell
pwsh -NoProfile -File scripts/build-local.ps1 -OutputRoot <新目录>\c8-windows-only -Target windows/amd64 -Version v0.1.0-local.c8win
pwsh -NoProfile -File scripts/build-local.ps1 -OutputRoot <新目录>\c8-linux-arm64 -Target linux/arm64 -Version v0.1.0-local.c8arm
```
Expected:
- 第一次完成收口，`build-info.json` 的 `docker_context.status` 为 `not-applicable: ...`、`platforms` 为空，且目录中不存在 `docker-context\build\docker`。
- 第二次完成收口，存在 `docker-context\build\docker\linux\arm64\octopus-roam`，`platforms` 为 `@('linux/arm64')`（Review Focus #5）。
- 两次都另跑 `pwsh -NoProfile -Command "[scriptblock]::Create((Get-Content -Raw scripts/build-local.ps1)) | Out-Null"` 确认解析通过。

- [ ] **Step 4: 记录验证输出** → `logs/task08-build-subset.log`，并记录两个探针目录路径（保留，不删除）。不 commit。

---

## Task 9：SQLite 空库恢复与失败回滚验收（A2 本地验收缺口）

**Files:**
- Modify: `scripts/test-runtime-sqlite.ps1`

**Interfaces:**
- Consumes: 现有 `Invoke-Json`、`Wait-Endpoint`、`Get-Setting`、`$Executable`、`$Port`。
- Produces: 新增参数 `[int]$RestorePort = $Port + 1`；输出 JSON 的 `checks` 追加 `empty_database_restore`、`business_content_restore`、`rejected_restore_rollback`；新增 `restore_runtime` 字段记录第二份库的相对位置。

- [ ] **Step 1: 写失败验收步骤**

在现有"原库导入恢复"之后追加：
1. 用**第二份全新空 SQLite 库**（新的 `data2/data.db`、新的 config、`$RestorePort`）启动第二个隔离实例；登录；断言这是空库（`stats_save_interval == '10'`，渠道/分组/API Key 列表为空）。
2. 向空库导入第一份库导出的加密备份；重新登录；断言 `stats_save_interval == '11'`，且 `local-sqlite-channel`、`local-sqlite-model`、`local-sqlite-api-key` 三项业务内容各恰好 1 条（覆盖复审"仅断言一个设置恢复"的缺口）。
3. 失败回滚用例：对空库实例再用**错误密码**导入同一备份，断言请求非 2xx；随后重新查询，断言设置与三项业务内容与步骤 2 完全一致（拒绝的导入不得留下部分写入）。
4. `finally` 中停止并删除第二份运行目录；两个实例都断言停止后退出码可取到。

- [ ] **Step 2: 运行确认当前脚本不覆盖**

Run: `pwsh -NoProfile -Command "[scriptblock]::Create((Get-Content -Raw scripts/test-runtime-sqlite.ps1)) | Out-Null"` 后，针对已有的 Windows 候选包可执行文件跑改动前版本
Expected: 改动前的输出 `checks` 中没有 `empty_database_restore` 等三项 —— 缺口确认。

- [ ] **Step 3: 实现**

按 Step 1 扩展脚本，保持"临时目录 + 合成数据 + 不触碰现有运行实例"的既有边界，并保持启动时设置 `OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1`。

- [ ] **Step 4: 运行验收确认通过**

Run:
```powershell
pwsh -NoProfile -File scripts/test-runtime-sqlite.ps1 -Executable <Task 11 候选解压出的 octopus-roam.exe> -Port 18090
```
Expected: 输出 `status=passed`，`checks` 含全部 12 项；`$LASTEXITCODE` 为 0。
另外跑一次失败注入（临时把期望值改错）确认脚本确实会非零退出，然后改回 —— 证明断言有效。

- [ ] **Step 5: 记录验证输出** → `logs/task09-sqlite-restore.log`。不 commit。

---

## Task 10：真实入口到替换/重启/版本确认的完整 Windows 链路（A2）

**Files:**
- Test: `internal/update/core_apply_windows_test.go`（在 Task 1 的文件中追加）

**Interfaces:**
- Consumes: Task 1 的 `scheduleWindowsUpdate` 返回值与 `currentProcessID`；`release.go` 既有的 `testHTTPClient`、`releaseAPIEndpoint`、`releaseDownloadEndpoint`、`testRuntimeGOOS/GOARCH`、`executablePath`、`exitProcess`；`release_test.go` 既有的 `buildUpdateFixture`、`waitForRestartResult`。
- Produces: 无新生产符号。

- [ ] **Step 1: 写失败测试**

```go
func TestWindowsUpdateCoreReplacesRestartsAndConfirmsVersion(t *testing.T) {
    // 跳过：runtime.GOOS != "windows" 或 powershell.exe 不可用
    // 1. 起一个真实的 "parent" fixture 进程（sleep 30s），把 currentProcessID
    //    注入为它的 PID —— 这样 helper 等待的是可控进程，而非测试进程本身。
    // 2. 用 httptest 搭建合成发布源：releases JSON（tag v9.9.9）、平台 zip、SHA256SUMS。
    //    zip 内含 new fixture（octopus-roam.exe）+ octopus-roam.cmd + windows/ 四个脚本。
    // 3. runtime 目录放 old fixture 及同名支持文件；executablePath 指向它；
    //    conf.Version 设为更低的稳定版；testRuntimeGOOS/GOARCH = windows/amd64。
    // 4. exitProcess 换成记录 code 的桩（updateCore 的 goroutine 会调用它）。
    // 5. 调用 UpdateCore()；断言 err == nil。
    // 6. kill parent fixture，模拟旧进程退出。
    // 7. 断言：runtime 目录的 octopus-roam.exe 字节等于 new fixture；
    //         octopus-roam.cmd 等于新包内容；
    //         waitForRestartResult 得到 marker=="new" 且 cwd/args 与 os.Args[1:] 一致
    //         （版本确认：重启起来的是新程序）；
    //         .octopus-roam-update.json 的 failed == false；
    //         stage 目录已被 helper 删除。
    // 8. 清理：kill 残留 helper 与被重启的 fixture 进程，恢复所有注入变量。
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -run TestWindowsUpdateCoreReplacesRestartsAndConfirmsVersion ./internal/update -count=1 -v`
Expected: 先因 `currentProcessID` 不可注入/未定义而编译失败（若 Task 1 已落地则直接进入运行）；在 Task 1 的 BOM 修复被临时还原时必须以"准备超时"失败 —— 用于证明该用例确实能抓住 C1。实现完成后应通过。

- [ ] **Step 3: 补齐可注入点（若 Task 1 未覆盖）**

仅允许新增包内注入变量（`currentProcessID`），不改变生产路径行为。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/update -count=1 -v`
Expected: PASS，且该用例实际执行。最后用 `Get-Process powershell,octopus-roam -ErrorAction SilentlyContinue` 确认无本轮残留 helper/fixture 进程。

- [ ] **Step 5: 记录验证输出** → `logs/task10-windows-e2e.log`，附进程残留检查输出。不 commit。

---

## Task 11：全量回归与新候选重建（A2 收口）

**Files:**
- Create: `build/release-local/review-fix-<批次>/`（全新唯一目录，`<批次>` = `yyyyMMdd-HHmmss` + 8 位随机）

**Interfaces:**
- Consumes: Task 1–10 的全部改动；`scripts/build-local.ps1`、`scripts/test-runtime-sqlite.ps1`。
- Produces: 新候选目录及其 `build-info.json`、`evidence-index.md`、`verification-log.md`、`file-sha256sums.txt`。

- [ ] **Step 1: 跑全量 Go / 前端 / 脚本回归**

Run（仓库根目录，显式列包，避免扫到历史 `build/` 副本 —— 复审 P5）：
```powershell
$env:GOTOOLCHAIN='local'; $env:GOPROXY='off'; $env:GOFLAGS='-mod=readonly'
$env:GOCACHE="$PWD\.tmp-go-cache"; $env:GOMODCACHE="$PWD\.tmp-go-mod"
go test . ./cmd ./internal/... ./static -count=1
go test -tags=jsoniter . ./cmd ./internal/... ./static -count=1 -p=1
go vet ./cmd ./internal/... ./static
go test -race ./internal/relay ./internal/op ./internal/task ./internal/server/handlers ./internal/update -count=1 -p=1
node web/node_modules/typescript/bin/tsc --noEmit --project web/tsconfig.json
node web/node_modules/eslint/bin/eslint.js .
git diff --check
```
Expected: 全部通过。`jsoniter` 与 `race` 套件沿用结果书记录的 `-p=1`（既有事件测试稳定性风险）。任何失败都按 `superpowers:systematic-debugging` 处理，不跳用例、不改产品代码绕过。

- [ ] **Step 2: 重建候选**

Run:
```powershell
pwsh -NoProfile -File scripts/build-local.ps1 `
  -OutputRoot D:\tool\AI\octopus-roam\build\release-local\review-fix-<批次> `
  -Version v0.1.0-local.<批次> `
  -VerificationScript <已审查的 verify.ps1 入口>
```
Expected: 七目标全部构建、归档权限/creator 检查通过、源码输入与完整文件哈希自检通过、`license_report` 仍为 unavailable（不伪造）。`-VerificationScript` 使用本轮新建的、已逐行审查过的入口（内部调用 Task 9 的 `test-runtime-sqlite.ps1` 并断言退出码）。

- [ ] **Step 3: 对新候选跑 db-check 退出码验收**

Run: 对解压出的 `octopus-roam.exe` 分别执行
- 合法合成 profile（不带 `--connect`）→ 断言 JSON `profile_valid` 且 `$LASTEXITCODE -eq 0`
- 不存在的合成 profile → 断言 JSON `profile_missing` 且 `$LASTEXITCODE -ne 0`（C6 验收）
- 默认路径（不传 `--profile`）→ 断言它找的是 `$env:USERPROFILE\.config\octopus-roam\db-profiles.json`（C5 验收；该文件不存在时报 `profile_missing`，不读取用户任何真实档案）

Expected: 三项均符合；输出中无连接材料。

- [ ] **Step 4: 残留与安全检查**

Run: 进程残留检查、候选目录与日志的密钥/DSN/主机/账号扫描、`git status --short` 复核新增文件清单。
Expected: 无本轮残留进程、无敏感内容、无计划外文件（尤其不得新增依赖锁文件或 workflow 变更）。

- [ ] **Step 5: 记录验证输出** → `logs/task11-regression-and-candidate.log` + 新候选自带证据。不 commit。

---

## Task 12：计划、结果书与交接材料修订（A3）

**Files:**
- Modify: `plan/release-plan-v0.1.0.md`（开头状态、L6.5 执行状态、L6.5"已知收口事项"、L6.5 回归命令块、第 7 节交接）
- Modify: `plan/release-local-result.md`（结论、L4 回归命令、L5 矩阵、L6.5 表、L6 待办）
- Modify: `plan/cloud-database-test-handoff.md`（第 1 节路径表述、第 2.1 节"系统用户配置目录"、第 6 节可粘贴说明）
- Modify: `plan/release-upstream-feature-comparison.md`（"渠道价格刷新"一行拆成三条路径）
- Modify: `plan/release-workflow-proposal.md`（补 `build.sh --output-dir` 与 Dockerfile 固定路径的 R1 映射说明）
- Create: `plan/docker-manual-acceptance-handoff.md`（本地测试镜像的完整构建命令、Compose 模板、数据库占位配置、操作说明、C1–C9 清单、执行权限要求）
- Modify: `scripts/dockerfile/Dockerfile`（为 `/app/${APP_NAME}` 增加执行权限）

**Interfaces:**
- Consumes: Task 1–11 的实际结果与新候选路径/哈希。
- Produces: 文档；无代码符号。

- [ ] **Step 1: 改 Dockerfile 执行权限并验证**

在 `COPY build/docker/...` 之后把 `RUN chmod +x /entrypoint.sh` 扩展为同时 `chmod +x /app/${APP_NAME}`。理由：context 由 Windows/NTFS 生成时不携带 0755，`COPY` 会落成 0644，entrypoint 将无法执行程序（复审第 2 节 P2）。
Run: `bash -n scripts/dockerfile/entrypoint.sh` 与对 Dockerfile 的逐行人工复核（本机无 Docker daemon，**不声称镜像构建通过**）。

- [ ] **Step 2: 写 Docker 人工验收交接材料**

`plan/docker-manual-acceptance-handoff.md` 必须包含：
- 从新候选 `docker-context/` 出发的完整 `docker buildx build` 命令（显式 `--platform`、`-f scripts/dockerfile/Dockerfile`、本地 tag 如 `octopus-roam:local-test-<批次>`），并写明 `build/THIRD_PARTY_LICENSES.csv` 缺失时构建会失败 —— 这是阻塞项而非可跳过项。
- 指向**本地测试镜像**（不是未发布的 GHCR tag）的 Compose 模板，带 SQLite 数据卷与占位数据库配置示例（只含合成值）。
- 执行权限要求：使用保留权限的归档，或依赖 Step 1 的 `chmod +x`，并在验收步骤里实际 `docker run ... ls -l /app/octopus-roam` 核对。
- C1–C9 人工验收清单与回传字段（版本/哈希、CPU 架构、Docker/Compose 版本、结果、脱敏日志）。
- 明确边界：镜像未生成、许可证报告缺失、不修改任何现有部署环境。

- [ ] **Step 3: 改发布计划**

- 开头与 L6.5 执行状态改为复审建议的状态表述：**"既有候选与证据已保存且哈希一致；复审发现本地实现缺陷和验收缺口，已完成 A1 修复与 A2 本地验收并重建候选；许可证和真实环境验收继续待补。"**
- L6.5"已知收口事项"中 `Compress-Archive` 那条标注为**历史问题已解决**（现为 GNU tar + libarchive，候选哈希已核对），不据旧条目再改归档实现。
- L6.5 回归命令块：删除 `GOSUMDB=off`，改为显式包列表 `. ./cmd ./internal/... ./static`，并写明在哪个冻结源码目录执行；若在仓库根目录执行则必须显式列包（复审 P5）。
- 第 7 节：把"输入未变不重建"与"无条件重建候选"统一为"先核对证据，只在源码/构建输入变化或存在明确产物问题时重建；本轮已因产品修复重建"。
- 补复审第 2 节 P1 的用例对应表（L3 真实链路、中文路径/失败恢复、L5.2 最终包流程、L5.3 空库恢复、L5.3 MySQL/PostgreSQL），逐行写明本轮实际完成到哪一步、哪些仍待环境/待授权。其中 **L5.2 的最终包普通/流式请求、取消/失败切换、日志统计与页面交互本轮不执行**，明确标为"待验"并写出它需要的本地模拟上游服务与交互记录范围 —— 不借既有单元测试冒充最终包验收。
- P4：明确 R0 范围决定与工具/环境准备、获准的 R1 本地准备可用于解除阻碍，但不等于候选通过，也不授权 R2/R3 远端变更。

- [ ] **Step 4: 改结果书与两份交接**

- `release-local-result.md`：纠正"本地可执行工作已完成"的概括；追加复审与本轮修复小节（C1–C9 逐项状态、新候选目录与哈希、新增/修改的测试、仍为 skip 的 Unix 用例、C9 无自动化测试）；保留旧候选与原始记录作为历史证据，**不改写任何既有哈希**。
- `cloud-database-test-handoff.md`：第 2.1 节"系统用户配置目录"改为"执行用户个人目录下的 `~/.config/octopus-roam/db-profiles.json`（Windows 为 `%USERPROFILE%\.config\...`）"，与第 1 节及修复后的实现一致；第 6 节可粘贴说明同步。
- `release-upstream-feature-comparison.md`：把"渠道价格刷新"拆成三条独立路径（渠道模型列表刷新 → 渠道模型接口；模型价格更新 → 价格预设/价格更新任务；程序版本更新 → 独立发布源），并说明本地运行验收需隔离哪些出站路径。
- `release-workflow-proposal.md`：保留 `prepare → test → build → publish`、固定 commit、构建前不推 tag、RC 不覆盖 latest 的方向；补一段写明 `scripts/build.sh --output-dir` 产出的 `<out>/docker/...`、`<out>/THIRD_PARTY_LICENSES.csv` 必须在 R1 映射到 Dockerfile 固定的 `build/docker`、`build/THIRD_PARTY_LICENSES.csv`，当前脚本不独立生成完整 Docker 构建上下文。

- [ ] **Step 5: 验证文档**

Run:
- 相对链接存在性检查（对改动过的 6 份 md 提取 `](xxx.md` 与 `` `path` `` 引用，逐一 `Test-Path`）
- `git diff --check`
- 人工复核：确认没有把未执行项写成已通过，没有把 skip 写成 pass，没有出现真实连接参数

Expected: 链接全部存在；无空白错误；表述与 Task 1–11 的实际日志一致。

- [ ] **Step 6: 记录验证输出** → `logs/task12-docs.log`。不 commit。

---

## 交付报告要求

收尾时（Task 12 之后）必须说明：
1. 改了哪些文件（按 A1/A2/A3 分组）。
2. 跑了什么验证及结果，逐条给出命令与结论；**明确列出 skip 的用例**（Unix 真实进程恢复）与**没有自动化测试的项**（C9 前端相位判定）。
3. 该跑没跑的为什么没跑：MySQL/PostgreSQL 真实连接（待授权）、Docker 镜像构建（无 daemon + 许可证缺失）、Android/NDK、真实发布源在线升级、actionlint。
4. 剩余风险：许可证报告仍 unavailable、`jsoniter`/事件测试稳定性、Linux/macOS 仅交叉编译未在目标 OS 执行。
5. 待我授权的动作：commit、许可证工具安装/联网、云数据库连接、CI 实改、远端发布，以及本轮保留的探针/候选目录的删除。
