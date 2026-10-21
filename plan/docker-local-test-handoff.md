# 本地最终候选 Docker 人工交接

本文对应候选 `build/release-local/b-final-20261015-181705-6ebd8c65/`，版本为 `v0.1.0-local.b-final-20261015-181705-6ebd8c65`。它提供包含真实许可证报告的离线构建材料和人工验收步骤，不代表镜像已经生成或发布。旧 A2、A3 候选保留。

当前候选的 Docker context 是：

```text
build/release-local/b-final-20261015-181705-6ebd8c65/docker-context/
```

其中包含 `build/docker/linux/amd64`、`linux/arm64`、`linux/arm/v7`、`linux/386`、`scripts/dockerfile/`、`LICENSE`、真实 `build/THIRD_PARTY_LICENSES.csv` 和历史 Compose 文件。`build-info.json` 的 context 状态为 `license-report-present; daemon-not-run`；尚未构建或导出镜像。报告与七个平台 ZIP 内副本的 SHA-256 均为 `7282dfd529a5ab2904d5d084b61677a28bc63feeef14f4a8edf43e9944db8b16`。报告的 460 条记录、原文与来源限制见[B 组报告](release-license-tools-20261015.md)。冻结候选不追改；复制到新 staging context 后使用本文链接的本地 Compose 模板。

## 构建前检查

在有 Docker daemon 的 Linux 主机上，将下面来源路径替换为上传目录。新测试目录把 context、运行数据和证据分开；后续命令在同一个 shell 执行，`TEST_ROOT` 保持为绝对路径。不要把仓库根目录或包含 profile、数据库、日志、凭据的父目录作为构建上下文。

```sh
set -eu
SOURCE_ROOT='/path/to/uploaded/octopus-roam'
TEST_ROOT="$PWD/octopus-roam-docker-test-$(date +%Y%m%d-%H%M%S)"
test ! -e "$TEST_ROOT" || exit 1
mkdir -p "$TEST_ROOT/context" "$TEST_ROOT/run"
cp -a "$SOURCE_ROOT/build/release-local/b-final-20261015-181705-6ebd8c65/docker-context/." "$TEST_ROOT/context/"
cp "$SOURCE_ROOT/plan/docker-compose-local-test.yml" "$TEST_ROOT/run/compose.yml"
cd "$TEST_ROOT/context" || exit 1
test -s build/THIRD_PARTY_LICENSES.csv
printf '%s  %s\n' '7282dfd529a5ab2904d5d084b61677a28bc63feeef14f4a8edf43e9944db8b16' 'build/THIRD_PARTY_LICENSES.csv' | sha256sum -c -
chmod 0755 build/docker/linux/amd64/octopus-roam
test -x build/docker/linux/amd64/octopus-roam || exit 1
sha256sum build/docker/linux/amd64/octopus-roam
find . -type f -print0 | sort -z | xargs -0 sha256sum > "$TEST_ROOT/context-sha256sums.txt"
```

将程序 SHA-256 与该候选的 `build-info.json` 对应平台条目比较；staging 的新清单同时覆盖许可证报告。`chmod` 只作用于新 context；换架构时调整对应路径。Dockerfile 只设置 entrypoint 的执行权限，entrypoint 在容器启动时对 `/app/octopus-roam` 执行 `chmod +x`；构建前仍须核对宿主机的程序权限。镜像标签相同也不能证明新 context 与冻结候选相同，应另行记录 staging 清单和镜像 ID。

## 构建命令

下面以 `linux/amd64` 为例。一个命令只构建一个目标，`TARGETPLATFORM` 必须与 `build/docker` 下的目录一致。版本标签只用于本地验收，不会推送到 registry。

```sh
docker build \
  --platform linux/amd64 \
  --build-arg TARGETPLATFORM=linux/amd64 \
  -f scripts/dockerfile/Dockerfile \
  -t octopus-roam:v0.1.0-local.b-final-20261015-181705-6ebd8c65 \
  .
```

ARMv7 使用 `--platform linux/arm/v7 --build-arg TARGETPLATFORM=linux/arm/v7`；ARM64 使用 `linux/arm64`；386 使用 `linux/386`。Docker daemon 必须已有可用的 `alpine` 基础镜像和 `apk` 缓存，或者用户明确允许其在云主机上获取。离线环境没有这些材料时，保留“待环境”，不要把构建失败改写为应用失败。

构建成功后可导出本地 tar，并记录文件哈希和镜像 ID：

```sh
docker image inspect octopus-roam:v0.1.0-local.b-final-20261015-181705-6ebd8c65 --format '{{.Id}}'
docker save octopus-roam:v0.1.0-local.b-final-20261015-181705-6ebd8c65 -o octopus-roam-v0.1.0-local.b-final-20261015-181705-6ebd8c65-linux-amd64.tar
sha256sum octopus-roam-v0.1.0-local.b-final-20261015-181705-6ebd8c65-linux-amd64.tar
```

## 启动和数据库占位配置

上面的步骤已经把 [docker-compose-local-test.yml](docker-compose-local-test.yml) 复制到 `$TEST_ROOT/run/compose.yml`。模板禁止自动拉取应用镜像、将端口绑定在 `127.0.0.1`、给予 30 秒正常停止时间，并关闭启动价格更新；远端浏览器可通过用户自行配置的 SSH 转发访问。默认使用挂载目录中的 SQLite。[SQLite 配置](docker-database-config.example.json)、[MySQL 占位配置](docker-database-mysql.example.json)和 [PostgreSQL 占位配置](docker-database-postgres.example.json)均不含真实连接材料；MySQL/PostgreSQL 示例只是连接形状，不能原样启动，也不能代替专用数据库授权。不要把真实 profile 或完整 DSN 写入 Compose 文件。

```sh
export OCTOPUS_ROAM_IMAGE='octopus-roam:v0.1.0-local.b-final-20261015-181705-6ebd8c65'
export OCTOPUS_ROAM_DATA_DIR="$TEST_ROOT/run/roam-data"
export OCTOPUS_ROAM_PORT=18080
mkdir -p "$OCTOPUS_ROAM_DATA_DIR"
docker compose -f "$TEST_ROOT/run/compose.yml" config --quiet
docker compose -f "$TEST_ROOT/run/compose.yml" up -d
```

如需连接专用测试库，只在用户自己的部署终端设置 `OCTOPUS_ROAM_DATABASE_TYPE` 和 `OCTOPUS_ROAM_DATABASE_PATH`，值使用占位符替换后的实际配置；配置和数据库权限仍按 `plan/cloud-database-test-handoff.md` 单独授权。Compose 人工验收只做连接冒烟，MySQL/PostgreSQL 的完整刷新、空库恢复和失败回滚仍由执行会话负责。

正常停止并检查挂载数据：

```sh
docker compose -f "$TEST_ROOT/run/compose.yml" logs --no-color --tail=200
docker compose -f "$TEST_ROOT/run/compose.yml" ps
docker compose -f "$TEST_ROOT/run/compose.yml" stop --timeout 30
```

## 人工验收记录

记录镜像 ID、tar SHA-256（若导出）、CPU 架构、Docker/Compose 版本、使用的数据库类型和以下项目。`docker ps` 显示 `Up` 不能替代页面/API 检查。

| ID | 检查 | 结果/证据 |
| --- | --- | --- |
| C1 | 目标架构、镜像标签、程序/前端版本 | 待用户填写 |
| C2 | 新数据目录初始化、登录、API 和端口 | 待用户填写 |
| C3 | 正常停止并重建容器后数据仍在 | 待用户填写 |
| C4 | SQLite 或指定云数据库连接冒烟 | 待用户填写 |
| C5 | 普通/流式请求、取消、失败切换、日志/SSE、模型刷新 | 待用户填写 |
| C6 | TLS/CA、PUID/PGID（如使用）和挂载目录权限 | 待用户填写 |
| C7 | 正常停止、宽限时间和日志完整性 | 待用户填写 |
| C8 | SQLite 导出、错误口令拒绝和空库恢复入口 | 待用户填写 |
| C9 | 更换镜像后保留数据挂载和外部数据库配置 | 待用户填写 |

当前材料已包含真实许可证报告，但没有镜像 ID、镜像 tar 或人工结果。用户回传镜像字段、staging context 清单和脱敏日志后，结果书再更新为“完成（用户 Docker 验收）”；在此之前保持“待用户 Docker 验收”。LGPL/MPL 等源码交付及再链接材料条件仍需在公开发布前落实，报告到位不等于发布批准。重建容器/更换镜像仅在这份独立测试部署中按授权执行；停止命令不删除挂载数据或历史证据。
