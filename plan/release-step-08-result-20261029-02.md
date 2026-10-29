# 工作包 8：公开升级、恢复与支持范围收口结果

日期：2026-10-29。工作目录：`D:\tool\AI\octopus-roam`。

## 结论

**工作包 8 / R4 已完成，可进入工作包 9 的正式版准备。** 本报告接续[rc.2 发布与公开回读结果](release-step-08-result-20261029-01.md)，补齐两个公开 RC 之间的 Windows 实际升级、重启、数据保留和失败恢复，以及 WSLc/Linux amd64 的镜像升级、持久化、备份恢复与旧镜像回退。

用户已明确选择方案 A，并分别授权本轮隔离验收和九文件文档补丁。正式支持范围已在本地文档落实为 **Windows amd64 原生程序 + SQLite**、**Windows / WSL Containers 3.0.3.0 / Linux amd64 镜像 + SQLite**。其他构建继续交付并标为实验性，完整 11 ZIP / 四架构镜像合同保留，见[支持矩阵](../docs/support-v0.1.0.md)。

正式 `v0.1.0` 尚未发布。本轮没有 commit、push、tag、Release、workflow dispatch、镜像推送或其他远端写入；本地 HEAD 保持 `1a8729a11b122888a683fd1bd9491ce38ceb4e18`。文档改动尚未提交或同步到远端。

## 输入与隔离范围

- rc.1：`v0.1.0-rc.1`，提交 `5ec4dc5905149db03159d9d02c8f76646300d477`。Windows 公开 ZIP SHA-256：`19739b687b0dd28a9dabf62d2b184f78c431027d0e4e319d6a10375fe4b4678f`。
- rc.2：`v0.1.0-rc.2`，提交 `1a8729a11b122888a683fd1bd9491ce38ceb4e18`。Windows 公开 ZIP SHA-256：`44f270007e2adb407d030484d36c3a073bad745a0bda48504d0a7bbe932ea874`。
- 使用先前匿名下载、在本轮重新核对公开 `SHA256SUMS` 的原始 ZIP；真实更新由原始 rc.1 程序通过生产发布源重新下载 rc.2 并执行校验与替换。程序及 helper 未修改或重新编译。
- 容器直接拉取两个公开 `-amd64` 标签。rc.1 image ID 为 `sha256:92da6493da0b0de6dba98fa17e8b7829ab4df9ac0de6b06642d1eafa5e45201a`；rc.2 为 `sha256:537de02c21fc1df85645d9ca73c601a446b8d5453b51d9a969735a88cfce05a1`，均匹配封存身份。
- 所有新实例、合成 SQLite、备份、日志和失败现场位于 [step-08-upgrade-20261029-02](../build/release-local/step-08-upgrade-20261029-02/)，监听仅使用 `127.0.0.1:18280–18285`。关闭后台价格任务，合成渠道禁用且指向回环地址；没有连接业务数据库、供应商或 Webhook。
- 本轮宿主为 Windows `10.0.26300`；WSLc 为 `3.0.3.0`。需要的宿主权限仅用于已授权的隔离进程、容器操作和只读检查；未修改系统配置或安装依赖。

## 验收结果

| 项目 | 结果与证据 |
| --- | --- |
| 公开版本查询与目标确认 | rc.1 实际查询得到 `available / v0.1.0-rc.2`；空目标 HTTP 400、旧目标 HTTP 409，程序文件保持 rc.1。见 [attempt-04 记录](../build/release-local/step-08-upgrade-20261029-02/windows/attempt-04/summary.json) 中已完成的前置检查。 |
| 真实 Windows 升级 | 首个页面请求返回 HTTP 502，原程序与数据保留；重新确认同一目标后的实际 API 重试返回 `update_pending / v0.1.0-rc.2`，随后运行 rc.2 并正常停止。见 [公开更新结果](../build/release-local/step-08-upgrade-20261029-02/windows/update-diagnosis-01.json)。 |
| 安装文件与旧程序备份 | 更新后的 exe SHA-256 为 `39e65e678e3ccf93fdde725a9239fd691a2b00fff17f71843f1e8b5f4eeea221`；六个程序/菜单文件与公开 rc.2 一致，helper 保留的六个更新前文件与公开 rc.1 一致。配置和自定义数据文件未变。 |
| Windows 持久化与加密恢复 | 更新后的正常重启、当前 rc.2 加密导出、空 SQLite 的错误口令拒绝、正确恢复及恢复后正常重启通过。设置、渠道、分组和禁用状态保留。见 [completion-01 中已完成的检查](../build/release-local/step-08-upgrade-20261029-02/windows/completion-01/summary.json)；其后续故障驱动失败由下项独立收口。 |
| Windows 原始 helper 自动恢复 | 新隔离 rc.1 经真实公开 API 接受 rc.2 更新；仅对配置取得临时读取锁，使新进程启动失败。原始 helper 恢复 rc.1、写入 `update_failed`，释放锁后旧版本自动启动；数据和六个原始文件一致，再次正常重启通过。保留的 stage 程序与公开 rc.2 一致。见 [最终恢复结果](../build/release-local/step-08-upgrade-20261029-02/windows/recovery-final-01/summary.json)。 |
| 实际浏览器与缓存 | 页面曾以准确 rc.2 目标提交更新，并显示失败终态。成功升级后，旧 rc.1 前端与 rc.2 后端的版本不匹配提示正确；点击产品的“强制刷新”，项目缓存删除、根 Service Worker 注销，其他缓存保留。重新加载的前后端均为 rc.2，设置资源为 `/assets/setting-C8UNm5sY.js`。见 [浏览器证据](../build/release-local/step-08-upgrade-20261029-02/windows/completion-01/browser-complete.json)。成功更新回执来自后续 API 重试，不将首个页面失败写成成功。 |
| WSLc 镜像升级与持久化 | 公开 rc.1 创建合成数据和备份，正常停止并复制完整数据目录；新 rc.2 容器沿用原挂载，版本和数据正确，正常停止/重启通过。镜像内程序 SHA-256 为 `6859e9683c549bb86a62d99285db9c18a04b0014d69a1f27b5618074bc17947c`，与公开程序一致。 |
| WSLc 备份与失败恢复 | rc.2 加密备份在空数据目录中先拒绝错误口令，再正确恢复并通过重启。独立故障副本使用不可绑定地址，观察监听错误和 HTTP 不可用；正常停止后以 rc.1 镜像及升级前完整副本恢复通过。见 [容器结果](../build/release-local/step-08-upgrade-20261029-02/containers/summary.json)。 |
| 数据逐项对照 | 六组升级、加密恢复和回退对照均完成 SQLite `quick_check` 与八张业务表、每组 20 行核对。直接升级和回退的全部行一致；加密导入按已有合同刷新隐私规则并发 `revision`，规则内容、其他设置、渠道凭据、API Key 和关联记录一致。见 [最终数据审计](../build/release-local/step-08-upgrade-20261029-02/persistence-audit-final.json)；合同实现为 `internal/op/privacy_backup.go`。 |
| 通道与容器保护 | rc.1 接受更高 RC，更新后 rc.2 查询为 `latest`；容器查询为 `container`，容器内更新请求被 HTTP 409 拒绝。稳定/RC 比较规则沿用已通过且未修改的实现；RC 未更新稳定通道的公开回读证据见前一份报告。 |

容器故障场景确认了“进程仍存活但 HTTP 不可用”的状态；部署确认继续以页面/API、运行版本和数据检查为准，不能仅凭容器 Running 或退出码判断服务健康。

## 文档与构建边界

已按用户批准的 [scope-a.patch](../build/release-local/step-08-upgrade-20261029-02/support-review-02/scope-a.patch) 应用九文件改动：双语 README、升级说明、CI/材料说明、Release Notes、两份发布计划，以及新增 `docs/support-v0.1.0.md`。补丁应用前核对原文件哈希，保留会话开始时已有的计划差异；没有改动 workflow、构建矩阵、产品代码、依赖或已公开产物。

README 修正了“候选尚未发布”的旧说明，优先提供已验收 WSLc 组合的安装指引。Linux 原生、其他架构、macOS、Android、Engine/Compose、MySQL/PostgreSQL 及其他范围外部署分别标明边界。正式版的 `INSTALL.md`、源码包 README 和材料必须在工作包 9 从最终冻结源码重新生成并绑定正式版本/SHA。

本轮没有重复下载 rc.2 全部 27 个附件或完整 2.76 GB Actions artifact。前一份报告的 27 个附件 digest 对照、8 个关键附件实际下载、源码内部校验、11 目标/四镜像构建及 publish job 的完整 artifact 重校验继续作为发布证据；本轮增加所保留支持组合的真实运行证据。

## 保留的失败与修正

- 首轮启动驱动因后台子进程继承输出管道而超时；进程正常停止后，驱动改用独立日志文件。
- 两次前端断言未适配相对资产路径和延迟加载的设置页脚本；读取实际产物后修正驱动，保留原失败目录。
- 首个实际页面更新返回 HTTP 502；没有更改程序、代理或 TLS 校验，后续对相同公开目标的请求成功。原失败及正常停止记录保留。
- `completion-01` 的故障观察在替换窗口持续读取目标 exe，未得到恢复终态。未经修改的公开 helper 在 [独立捕获日志的复测](../build/release-local/step-08-upgrade-20261029-02/windows/helper-probe-01/result.json) 中正常回滚；随后停止在替换过程中读取目标文件，改在完成后校验保留 stage，完整公开 API 的 [recovery-final-01](../build/release-local/step-08-upgrade-20261029-02/windows/recovery-final-01/summary.json) 通过。产品代码未因此修改。
- 最初逐字节数据对照标出了两次加密导入的隐私规则 `revision` 差异；源码和已有测试明确要求导入生成新 revision。最终审计核对该字段确已刷新，同时核对所有规则内容和其他数据一致，原严格对照记录保留。

所有本轮产品实例、辅助进程和五个新容器已结束或正常停止；镜像、停止的容器、数据、备份和失败证据保留。最终检查索引见 [final-result.json](../build/release-local/step-08-upgrade-20261029-02/final-result.json)。

## 工作包 9 入口

准备正式 `v0.1.0` 的准确版本与材料文案补丁，将获准文档纳入新的冻结提交，完成该 SHA 的规定 CI、完整材料和构建检查；审核实际产物并取得对应发布授权后，才执行正式 Release、版本镜像及 `latest` 发布，并补正式产物回读和首次启动冒烟。工作包 8 的通过不替代这些操作的授权与最终产物验证。
