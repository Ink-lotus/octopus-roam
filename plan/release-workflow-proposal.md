# v0.1.0 发布工作流本地提案

状态：仅提案，第一轮未获 CI 文件修改授权，`.github/workflows/build.yaml` 和 `.github/workflows/release.yaml` 保持原样。

## 现状与风险

- `release.yaml` 在测试和构建前创建并推送版本标签；测试或构建失败时远端已经留下标签。
- 版本来自 `main.go` 注释，当前仍是上游 `v0.13.6`。首次建立产品 `main` 时，不能依赖路径过滤阻止误触发发布。
- Release 作业没有独立的测试门禁，普通 `build` 作业也没有被发布作业引用。
- L6 基线中的构建脚本从 `git describe` 推导版本，和“构建成功后才公开标签”的顺序相互依赖。L6.5 已改为显式 `--version` / `--commit`（也可使用同名环境变量）；旧 workflow 尚未传入这些参数，不可直接用于发布。
- `changelogithub` 依赖 HEAD 已存在的标签；调整标签顺序时必须改成使用已经审查的说明，避免按分支名生成额外 Release。
- Docker Hub 登录无条件执行；当前只确认 GHCR 目标，Docker Hub 是否启用仍待用户决定。

## 建议顺序

1. `prepare`：读取明确的 `workflow_dispatch` 输入或受保护版本变量，校验 `main.go` 不再是上游版本，计算 `prerelease` 和发布资产名；在首次 `main` 推送时直接退出。
2. `test`：同一 commit 运行 Go 全量/`jsoniter`/race、`go vet`、前端 lint/build 和脚本静态检查。
3. `build`：显式接收版本和 commit，向前端 `VITE_APP_VERSION`、Go ldflags 及归档元数据注入同一值；只上传 Actions artifact，不创建公开 Git tag。
4. `publish`：通过 `needs: [prepare, test, build]` 后再核对 commit、资产哈希和版本。取得用户批准后创建 tag、Release 和 GHCR 版本标签；候选版不更新 `latest`。
5. `changelog`：使用审查过的 `plan/release-notes-v0.1.0.md` 或 Release body，禁止从未打标签的分支名推导版本。
6. Docker：GHCR 登录始终保留；Docker Hub 步骤只在显式启用输入且对应 secrets 存在时执行。

## Artifact 与门禁合同

`release.yaml` 改为显式 `workflow_dispatch`；普通 push/PR 只运行 `build.yaml` 的测试，不创建远端对象。以下是待 R1 落地的作业依赖示意，不是已经应用的 workflow：

```yaml
jobs:
  prepare:
    permissions:
      contents: read
    # 校验版本、目标 ref 和 main.go，解析一次不可变的完整 commit SHA。
  test:
    needs: prepare
    permissions:
      contents: read
    # checkout prepare.outputs.commit；执行规定回归。
  build:
    needs: [prepare, test]
    permissions:
      contents: read
    # checkout 同一 commit；生成内部 artifact，不创建 tag。
  publish:
    needs: [prepare, test, build]
    permissions:
      contents: write
      packages: write
    # 下载并验证本次运行的 artifact 后，才允许创建 tag / Release / 镜像。
```

- `prepare` 输出完整 `commit`、`version`、`prerelease`；其他作业均以该 SHA checkout，不能再次根据可移动分支解析源码。各作业还须比对当前 HEAD 与输出 SHA。
- `build` 使用 `bash scripts/build.sh --version "$VERSION" --commit "$COMMIT" --output-dir "$ARTIFACT_ROOT"`，输出目录须为空；前后端由同一版本参数构建。正常发布保留 Android 矩阵，`--standard-only` 仅用于明确的平台子集，不能把子集称为完整发布矩阵。
- `scripts/build.sh` 的输出必须显式映射给 Dockerfile 的固定路径：`$ARTIFACT_ROOT/docker/linux/amd64/octopus-roam`、`linux/arm64/octopus-roam`、`linux/arm/v7/octopus-roam` 和 `linux/386/octopus-roam`，并将同一份非空 `THIRD_PARTY_LICENSES.csv` 放到 `$ARTIFACT_ROOT/THIRD_PARTY_LICENSES.csv`；构建 Docker context 时分别映射为 `build/docker/...` 和 `build/THIRD_PARTY_LICENSES.csv`。脚本并不独立生成完整的 Docker context，workflow 必须在 artifact 清单中明确这两个映射和 `scripts/dockerfile/`、`LICENSE`、Compose 模板。
- 许可证工具在独立 setup 步骤准备固定版本，不在构建脚本中 `go run ...@version` 联网补装。许可证缺失或为空时在编译前失败；不能拿本轮无许可证的本地证据包作为发布输入。
- artifact 名称包含版本和完整 SHA；内容至少包括完整平台 `archives/`、`SHA256SUMS`、实际 `THIRD_PARTY_LICENSES.csv`、Docker 构建上下文和记录版本/SHA/平台清单的构建元数据。测试用 profile、数据库、日志和临时目录不能进入附件目录。
- `publish` 只下载本次运行的指定 artifact，核对名称、完整 SHA、版本、平台归档白名单、许可证非空和 `SHA256SUMS`；缺文件、额外归档或哈希不一致均须在创建 tag 前失败。
- `publish` 在门禁成功后创建指向该 SHA 的新 tag，再创建 Release 和对应版本镜像；Release body 使用已审查文本。任一前置 job 失败/取消时，不能通过 `if: always()`、`continue-on-error` 或独立作业绕过门禁。
- 使用发布 concurrency 防止同版本并行发布；已存在 tag/Release 先核实状态，不能自动覆盖或强推。RC 的 `prerelease` 为 true，仅发布版本镜像标签；稳定版才更新 `latest`。
- workflow 使用已提交价格预设，不执行 `scripts/updatePrice.py`。离线回归优先使用已有 Go 模块缓存、`GOTOOLCHAIN=local`、`GOPROXY=off` 和 `-mod=readonly`；如果环境设置 `GOSUMDB=off`，只能说明关闭了校验数据库查询，不能写成关闭所有 `go.sum` 校验。Action、Go、Node、pnpm 和许可证工具的固定版本，以及 YAML/actionlint 和实际 artifact 下载校验，都须在 R1 按环境补齐。

## 需要第二轮授权的具体动作

- 修改 `.github/workflows/build.yaml`、`.github/workflows/release.yaml`。
- 创建或推送 `main`、版本标签、Release、镜像以及任何远端分支/仓库设置。
- 在线验证 Actions、GitHub Release、GHCR 或 Docker Hub。

本地第一轮仅保留本提案和静态审查记录；没有 `actionlint`，未声称 YAML 已由 actionlint 或线上 Actions 验证。
