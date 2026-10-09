# 本地部署验收

使用生产构建在仓库外运行，建议源码目录为 `D:\tool\AI\octopus-roam`，运行目录为同级的 `D:\tool\AI\octopus-roam-runtime`。运行目录无需 Git 仓库，也无需安装 Go、Node.js 或 pnpm。

以下是目录安排；本机部署结果见文末记录。

```text
D:\tool\AI\octopus-roam-runtime\
  octopus-roam.exe       # 嵌入前端的 Windows 生产构建
  octopus-roam.cmd       # 双击打开管理菜单
  windows\              # 菜单和启停、自启支持文件
    octopus-roam-menu.ps1
    octopus-roam-start.ps1
    octopus-roam-autostart.ps1
    send-stop-signal.ps1
  data\
    config.json     # 本地实例配置
    data.db         # SQLite 数据库及运行中可能产生的 WAL/SHM 文件
  logs\             # 启动时重定向的进程输出
  backups\          # 手动保存的加密备份
```

在源码目录构建前端和后端，将最终可执行文件复制到运行目录。构建输出可以写入临时目录，前端产物须在编译后端时嵌入；部署后不依赖源码目录或前端开发服务器。复制新构建前应停止当前实例，避免覆盖正在运行的程序。

## 从当前源码更新本机运行目录

在源码目录运行以下命令，仅构建包含前端的新程序，当前服务继续运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\windows\update-local.ps1
```

脚本使用当前工作区的代码（包含未提交改动），在 `build/local-update-时间戳/` 生成 `octopus-roam.exe`、`build-info.json`、`octopus-roam.cmd` 和 `windows` 支持文件。前后端使用相同版本号；构建不修改运行目录。现有 Go、Node.js 和 `web/node_modules` 必须可用。

退出使用此服务的 AI 终端，等待请求结束后，在普通 PowerShell 中执行下面的命令，将示例目录换成构建输出中的 `Prepared update` 路径：

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\windows\update-local.ps1 `
    -Apply -PreparedDirectory 'D:\tool\AI\octopus-roam\build\local-update-时间戳'
```

也可以退出终端后直接加 `-Apply`，让脚本依次完成构建与更新。默认运行目录为 `D:\tool\AI\octopus-roam-runtime`，可用 `-RuntimeDirectory` 指定另一份部署。

更新会验证构建文件的 SHA-256，向运行目录对应的进程发送正常退出信号并等待退出，将完整 `data` 目录、旧程序、旧构建信息及已有菜单文件备份到 `backups/before-update-时间戳/`，然后替换程序、更新菜单与构建信息并启动服务。新菜单安装完成后，会清理旧的两个自启 `.cmd` 入口，原文件保存在更新备份中。渠道、密钥、分组和配置继续使用运行目录的原有数据。旧目录中平铺的 PowerShell 启动脚本仍兼容，日常操作使用新的 `octopus-roam.cmd`。

后台启动器继承的 Windows 标志可能忽略 Ctrl+C，因此停止辅助脚本使用 Ctrl+Break；Go 将其作为 SIGINT 交给现有退出流程。辅助脚本只接受独立控制台（目标程序、辅助进程以及最多一个 cmd 启动器）。若目标与其他应用共享控制台，会停止更新并提示在原终端按 Ctrl+C；不会强制结束进程。构建失败或进程未能退出时，不替换程序。

更新完成后重新打开管理页面；若仍显示旧界面或版本不一致，使用设置页的强制刷新。脚本测试命令为 `powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-update-local.ps1`，仅操作 `build/` 下的模拟部署。

## 首次配置与启动

Windows 用户将整个压缩包解压到固定目录后，双击 `octopus-roam.cmd` 并选择 `1` 或 `2` 即可启动。菜单会自动创建 `data`、`logs` 和 `backups` 目录，并在配置不存在时写入以下 `data/config.json`；已有配置不会被覆盖：

```json
{
  "server": { "host": "127.0.0.1", "port": 8080 },
  "database": { "type": "sqlite", "path": "data/data.db" },
  "log": { "level": "info" }
}
```

启动器会检查配置中的监听地址和端口，包括 `OCTOPUS_ROAM_SERVER_HOST`、`OCTOPUS_ROAM_SERVER_PORT` 环境变量的覆盖值。若 8080 已占用，先关闭占用该端口的应用，或修改本实例配置中的端口并使用对应访问地址。启动失败时，选项 `2` 不会创建自启项。

也可从 PowerShell 前台启动：

```powershell
Set-Location -LiteralPath 'D:\tool\AI\octopus-roam-runtime'
.\octopus-roam.exe start --config .\data\config.json
```

工作目录必须是运行目录。只从仓库里执行另一个目录的 `octopus-roam.exe`，或仅传入绝对配置路径，都不会改变相对数据库路径的解析位置。后台启动同样应设置 `-WorkingDirectory`，使用隐藏窗口，并将标准输出和错误分别重定向到 `logs` 下的文件。

浏览器访问 `http://127.0.0.1:8080`。首次登录方式见 [README](../README_zh.md#-默认账户)。浏览器下载的备份通常进入系统下载目录，需要主动保存到 `backups`；该文件夹不会由程序自动填充。

## Windows 双击管理菜单

Windows 发布压缩包根目录提供一个 `octopus-roam.cmd` 操作入口，支持文件位于 `windows` 文件夹。完整解压后双击入口，输入序号并按 Enter：

| 选项 | 操作 |
| --- | --- |
| `1` | 启动服务；已运行时跳过重复启动。 |
| `2` | 启动服务，成功后添加当前用户的登录自启。 |
| `3` | 正常关闭本目录的服务，保留当前自启设置。 |
| `4` | 关闭自启，并正常关闭本目录的服务。 |
| `0` | 退出菜单。 |

菜单显示服务及自启状态，启动成功后显示管理页面地址。关闭菜单窗口后，服务会继续在后台运行；需要停止服务时选择 `3` 或 `4`。停止操作只匹配当前目录的可执行文件，并等待程序保存数据、正常退出。等待超过 60 秒会显示错误，保留进程供检查，不强制结束；若程序在与其他应用共用的终端中启动，按提示回到原终端使用 `Ctrl+C`。

这是个人电脑的登录自启，无需管理员权限；登录前不会启动。启用时只在当前用户的 Startup 文件夹创建 `Octopus Roam.lnk`，关闭时只移除指向本运行目录的该快捷方式。重复操作可安全执行；发现同名快捷方式属于其他部署时会报错，避免覆盖或删除其他部署的启动项。Windows 任务管理器的“启动应用”中也应允许此启动项；如果手动将其禁用，应在同处恢复启用。

启动器固定工作目录、隐藏程序窗口、将输出写入 `logs/startup-*.out.log` 和 `logs/startup-*.err.log`；登录自启失败记录在 `logs/autostart-errors.log`。关闭服务的结果写入 `logs/shutdown-*.json`。同一目录中的启停、组合选项和更新通过同一个锁串行处理。

脚本仅为自身 PowerShell 进程使用 `RemoteSigned`，不修改系统执行策略，组织策略仍然生效。若下载的脚本因来源标记或组织策略被阻止，应按 Windows 提示和本机策略处理。

移动或清理运行目录前选择 `4`，在新目录需要自启时再选择 `2`。菜单管理普通后台进程，不安装 Windows 服务，也不提供崩溃重启。Docker 镜像使用容器工具管理启停和重启策略，不包含 Windows 菜单。

维护脚本时，在仓库中执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-menu.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-autostart.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-update-local.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-update-local.ps1 -Layout Flat
```

这些检查使用隔离目录、测试自启目录和合成程序，覆盖四个菜单选项、首次配置、端口冲突、特殊字符路径、正常关闭、进程归属、自启兼容及更新备份，不操作当前用户的实际启动项；测试脚本无需复制到部署目录。

## 验收范围

- 全新安装、登录、渠道与分组配置、模型刷新和客户端 API Key 管理。
- 实际模型的普通请求、流式响应、取消、失败切换，以及日志和统计。
- 自动刷新与渠道并发限制。
- 当前版本加密备份导出、错误口令拒绝、在另一个空实例恢复。
- 正常停止并重启后，配置、禁用状态和业务数据保留。
- 设置页仓库和手动升级链接指向 `Ink-lotus/octopus-roam`；版本检查与一键更新仍停用。

旧版本升级兼容不作为首次独立发布的门槛。如首版声明支持 MySQL/PostgreSQL，其全新安装、读写及当前版本备份恢复仍需对应实例验证。

## 停止与清理

前台运行时使用 `Ctrl+C` 正常停止，等待程序退出和缓存保存。后台实例应通过其启动管理方式正常关闭。停止后才能移动数据库或清理目录；需要保留数据时复制完整 `data` 目录及备份文件。

测试结束后，确认进程已退出且无需保留的数据已处理，再清理这个独立运行目录即可。源码仓库中的构建缓存和浏览器存储分别管理，不属于运行目录。

## 本机部署记录（2026-10-07）

> 以下是更名前 Octopus Solo 的历史部署记录；Octopus Roam 的更名与测试不修改该运行目录。

已部署至 `D:\tool\AI\octopus-solo-runtime`，使用独立 SQLite 数据库，监听 `http://127.0.0.1:8080`。本地构建版本为 `v0.1.0-local`，基于 `b4af012` 加当前工作区的仓库地址及启动脚本改动；不是已发布的 RC 或正式版。前后端使用同一版本标记。

构建在系统临时目录的源码副本中完成，前端已经嵌入 `octopus.exe`。运行目录包含程序、四个启动脚本、`LICENSE` 和 `build-info.json`；后者记录构建时间、源码基线和程序 SHA-256。

本次部署验证通过：首页加载、未登录接口返回 401、登录成功、渠道及分组查询、当前版本显示、仓库与 Releases 链接、上游更新检查停用，以及重复启动仅保留一个实例。运行配置、SQLite 数据文件和启动日志均位于独立运行目录。

服务保持后台运行，自启脚本已复制但尚未启用。首次使用请在设置页修改默认登录密码，然后配置自己的渠道。真实模型请求、流式响应和故障切换需在配置渠道后继续验收；本次部署没有导入已有业务数据。
