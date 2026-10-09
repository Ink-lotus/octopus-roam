# Octopus Roam 更名与验证结果

日期：2026-10-08。

项目已在独立目录完成更名。验证以全新安装和当前版本数据为准，没有为更名前的测试数据增加迁移逻辑。

## 目录与改动面

- 原项目：`D:\tool\AI\octopus-solo`。
- 新项目：`D:\tool\AI\octopus-roam`。
- 原项目的 374 个受 Git 管理文件已逐一核对 SHA-256，与复制时一致。
- 新目录复制了完整、独立的 Git 历史，不是依赖原目录的 worktree，也没有共享 Git objects。
- Git 可见改动共 170 个文件：166 个原有文件修改、2 个启动脚本改名、2 个新增文件（环境变量测试与本报告）。脚本改名各计一个文件。
- 本地 `origin` 已指向 `https://github.com/Ink-lotus/octopus-roam.git`；没有执行远程仓库创建、推送、PR 或发布操作。
- 已部署的 `octopus-solo-runtime` 不在本次修改和测试范围内。测试仅使用新项目 `build/rename-checks/` 下的独立数据库和临时端口。

## 名称与关联配置

| 用途 | 新值 |
| --- | --- |
| 产品显示名 | Octopus Roam |
| 程序、前端包及 URL 标识 | `octopus-roam` |
| Go 模块 | `github.com/Ink-lotus/octopus-roam` |
| 环境变量前缀 | `OCTOPUS_ROAM_` |
| 生成的 API Key 前缀 | `sk-octopus-roam-` |
| 备份产品标识 | `octopus-roam` |
| GitHub 链接 | `https://github.com/Ink-lotus/octopus-roam` |
| GHCR 镜像 | `ghcr.io/ink-lotus/octopus-roam` |

同步调整了 Go import、价格数据生成脚本、构建产物、Docker 输入路径、Windows 启动和更新脚本、界面、PWA、浏览器缓存、通知模板、下载文件名及当前使用文档。

程序名与环境变量前缀分别定义；构建脚本通过 `go list -m` 取得模块路径。pnpm 使用新项目自己的 `.pnpm-store`，安装后的依赖目录不引用旧项目。密钥仪表盘标题在手机宽度下调整字号并允许换行。

当前运行代码和脚本已检查旧项目标识、旧 Go 模块路径及旧可执行文件/启动脚本名，没有残留。历史计划、历史验收与明确标记的旧部署记录保留当时信息；上游出处和 LICENSE 保留。

依赖版本、`go.sum` 和 `web/pnpm-lock.yaml` 均未改变。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/relay ./internal/server/handlers -count=1` | 通过 |
| 前端 ESLint、TypeScript、Vite 生产构建 | 通过 |
| 新环境变量覆盖配置文件中的端口与数据库路径 | 通过 |
| 构建脚本及容器入口 shell 语法检查 | 通过 |
| Windows 启动、自启动、双击入口和更新的隔离测试 | 通过 |
| Windows amd64 含前端可执行文件 | 构建与实际运行通过 |
| Linux amd64 Docker 输入产物 | 交叉编译通过 |
| 最终二进制两份空实例的 31 项检查 | 通过 |
| 密钥仪表盘 320、375、1280 像素宽度 | 完整显示名称，无横向溢出 |
| `git diff --check` | 通过 |

31 项运行检查覆盖嵌入页面、PWA、缓存命名、未认证访问拒绝、管理员登录、JWT issuer、版本注入、新 API Key 生成与认证、模型列表接口、加密备份标识与下载名称、错误口令拒绝、失败导入不写数据、向第二份空数据库恢复、恢复后的 Key 认证及再次导出。

测试发现并修复了两个问题：双击入口引用旧脚本文件名；手机页面截断产品名称。相关检查在修复后重新通过。

验证工具版本：Go 1.27.0、Node.js 24.18.0、pnpm 11.15.1。当前环境未执行 Docker 容器运行、Android/其他架构的完整发布矩阵或线上发布流水线。

## 产物与记录

- Windows：`build/octopus-roam.exe`。
- Linux amd64：`build/docker/linux/amd64/octopus-roam`。
- 本地构建版本：`v0.1.0-roam-local`，前后端一致。
- 各项日志及运行检查明细：`build/rename-checks/`。
- 产物 SHA-256：`build/rename-checks/artifact-hashes.json`。

测试实例在验收后正常退出；旧项目目录的删除由用户处理。后续开发应从新目录打开项目。
