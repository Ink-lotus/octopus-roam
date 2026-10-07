# R1 口令加密备份设计

日期：2026-10-07。状态：用户已明确授权按本设计实现，已在工作区落地。本文补充 [R1 执行方案](../plan/r1-execution.md)，不替代备份内容的产品身份校验；验证见 [R1 结果](../plan/r1-result.md)。

## 用户行为

设置页导出时输入备份口令并再次确认，下载一个加密 JSON 文件。恢复时选择文件、输入该文件的口令，服务端解密并完成已有的增量导入。口令由用户保管，服务器不保存，遗失后无法恢复；页面在成功、失败或离开时清空本次口令，不写入 localStorage、URL 或日志。

导出不再提供明文下载。升级前已有的上游 `0`/`5` JSON 仍可直接导入，无标识 `6` 继续拒绝；程序内部的 `octopus-solo` / `1000` JSON 可导入，但 UI 正常导出只生成加密文件。已有数据库在新程序中直接打开后即可重新导出，不要求先导入旧 `6` 号文件。

## 格式与密码学

内层是现有完整 `DBDump`：`product = "octopus-solo"`、`version = 1000`，渠道凭据、API Key、Webhook 认证头及带认证信息的 URL 一并加密。统计与设置同样位于密文内。管理账号、数据库迁移记录及刷新诊断沿用原有备份边界，不加入本格式。

外层 JSON 只有以下字段，不包含渠道名、模型名、凭据或明文摘要：

| 字段 | 值 / 编码 |
| --- | --- |
| `product` | `octopus-solo` |
| `format` | `encrypted-backup` |
| `version` | `1`（加密容器版本，与内层 `1000` 分开） |
| `kdf` | `scrypt-n32768-r8-p1` |
| `cipher` | `aes-256-gcm` |
| `salt` | 随机 16 字节，标准 Base64 |
| `nonce` | 随机 12 字节，标准 Base64 |
| `ciphertext` | 密文与 GCM 16 字节认证标签，标准 Base64 |

使用 Go 标准库 `crypto/rand`、`crypto/aes`、`crypto/cipher` 和已有依赖 `golang.org/x/crypto/scrypt`，不新增或升级依赖。scrypt 固定 `N=32768, r=8, p=1`，派生 32 字节密钥，单次内存约 32 MiB。导入不采纳文件提供的任意成本参数，未知 `kdf`/`cipher`/容器版本直接拒绝。

GCM additional data 固定为 UTF-8 字节串 `octopus-solo\nencrypted-backup\n1\nscrypt-n32768-r8-p1\naes-256-gcm`。所有 header 均精确匹配后才解密；salt 参与密钥派生，nonce 参与 GCM 校验。每次导出独立生成 salt 和 nonce；相同口令及内容也得到不同密文。禁止复用密钥/nonce、固定随机数、忽略认证失败或回退明文。

口令按 UTF-8 原始字节使用，不 trim、不做 Unicode 归一化。导出口令要求至少 12 个 Unicode 字符，最多 1024 字节；导入只限制非空且不超过 1024 字节，兼顾其他兼容工具产生的旧文件。前端校验用于交互，服务端重复校验。

## API 与交互

| 操作 | 契约 |
| --- | --- |
| 导出 | `POST /api/v1/setting/export`，JSON `{ "password": "<用户输入>" }`；沿用管理员认证及 RequireJSON |
| 原明文导出 | `GET /api/v1/setting/export` 返回 `405`，不生成任何备份内容，并提示使用口令导出 |
| 导入 | 保留 `POST /api/v1/setting/import`；multipart 包含 `file`，加密文件额外包含 `password` 字段 |
| 旧脚本导入 | 原有裸 JSON 导入继续接受已支持的明文内容；加密容器通过 multipart 上传，避免把口令放入查询参数或自定义日志头 |
| 成功下载 | `application/json`，文件名 `octopus-solo-backup-<UTC时间>.encrypted.json`，`Cache-Control: no-store` |
| 成功导入 | 保留 `{code,message,data:{rows_affected}}` 响应，缓存重载与路由重置沿用现有成功路径 |

前端分别设置“导出口令”“再次输入口令”“导入口令”及可访问的 label，输入框 `type=password`、合适的 autocomplete。提示备份口令不是登录密码且无法找回；匹配失败、口令太短时在提交前显示可理解的错误。旧文件导入可留空口令。三份 locale 同步，不在 UI 展示 KDF 参数等实现细节。

口令不作为 React Query mutation 的 variables 保存：组件将其交给专用请求函数，并在请求结束后清空本地 state；mutation 缓存只保留成功结果或不含敏感内容的错误。不得通过通用错误打印回传整个请求。

## 资源与错误边界

- 只允许 JSON object；加密容器严格校验字段、算法、版本、Base64 和解码后的长度。容器与明文/响应包裹混合时拒绝，不能解密失败后尝试旧格式。
- 内层序列化 JSON 上限 64 MiB；导入请求整体上限 96 MiB（覆盖 Base64 与 multipart 开销），超过返回 `413`。解密前检查密文长度，避免按不可信参数分配任意内存。导出按有上限的写入器编码内层；达到上限后停止，不下载截断备份。DB 查询加载数据仍有内存成本，极大实例使用原生数据库备份。
- 进程内同时最多 1 个备份导出/导入工作；忙时立即返回 `429` 和可重试说明，避免多个 KDF/全库导出与 Relay 争夺内存。每次在 defer 中释放，不使用无界排队。
- 参数无效返回 `400`；口令错误或认证失败统一返回“口令错误或备份已损坏”，不区分认证失败细节。意外的内部错误返回通用 `500`，记录时不带口令、请求体、备份内容或完整驱动错误。
- 解密、内层产品/版本/数据验证全部完成后才进入数据库事务；失败不写数据库、不刷新缓存、不重置路由。取消请求后不开始新事务，已进入的事务继承请求 context。
- 不创建明文临时文件，不持久保存口令或派生密钥。能够控制的密钥 byte slice 使用后清零；Go 运行时和浏览器无法承诺内存完全擦除。传输依赖部署端 HTTPS，加密文件本身提供离线保密与完整性保护。

## 实现范围与命令

在已授权的身份校验、导入状态修复及文档工作完成后，用 `apply_patch` 增加/局部修改：

- `internal/op/backup_crypto.go`、`internal/op/backup_crypto_test.go`：容器、加解密、固定算法参数、大小限制与格式拒绝矩阵。
- `internal/server/handlers/setting.go`、`internal/server/handlers/backup_test.go`：POST 导出、GET 禁用、受限上传、并发门、错误映射、加密/旧格式导入与认证测试。
- `web/src/api/setting.ts`、`web/src/components/modules/setting/Backup.tsx`、三份 locale：口令输入、下载/上传及敏感状态清理。
- 两份 README、升级文档和 R1 验收记录：新操作方式、脚本 API 变化、限制、备份口令保管与回滚说明。

执行 `gofmt -w` 格式化变更的 Go 文件，运行相关 Go 测试、完整 Go 回归、竞态检查、vet、前端 lint/build 和 `git diff --check`。此扩展涉及 HTTP 方法和前端交互，属于 R1 对外 `v0.1.0` 的兼容性说明；不增加数据库迁移或依赖，不修改 CI、生产配置或发布版本。

## 验收矩阵

1. 完整合成数据加密、序列化、解密、导入、再次导出成功；disabled、空模型、路由设置和逐 Key 凭据均保留。
2. 下载文件不包含合成 Key、认证头或 URL 凭据的明文；同内容同口令两次导出密文不同。
3. 错误口令、篡改 salt/nonce/ciphertext/header、截断、未知格式、无标识 `6` 均失败；数据库行数/值、缓存和路由状态不变。
4. API 未登录不能导出/导入；GET 导出无法下载明文；空口令、超长口令、超大文件返回约定状态。
5. 并发只接纳一个备份任务；失败与取消后可再次执行；测试小输入即可验证长度/忙碌边界，避免分配巨量内存。
6. 浏览器验证口令确认、导出、正确/错误口令导入、旧格式导入、错误提示、字段清空与键盘操作；日志和 mutation 缓存不保留口令。

本节 API/前端扩展范围已获得用户明确授权。分支和发布操作仍按 R1 的发布授权边界处理。
