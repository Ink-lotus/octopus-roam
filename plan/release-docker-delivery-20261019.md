# Docker 本地交付与补验（2026-10-19）

本轮已完成用户授权的两项工作：将补充许可证材料装入新镜像并导出 tar，补验版本、HTTPS/CA、权限重启、页面备份恢复及容器更新行为。证据和[交付目录](../build/release-local/r0-docker-delivery-20261019-03/delivery/)位于 `r0-docker-delivery-20261019-03/`。适用范围为 **WSL Containers 3.0.3.0 / Linux amd64 / SQLite / 直连测试上游**，`L6_5_strictly_passed=false`。

## 交付身份

| 项目 | 值 |
| --- | --- |
| 应用及前端版本 | `v0.1.0-local.mysqlfix-20261018-03` |
| 新镜像标签 | `octopus-roam:v0.1.0-local.mysqlfix-20261018-03-licenses.1` |
| 新镜像 ID | `sha256:1f51a67ec361cc97369ed3169ddd33600b78e621be23dbb5a12428a0ed922c96` |
| 平台 | `linux/amd64` |
| tar | `octopus-roam-linux-amd64-v0.1.0-local.mysqlfix-20261018-03-licenses.1.tar` |
| tar 大小 | 68,723,200 字节，约 65.5 MiB |
| tar SHA-256 | `b7473075f6eb88230074fd010c76eb48c7f26c32420a8f16b61b0704e1567d4b` |
| 程序 SHA-256 | `6ce55ef7215078c7b338a8787d6903f92ba40d3cc91fb0c05d1c24b826711f71` |
| 对应源码 ZIP SHA-256 | `3c397bac4a21deecb3b0d6777969679b9f160090335a9c7c5269c436f5ee4514` |

新包装只添加材料，不重新编译程序，不执行 apk，不升级或安装依赖。构建前核对原镜像 ID `sha256:ac802c5bc7ece4f1e99d292897cd1d904ab4850ac8932d0041cff780ab588721`，构建后逐层确认原有七层完全一致；新增一层提供声明。原镜像、源码、候选、数据库验收证据全部保留。

镜像内 `/app/` 的 299 个材料文件全部逐文件比对新 context：原 LICENSE、CSV、第三方声明、源码获取说明、LGPL 重新链接说明、完整许可证与前端来源材料，以及继承的 Alpine 包清单和源码边界说明。程序哈希与当前 mysqlfix 候选一致；实际 HTTP 服务的 49 个前端文件解压后与冻结静态产物一致，编译后的前端 JS 包含相同应用版本，页面没有前后端版本不一致提示。

tar 由实际 `wslc save` 导出，并用 `wslc load` 导入后核对镜像 ID。同时核验 Docker manifest、OCI layout、18 个 blob 摘要、八层内容和镜像配置摘要，确认不包含本轮 SQLite 数据、测试 CA、临时私钥、profile 或验收备份。Docker Engine/Compose 实际导入未在本机执行，不用 WSLc 结果替代这一实现的实测。

## C1–C9 结果及边界

| ID | 本轮实际证据 |
| --- | --- |
| C1 | amd64 镜像启动；ID、程序版本和哈希匹配；49 个服务端静态文件及前端版本匹配；页面版本信息无不一致提示 |
| C2 | 新 SQLite 数据目录初始化，回环端口映射，PUID/PGID 环境变量，原生页面登录和管理 API 均通过 |
| C3 | 正常停止、重启后读取原数据并将设置 11 改为 12；新建容器复用完整 `/app/data` 后设置 12、禁用渠道、分组和 API key 仍在 |
| C4 | 本轮使用默认 SQLite。外部 MySQL/PostgreSQL 容器连接未执行；原始程序两引擎各 35/35 的独立证据仍有效，但不替代本轮容器 TLS |
| C5 | 普通/流式请求、失败切换、客户端和管理端取消、日志 SSE、模型刷新通过；页面渠道、分组、模型、日志和设置均显示预期内容。未使用反向代理，代理缓冲检查不适用 |
| C6 | Go 应用实际 HTTPS 模型探测：受信任 CA + 正确 IP SAN 成功；主机名不匹配、陌生 CA、未挂载信任 CA 均 HTTP 502，fixture 证明拒绝发生在 HTTP 业务之前。测试 CA 仅以只读 `SSL_CERT_FILE` 挂载，不关闭验证；时钟/时区检查通过。重启和重建后实际进程 UID/GID 均为 65532，数据库写入成功 |
| C7 | 六个业务/fixture 容器最终退出码 0；应用日志均有 `Shutdown completed successfully`，测试端口释放 |
| C8 | API 加密导出、错误口令 HTTP 400 且设置不变、空库恢复通过；页面导出 Blob、文件选择、口令输入和导入通过，恢复后的业务实体由 API 交叉核对。另用已有 Edge 155 原生无头浏览器完成真实下载落盘，文件字节和 SHA-256 与页面 Blob 完全一致 |
| C9 | 重建容器保持完整数据挂载；GET 更新状态为 `container`、`can_update=false`，POST 返回 HTTP 409 并要求替换镜像；页面显示容器升级提示且版本卡没有自动更新按钮；请求后程序哈希不变。没有声称完成不同应用版本之间升级 |

本轮 API 业务统计观察为成功 3、失败 2、输入 9、输出 6。页面加密导出后清空口令，导入成功后清空口令及文件选择，显示导入行数。首次 Paseo 浏览器下载只确认 Blob 和下载链接调用，未冒称默认 Downloads 中已有文件；随后 `checks/native-download.json` 补齐了 Edge 实际落盘证据。

## 执行与证据

具体命令和影响见 [EXECUTION.md](../build/release-local/r0-docker-delivery-20261019-03/EXECUTION.md)。入口包括 `prepare-delivery.ps1`、`build-image.ps1`、`start-tests.ps1`、`verify-image-and-export.ps1`、`restart-and-recreate.ps1`、`stop-and-record.ps1`、`run-native-download.ps1` 和 `finalize-delivery.ps1`。本轮具名容器前缀为 `octopus-roam-r0-delivery-03-`，仅在新 `data-*` 目录使用合成 SQLite 数据。

WSLc 拒绝默认 bridge 固定 IP 后，改建本轮网络 `octopus-roam-r0-delivery-03`；fixture 使用该网络 IPv4。应用只发布回环 18086–18089。CA 不安装到宿主机或交付镜像。Edge 使用本轮新 profile/下载目录，关闭时只处理自建进程；不安装依赖或修改既有用户浏览器 profile。结束保留停止的容器、网络、数据、镜像及失败记录。

主要机器证据位于 `checks/`：`image-build.json`、`image-materials.json`、`image-notice-files.json`、`image-tar.json`、`frontend.json`、`tls.json`、`fixture.json`、`browser-ui.json`、`native-download.json`、`container-update.json`、`puid-after-restart-and-recreate.json`、两份持久化结果及正常停止结果。API 主测/空库恢复结果另在本轮根目录。

测试脚本的修正也保留：默认 bridge 固定 IP 在应用启动前被拒绝；更新接口最初错误预期 400，核对冻结源码后改为既定的 409；恢复前误用恢复后断言确认到默认值 10，真正导入后再按 11 核验；最初 WebView2 独立启动无调试页，随后改用注册表确认的既有完整 Edge。多端口共用主机名的浏览器 cookie 会相互覆盖，页面导航检查因此在已完成导入的同一实例内顺序执行。以上均未修改产品程序或降低检查要求。

## 对旧结论的修订与剩余门禁

`r0-wslc-docker-20261019-02/docker-acceptance-summary.json` 保留为历史记录，其“C1–C9 全通过”过宽：它没有容器 HTTPS/CA、权限重启、前端版本、页面备份和更新保护的完整证据。本轮补齐这些项目，C4/C6 的外部数据库、其他架构、Compose、反向代理与远端版本升级按各自范围继续保留。

应用对应源码及许可证材料已随本地测试包提供，公开网络源码入口仍待上线和真实下载验收。此次还识别出继承 Alpine 3.24.2 的 OS 包是独立材料范围；`ALPINE-PACKAGES.json` 记录准确版本、许可和 aports commit，应用源码 ZIP **不包含**这些 OS 包的完整对应源码。公开分发前须补齐/验证适用的 OS 源码与原始声明，不能以应用 CSV 或当前库存链接替代。

本轮完成的是本地测试包与所列场景，尚未达到公开发布门禁。CI、GHCR/Release、公开源码、远端下载和版本升级，以及其他平台实机继续按主计划处理；不自动 commit、切换分支、push、tag 或发布。
