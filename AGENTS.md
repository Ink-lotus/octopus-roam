# 项目规划入口

- 开发或规划前先读取本机存在的 `plan/README.md`，再读取用户指定的当前任务或版本计划；v0.1.1、v0.1.2 均已发布，不从已完成计划自动续做。后续平台验收与分支协作遵守 `docs/release-ci.md` 的当前门禁。
- 仅在选择后续功能或用户明确要求时读取 `plan/future-plans.md`；其中条目未排期，不自动执行。
- `plan/archive/` 和 `docs/archive/` 中的旧规划全部失效，只在用户明确要求历史追溯时读取；不要默认搜索、加载或执行旧里程碑、发布交接、旧版本规则或历史授权。
- 当前代码、测试和实际发布脚本是实现事实。进入会话核对 main/HEAD 与规划基线，保护现有工作区改动，不为符合旧计划回退代码。
- `plan/release-notes-v0.1.0.md` 是保留给既有脚本的历史发布材料，不是当前计划；操作文档中的历史证据链接也不是自动续读要求。
- `plan/` 仅本地保存。如跨机器缺少当前计划，先说明缺失；已有明确的任务范围、准确 SHA 和验收命令时按该任务执行，缺少必要信息时再获取，不从归档推导新的开发任务。
- 本文件仅补充项目规划导航；本会话用户提供的授权门禁、工具、语言和其他工作要求继续适用。

## 后续平台验收门禁

- 自 2026-11-12 起，后续版本的正式支持目标为 Windows amd64 原生程序 + SQLite，以及 Linux amd64 宿主上的 Docker Engine / Linux amd64 镜像 + SQLite；使用 Compose 发布部署说明时一并验证该入口。
- WSL2 / WSL Containers 定位为预期兼容环境，不再作为独立正式支持组合或发布必验项，不要求保留本机 WSL 或固定 WSLc 版本。Windows 挂载、权限、网络等集成问题按实际问题调查，不承诺所有 WSL 配置等价。
- 本项目原有的本机 WSL / WSLc 开发、构建和测试流程已过时。除用户明确要求处理 WSL 相关任务外，不默认调用 `wsl` / `wslc`、启动或安装发行版，也不在 SSH 或云机不可用时自动回退到 WSL。
- Windows 原生验收在本机完成；Linux 开发、构建和测试默认在 instabox 等 Linux amd64 主机上通过 SSH 直接执行，由当前代理运行命令和读取结果。测试资源的云商不属于产品支持条件。
- `docs/support-v0.1.0.md`、已发布版本说明、已完成规划和验收记录中的 WSL / WSLc 环境准备、命令及发布门禁仅供历史追溯，不作为当前操作依据；即使旧文件未逐页标注过时，也遵守本条。历史运行结果保留为当时事实，不改写已发布说明或附件。
- 平台验收必须绑定本次准确 commit、实际 ZIP / 镜像及哈希，覆盖安装、正常启停、真实旧版升级、持久化和备份恢复；Linux 另验证数据卷权限与容器重建。SSH 连通、工具版本查询或镜像构建成功不能替代应用验收。首次 Linux 开发产物应用验收已在 C02 的准确提交 `9a7ed524bf763bc776ef291a0998aeb715700ad4` 完成；后续版本仍须用本次实际产物验收，当前证据见 `docs/support.md`。旧 WSLc 结果保留为历史证据。
- `scripts/ci-test.sh`、workflow、锁文件、固定工具版本、规定的 race 检查和完整发布产物合同继续执行。Linux 原生、其他架构、其他数据库及其余部署组合维持各自既有状态；当前支持矩阵见 `docs/support.md`，详细发布门禁见 `docs/release-ci.md`。

## instabox 唤醒与 SSH

- 连接顺序固定为 **API 检查并按需唤醒 → 等待 `state=running` 且 `runtime.ready=true` → SSH**。新会话首次连接、主机休眠后及断线后重连，都先走脚本；不要直接执行保存的 SSH 连接串或反复重试裸 `ssh`。
- 本机统一入口为 `plan/tools/Invoke-InstaboxSsh.ps1`，当前测试主机为 `linux-test-ssh`。在仓库根目录用 PowerShell 7 运行：

```powershell
pwsh -NoProfile -File .\plan\tools\Invoke-InstaboxSsh.ps1 -MachineName linux-test-ssh -Command 'uname -s; uname -m'
```

- 此入口已内置唤醒：自动休眠用 `wake`，手动暂停用 `resume`，停止用 `start`；已就绪的主机直接进入后续检查。脚本确认运行与就绪条件后输出 `Step=ready`，再校验 SSH 主机证书并执行命令。成功以 `ssh-completed` 的退出码和实际输出为准；`wake-request` 只代表请求已接受。
- 后续已获授权的远程命令使用同一入口的 `-Command`，本地 Bash 文件可用 `-ScriptPath`。唤醒与 SSH 均使用此统一入口连接 `linux-test-ssh`，常规连接不加 `-Create`。
- 遇到 `cooldown` 等待服务给出的冷却时间，不反复唤醒；`wait-ready` 超时或失败时先核对 API 状态，不继续裸 SSH、不自动重放可能已执行的远程操作，也不回退 WSL。
- 脚本与凭据当前仅本机保存，不随 Git 分支同步。脚本或加密配置缺失时先补齐连接条件，不猜测主机地址、不自动新建机器。DPAPI 凭据须由保存时的同一 Windows 用户读取；遇到 `Cryptography_DpApi_ProfileMayNotBeLoaded` 先核对执行用户和工具权限，不据此判断云机离线。保留严格主机校验，不在输出或仓库中暴露 API Key、SSH 令牌或私有连接参数。

## test 分支与云端验证

- 日常验证使用可复用的 `test` 分支同步已提交代码；云机检出本轮指定的完整 SHA，核对工作区后执行对应测试，记录命令、退出码、环境和产物身份。分支名不能代替受测 SHA。
- `test` 的对应检查及本轮所需验收通过后，核对最新 `main` 是受测 SHA 的祖先，再以 `git merge --ff-only <已验证SHA>` 推进本地 `main`，按授权推送。无法快进时先将最新 `main` 合入 `test`，重新验证最终 SHA；不得把 Squash / Rebase 产生的新提交当作原已测提交。
- `test` 正常提交和推送即可复用，不默认删除重建或强推。此流程不构成实际 commit、远端分支、推送、发布或仓库保护设置的操作授权，仍遵守当前任务的具体授权门禁。
- Git 同步不包含未提交文件、被忽略的 `plan/`、凭据、数据库或依赖缓存。必要测试入口应纳入版本管理，任务专用参数随任务明确传递，云机依赖按锁文件准备；密钥和私有连接信息不得写入仓库。
