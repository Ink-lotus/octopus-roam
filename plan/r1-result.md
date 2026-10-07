# R1 实现与本地验收记录

日期：2026-10-07。基线：`6fa07227ddcd928916ce98147785bcb0dffc8e5b`，分支 `test`。

状态：本轮已授权的代码、测试、文档工作已实现并通过本地验证；**R1 尚未完成对外发布**。MySQL/PostgreSQL 实例迁移与恢复验收、产品主分支切换、rc/GA 发布仍待完成。

授权依据：用户依次明确授权 [R1 执行范围](r1-execution.md)、[口令加密设计](../docs/backup-encryption-design.md)与[停用上游自更新](r1-release-preflight.md)。未新增依赖，未修改数据库结构或历史迁移、CI、`main.go` 版本、Git 分支/标签与远程仓库。

## 实现结果

| 工作 | 最终行为 |
| --- | --- |
| 备份身份 | 内层恒为 `product: octopus-solo`、`version: 1000`；上游无标识仅接受 `0`/`5`，无标识 `6`、外来标识和未知版本拒绝 |
| 口令加密 | 固定参数 scrypt 派生密钥，AES-256-GCM 加密全部内容；容器版本 `1`，随机 salt/nonce；篡改或口令错误不能写库 |
| HTTP 与界面 | 已认证 POST 导出加密 JSON；旧 GET 返回 405；multipart 导入增加口令；口令不进入 React Query mutation 缓存，操作结束清空输入 |
| 资源限制 | 明文内容 64 MiB、上传整体 96 MiB、单进程一个备份任务；超限 413、忙碌 429；不创建明文临时文件 |
| 禁用状态 | 修复渠道、渠道 Key、客户端 API Key 导入时 false 被 GORM 默认值改为 true；保留原有增量冲突策略 |
| 一致性 | 导出所有表使用同一个只读可重复读事务快照，避免导出期间刷新/删除造成不同表来自不同状态 |
| 解码与错误 | 拒绝混合/嵌套响应包裹；失效成员引用使整次事务回滚；数据库写入错误不向浏览器回显驱动详情 |
| 自更新 | 后端版本查询与更新入口直接拒绝，不下载、不替换、不重启；界面移除上游更新按钮并提示手动升级 |
| 文档 | 更新双语 README、路线图、R1 验收口径，新增变更日志、升级回滚说明、迁移审计与加密设计 |

## 文件范围

- 备份数据与实现：`internal/model/backup.go`、`internal/op/backup.go`、`internal/op/backup_crypto.go`。
- HTTP：`internal/server/handlers/setting.go`；沿用现有管理员认证。
- 备份回归：`internal/op/backup{,_crypto}_test.go`、`channel_automation_test.go`、`group_route_defaults_test.go`；`internal/server/handlers/backup_test.go`、`runtime_test.go`；`internal/webhook/service_test.go`。
- 自更新：`internal/update/{update,core,independent_test}.go`。
- 前端：`web/src/api/setting.ts`、设置模块的 `Backup.tsx`/`Info.tsx`、三份 locale。
- 文档：两份 README、`CHANGELOG.md`、`docs/{backup-encryption-design,upgrade-r1}.md`、本轮 `plan/r1-*.md`、路线图与 R1 里程碑。

## 验证证据

1. 身份测试先因缺少字段/校验函数失败；实现后矩阵通过。常量测试锁定产品、`1000` 与上游 `{0,5}`，防止后续同步扩大白名单。
2. 禁用状态回归在修复前显示三类记录 `enabled=true`；修复后新增 false 保留，重复导入时渠道/API Key 保留目标配置、渠道 Key 可按备份覆盖 true/false。
3. 加密测试覆盖序列化往返、随机性、错误口令（包含首尾空格语义）、salt/nonce/密文/header 篡改、未知算法/版本、截断及大小/口令边界。
4. HTTP 测试覆盖裸 JSON、响应包裹、multipart、认证、旧 GET 禁用、错误口令无写入、正确恢复、超限/并发与失败后额度释放。
5. 新配置恢复测试验证禁用渠道和凭据、显式退出自动跟随、并发上限 3、协议限制、严格匹配、指数路由策略、成员顺序及 Webhook 认证头。
6. 导出快照测试在读完渠道表后用另一连接删除渠道；修复前导出丢失关联 Key，修复后仍得到同一时刻的完整快照。
7. 失效分组成员导入失败时检查没有残留的新分组。SQLite 旧表升级及二次 AutoMigrate 测试继续通过。

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过；最后的导出快照修复后又完成相关包复验 |
| `go test ./internal/db/... ./internal/op ./internal/server/handlers ./internal/webhook -count=1` | 通过；快照修复后再次通过 op/handlers/webhook |
| `go test -race ./internal/relay ./internal/op ./internal/task ./internal/server/handlers -count=1` | 通过；后续新增自更新/快照相关测试另做定向竞态复验 |
| `go test -race ./internal/op ./internal/update -count=1` | 通过 |
| `go test -race ./internal/op -run 'TestBackup|TestEncrypted' -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `pnpm --dir web lint` | 通过 |
| `pnpm --dir web build --outDir "$env:TEMP/octopus-r1-final-build" --emptyOutDir false` | 通过，含 TypeScript 检查；仅构建插件耗时提示 |
| `go build -trimpath -tags=jsoniter` | 通过；在隔离打包目录嵌入本次前端产物 |
| locale 叶子键一致性 | 通过，三份各 516 个键 |
| `git diff --check` | 通过 |

Go 使用工作区构建缓存及已有模块缓存。首次发布构建因系统缓存不可写且缺少 jsoniter，改用工作区模块缓存，从已有本地文件代理读取锁定模块；没有联网安装或改变依赖。临时构建产物和合成验收数据位于忽略目录及系统临时目录。

## 浏览器与实际程序

用本轮 TypeScript/Vite 生产产物嵌入 Go 程序，以真实 `start --config` 启动隔离 SQLite 实例；测试数据仅含合成 Key、一个禁用渠道、显式退出跟随和并发上限 3。

- 实际页面导出返回 200，下载 Blob 为 `encrypted-backup`，不包含合成 Key 的明文。
- 错误口令导入返回 400；正确口令导入返回 200，展示各表导入结果。
- 导出/导入完成后口令清空；恢复成功后文件输入清空。localStorage/sessionStorage 和浏览器日志不含测试口令。
- 两次导出口令不一致时不发请求，显示明确提示并清空口令；旧 `5` 号明文文件可从页面导入。
- 三个口令输入框具有对应 label，沿用原生可聚焦输入与按钮。本次依据 DOM 和网络结果验收，不包含像素级截图或完整屏幕阅读器验收。
- 使用后续构建再次打开同一隔离数据库，渠道仍禁用，退出跟随和并发上限 3 保留。
- 自更新入口返回明确停用错误，页面显示手动升级说明；本轮没有执行实际程序替换或远程发布。
- 最后一次快照修复已重新编译进含前端的 `octopus-r1-verified.exe`，真实进程的加密导出/导入再次返回 200。隔离验收进程和浏览器标签已关闭。

## 兼容与发布限制

- 新文件不能由旧构建导入；无标识 `6` 的重新导出需要原数据库。只有旧 JSON 时不提供自动转换，不能手改版本号绕过校验。
- 备份不含管理登录账号、迁移记录、日志或运行态；增量导入不替代完整数据库回滚。加密保护下载文件，不对业务数据库做静态加密。
- MySQL/PostgreSQL 未发现本地工具、服务或已配置测试实例；仍缺实际升级/恢复证据。SQLite 通过不代表另外两种数据库已验收。
- 自更新已停用，独立发布源、产物校验、默认分支与镜像地址需在发布阶段确定。继承的 compose 和构建横幅仍含上游默认信息，README 已说明适用范围，不能把上游镜像当作 Solo 发布。
- 生产数据库备份、`main` 分支、默认分支切换、版本修改、commit/push、rc/GA 均未执行；后续按 [发布前核对](r1-release-preflight.md)继续，当前不具备宣称 R1 发布完成的条件。
