# v0.1.0 首发支持范围

`v0.1.0` 采用工作包 8 确认的方案 A。正式支持按部署组合限定，完整交付保持 11 个程序 ZIP、四架构 GHCR 镜像及对应材料。公开 RC 的实际升级与恢复已通过。

| 部署组合 | 首发范围 | 验收边界 |
| --- | --- | --- |
| Windows amd64 原生程序 + SQLite | 正式支持 | 本轮 Windows 宿主版本为 10.0.26300；公开包安装、自更新、重启、持久化及恢复以工作包 8 结果为准 |
| Windows / WSL Containers 3.0.3.0 / Linux amd64 GHCR 镜像 + SQLite | 正式支持 | 固定 amd64 标签或已核对 digest；实际数据挂载、换镜像、重启和备份恢复以工作包 8 结果为准 |
| Linux amd64 原生程序 | 实验性 | 镜像内程序运行证据保留；原生进程替换、重启及失败恢复未完成验收 |
| Linux arm64 / arm/v7 / 386 原生及镜像 | 实验性 | 交叉构建、材料和镜像 manifest 已验证，缺对应设备的运行及升级/恢复验收 |
| macOS amd64 / arm64；Android 四 ABI | 实验性 | 保留 ZIP、NDK 构建及对应源码，缺产品原生运行及升级/恢复验收 |
| Docker Engine / Docker Compose；其他 WSLc 版本 | 参考部署 | RC 曾接受 WSLc 等价门禁；正式支持仅覆盖上表明确的 WSLc 组合 |
| MySQL / PostgreSQL；容器外部数据库 | 首发范围外 | 已有原生候选两库的 TLS、迁移及备份恢复记录保留；未完成这些组合的公开版本间升级验收 |
| 反向代理、入站 TLS 终止及其他部署组合 | 首发范围外 | 需补认证、流式请求、超时、升级缓存及对应源码入口的部署验收 |

上述正式支持组合已通过公开 RC 升级与恢复验收。正式产物的版本、完整 SHA、程序哈希和镜像身份以同版 `BUILD.json`、`SHA256SUMS`、`IMAGE-MANIFEST.json` 为准；每次发布仍须完成全部构建检查及公开产物首次运行冒烟。

## 安装、升级与恢复

Windows 使用 [v0.1.0 Release](https://github.com/Ink-lotus/octopus-roam/releases/tag/v0.1.0) 的 `octopus-roam-windows-amd64.zip`，校验 `SHA256SUMS` 后完整解压并保留菜单支持文件。WSLc 使用 `ghcr.io/ink-lotus/octopus-roam:v0.1.0-amd64`，保留 `/app/data` 挂载。稳定版本更新 `latest`；固定版本标签或已核对的 digest 用于可复核部署。RC 不更新稳定标签。

升级前导出加密备份，正常停止后保留完整数据目录及旧程序或镜像身份；恢复时使用升级前程序/镜像和升级前完整数据副本。JSON 导入采用增量语义。具体操作见[升级与备份说明](upgrade-r1.md)。

## 材料与证据

实验性产物仍执行同样的版本、完整 SHA、实际 Go 编译版本、哈希、对应源码及原始声明检查。新正式版材料须由最终冻结源码重新生成，并在 `INSTALL.md`、源码包 README 和 Release Notes 使用本页范围；已公开 RC 资产保持原样。

- [rc.2 发布与公开回读结果](../plan/release-step-08-result-20261029-01.md)
- [工作包 8 升级、恢复及收口结果](../plan/release-step-08-result-20261029-02.md)
- [发布 CI 合同](release-ci.md)、[材料入口](release-materials.md)

增加正式支持组合时，应明确宿主、运行时、架构及数据库版本，补齐其安装、运行、升级、持久化与恢复证据后更新本页。
