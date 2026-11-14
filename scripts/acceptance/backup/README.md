# 备份真实数据库回归

此入口使用真实 `InitDB`、备份导入、缓存初始化、渠道保存及新增操作，默认期望修复后的业务操作成功。SQL 连接从 `ROAM_BACKUP_TEST_DSN` 读取，仅接受 loopback 上的专用合成测试库；SQLite 文件和 JSON 报告必须是新路径。

先用 Go 1.27.3 按当前锁文件构建。通过 `-ldflags` 设置 `main.sourceCommit` 为基线完整 SHA、`main.sourceTreeSHA256` 为实际受测文件摘要清单的 SHA-256；有未提交修改时，报告中的基线 SHA 不能代表修改后源码。

```powershell
go build -mod=readonly -trimpath -buildvcs=false -ldflags "-X main.sourceCommit=<baseline-sha> -X main.sourceTreeSHA256=<input-manifest-sha256>" -o backup-acceptance.exe ./scripts/acceptance/backup
```

生成新合成备份并验证 Windows SQLite：

```powershell
.\backup-acceptance.exe --stage fixture --db sqlite --sqlite-path source.sqlite --backup synthetic-backup.encrypted.json --report fixture-report.json --allow-isolated-write
.\backup-acceptance.exe --db sqlite --sqlite-path restored.sqlite --backup synthetic-backup.encrypted.json --report sqlite-report.json --allow-isolated-write
```

Linux 用相同入口的 amd64 交叉编译程序。`run-linux.sh <isolated-task-directory>` 要求新任务目录内已有 `backup-linux-amd64`、原 v0.1.2 行为的 `legacy-repro-linux-amd64`、合成加密备份及 `inputs.sha256`。脚本只使用固定 PG/MySQL 镜像，创建 loopback / tmpfs 测试容器并清理本轮容器；执行前核对具体资源与授权。没有旧行为程序时可分别运行 `--db postgres`、`--db mysql` 和 `--db sqlite`，但不能据此标记旧库升级通过。

PostgreSQL 覆盖密集/稀疏/空库、序列已领先和删除高 ID、两个固定连接的缓存号段、非默认且含引号的 schema/序列、identity、自定义起始值/增量/上下界/缓存参数保持、普通新增与导入并发、直接取号并发、序列权限、迁移失败重试、越界及事务回滚。三库共用控制覆盖首次保存自动补齐、九类实体真正新增、重复导入、分类归并、真实晚期 SQL 写失败与重连持久化；缺失外键引用另验证为写前拒绝。

`--stage upgrade` 在原行为程序已导入的合成 PostgreSQL 库中只执行新初始化和 CRUD，不重新导入。

B02 增加 `--stage statistics`，验证 0 点 100、1 点 200、完整 0～23 点、加密备份重放、缓存/重连/再导出、正常落库和实际 `SaveCache` 停止写入。`statistics-prepare` 只接受真实 v0.1.2 程序已创建、迁移最高号 15 的旧物理结构，添加隔离保留夹具；`statistics-upgrade` 验证迁移 17、第三列故障、MySQL 隐式提交后的重试、业务行/索引/外键/旧记录保留及重复启动。SQLite 旧库文件名固定为 `legacy-statistics.sqlite`；服务器仅接受 loopback 的专用数据库名。

`run-statistics-linux.sh <新 B02 隔离目录>` 使用同一固定镜像，另需 `op-linux.test`（当前 `internal/op` 测试包）、`v012-linux`（真实已发布程序）、`seed-v012.py` 和对应摘要清单。旧程序经回环 HTTP 确认启动后用 SIGTERM 正常停止；配置秘密只经进程环境传递，不输出应用原始日志。可控时间用例 `TestStatsBusinessKeysAndMidnight` 在三库运行，覆盖午夜换日、总统计/API Key 统计、停止重启和 24 槽位跨日复用。服务器测试需 `ROAM_STATS_TEST_ALLOW_ISOLATED_WRITE=1`，只接受回环专用 `roam_backup_b02_clock`。

B02 不猜测移动、合并或恢复已有错位统计。B03 增加 `--stage integrity`：C01～C08、完整输入图、冲突与排序规则、分类 ID 预留、统计、并发保存/导入/落库、独立连接竞争写入、提交后发布失败及旧错配的读取/保存/刷新/重启保护。只读审计前后比较业务内容、schema、迁移记录及序列状态，日志只包含合成输入摘要、实体 ID 和固定诊断。

```powershell
.\backup-acceptance.exe --stage integrity --db sqlite --sqlite-path b03-restored.sqlite --backup synthetic-backup.encrypted.json --report b03-report.json --allow-isolated-write
```

`run-integrity-linux.sh <新 B03 隔离目录>` 在独立、固定镜像、回环端口和 tmpfs 容器内运行三库 B03，以及 B01 / B02 回归和真实 v0.1.2 结构升级。输入列表与 B02 相同，runner 改为该脚本；专用目录名为 `/data/home/roam-backup-b03-YYYYMMDD-NN`。脚本拒绝收养已有容器或覆盖报告，仅清理由本次运行成功创建的容器。没有任一前置材料时不得把该项默认为通过。

最终冲突合同、HTTP 状态、兼容影响及 `db-audit` 使用方式见[备份完整性说明](../../../docs/backup-integrity.md)。这些源码/真实数据库回归不替代正式 ZIP、镜像、安装与部署验收，也不扩大正式支持范围。

B04 的 `--stage matrix --scenario C01` 至 `C08` 各使用独立新库，逐组核对数据库、关联、缓存、首次渠道保存、九类实体真正新增、统计落库和重连。`--stage cross-export` 将真实源库再导出的加密备份写入新 `--cross-backup` 文件；`--stage cross-import` 在空目标库恢复该文件，逐字段比较持久化内容后再保存、新增和重连。`run-joint-linux.sh` 在三库运行全部组合，并保留 B01～B03 的失败、并发、迁移与午夜回归。

`--stage fixture-json --cross-backup <new-path>` 只将通过固定合成数据身份校验的夹具转换为明文，供真实旧产品的 HTTP 导入验收；不接受任意用户备份。报告的 `source_commit_exact=false` 明确表示输入尚未提交，此时必须同时保留完整文件清单 SHA-256，不能把基线 HEAD 当作修复后源码的准确提交。
