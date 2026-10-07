# R1 发布前核对

日期：2026-10-07。当前代码位于 `test`，对外版本与 Git 远程状态尚未修改。

## 已发现的发布阻碍：自更新仍指向上游

`internal/update/update.go` 把最新版本 API 和归档下载地址固定到 `bestruirui/octopus`；`UpdateCore` 会下载归档并替换当前可执行文件。`web/src/components/modules/setting/Info.tsx` 仍会根据上游版本显示更新按钮。`scripts/build.sh` 没有注入独立仓库地址，因此修改 Release 触发分支并不能修复此问题。

这会使独立产品被上游二进制覆盖，发布前必须处理。已核实的事实仅来自源码；本轮没有执行自更新或下载替换程序。

建议本轮以局部补丁关闭上游自更新：

- `internal/update/update.go`：`GetLatestInfo` 返回独立发布源尚未启用的明确错误，不请求上游版本 API。
- `internal/update/core.go`：`UpdateCore` 在网络、文件与重启操作之前返回同一错误。
- `web/src/components/modules/setting/Info.tsx`：版本区明确显示“请从独立版本发布页手动升级”，不显示执行更新按钮。
- 对应测试、三份 locale、升级文档：验证不发请求、不替换文件，记录暂用手动升级。

命令为 `apply_patch` 局部修改、`gofmt -w`、相应 Go 测试/vet 与前端 lint/build。影响仅为停用指向上游的一键更新；独立版本源、产物校验与自动升级路径在发布目标明确后单独设计。此范围已获用户明确授权，后端两个入口已直接拒绝，界面已停止查询上游版本并移除更新按钮。

## 后续发布顺序

1. 完成本轮代码审阅、测试和 MySQL/PostgreSQL 临时实例升级/恢复验证。
2. 备好最终差异和发布说明，按授权创建产品 `main`，保持 `master` 的上游同步定位。
3. 确认 `main` 上 `release.yaml` 的分支、预发布识别和 Docker latest 规则，再切换默认分支。
4. 单独授权修改 `main.go` 至 `v0.1.0-rc.1`、commit/push，验证完整 rc 发布路径。
5. rc 通过、最终数据库备份和安装升级验收完成后，再单独授权 `v0.1.0` 发布。

`release.yaml` 当前三项规则已修复，本轮没有修改 CI。该文件仍依赖远程仓库和镜像凭据等实际发布配置，未执行远程发布就不能宣称流水线已验收。
