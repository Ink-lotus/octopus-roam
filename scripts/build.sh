#!/bin/bash
set -euo pipefail

readonly APP_NAME="octopus-roam" # 发布产物和容器内的可执行文件名。
VERSION="${VERSION:-}"
COMMIT="${COMMIT:-}"
OUTPUT_DIR="${OUTPUT_DIR:-build}"
REPO_URL="${REPO_URL:-https://github.com/Ink-lotus/octopus-roam}"
LICENSE_REPORT=""
MATERIALS_DIR=""
MATERIALS_SHA=""
PYTHON="${PYTHON:-python3}"
INCLUDE_ANDROID=1

usage() {
    cat <<'USAGE'
用法: scripts/build.sh --version VERSION --commit COMMIT [选项]

选项:
  --version VERSION          显式注入前后端及归档版本（必填）
  --commit COMMIT            显式注入源码提交标识（必填）
  --output-dir DIRECTORY     输出目录，默认 build；非空目录会拒绝覆盖
  --repo URL                 构建信息中的仓库地址
  --license-report FILE      使用已有非空 THIRD_PARTY_LICENSES.csv
  --materials-dir DIRECTORY  发布材料 artifact；须绑定当前版本及完整 commit
  --materials-sha SHA256     materials.json 的审核后 SHA-256
  --standard-only            跳过 Android/NDK 目标，仅验证标准平台构建
  --help                     显示本帮助
USAGE
}

fail() { echo "build.sh: $*" >&2; exit 2; }

while (($# > 0)); do
    case "$1" in
        --version) [[ $# -ge 2 ]] || fail "--version 需要参数"; VERSION="$2"; shift 2 ;;
        --commit) [[ $# -ge 2 ]] || fail "--commit 需要参数"; COMMIT="$2"; shift 2 ;;
        --output-dir) [[ $# -ge 2 ]] || fail "--output-dir 需要参数"; OUTPUT_DIR="$2"; shift 2 ;;
        --repo) [[ $# -ge 2 ]] || fail "--repo 需要参数"; REPO_URL="$2"; shift 2 ;;
        --license-report) [[ $# -ge 2 ]] || fail "--license-report 需要参数"; LICENSE_REPORT="$2"; shift 2 ;;
        --materials-dir) [[ $# -ge 2 ]] || fail "--materials-dir 需要参数"; MATERIALS_DIR="$2"; shift 2 ;;
        --materials-sha) [[ $# -ge 2 ]] || fail "--materials-sha 需要参数"; MATERIALS_SHA="$2"; shift 2 ;;
        --standard-only) INCLUDE_ANDROID=0; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "未知参数: $1" ;;
    esac
done

[[ -n "$VERSION" ]] || fail "必须显式提供 --version"
[[ -n "$COMMIT" ]] || fail "必须显式提供 --commit"
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || fail "版本格式无效"
[[ "$COMMIT" =~ ^[A-Za-z0-9._-]+$ ]] || fail "commit 格式无效"
[[ "$REPO_URL" =~ ^https://github\.com/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$ ]] || fail "仓库地址格式无效"
[[ -n "$OUTPUT_DIR" && "$OUTPUT_DIR" != "/" && "$OUTPUT_DIR" != "." ]] || fail "输出目录无效"
[[ ! -e "$OUTPUT_DIR" || -d "$OUTPUT_DIR" ]] || fail "输出路径不是目录"
if [[ -d "$OUTPUT_DIR" ]]; then
    shopt -s nullglob dotglob
    existing_output=("$OUTPUT_DIR"/*)
    shopt -u dotglob nullglob
    if (( ${#existing_output[@]} > 0 )); then
        fail "输出目录非空，拒绝覆盖: $OUTPUT_DIR"
    fi
fi
if [[ -n "$LICENSE_REPORT" ]]; then
    [[ -s "$LICENSE_REPORT" ]] || fail "许可证报告为空或不存在: $LICENSE_REPORT"
else
    command -v go-licenses >/dev/null 2>&1 || fail "缺少 go-licenses；请提供 --license-report 或在发布环境准备固定版本的许可证工具"
fi
command -v "${PYTHON}" >/dev/null 2>&1 || fail "Python 工具不可用"
if [[ -n "${MATERIALS_DIR}" ]]; then
    "${PYTHON}" scripts/release_contract.py materials --version "${VERSION}" --commit "${COMMIT}" \
        --materials "${MATERIALS_DIR}" --materials-sha "${MATERIALS_SHA}"
    [[ -n "${LICENSE_REPORT}" ]] || fail "发布材料要求显式 --license-report"
    cmp -s "${LICENSE_REPORT}" "${MATERIALS_DIR}/THIRD_PARTY_LICENSES.csv" || fail "许可证报告与材料不一致"
fi
readonly MODULE_PATH="$(go list -m)" # 模块路径独立于程序名。
readonly OUTPUT_DIR VERSION COMMIT REPO_URL
BUILD_TIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
if [[ -n "${SOURCE_DATE_EPOCH:-}" ]]; then
    BUILD_TIME="$(date -u -d "@${SOURCE_DATE_EPOCH}" +'%Y-%m-%dT%H:%M:%SZ')"
fi
readonly LDFLAGS="-X ${MODULE_PATH}/internal/conf.Version=${VERSION} \
                  -X ${MODULE_PATH}/internal/conf.BuildTime=${BUILD_TIME} \
                  -X ${MODULE_PATH}/internal/conf.Author=Ink-lotus \
                  -X ${MODULE_PATH}/internal/conf.Repo=${REPO_URL} \
                  -X ${MODULE_PATH}/internal/conf.Commit=${COMMIT} \
                  -buildid=${APP_NAME}/${VERSION}/${COMMIT} \
                  -s -w" # 注入版本信息并缩小发布二进制。

build_standard() {
    # 标准矩阵直接使用 GOOS/GOARCH，arm 固定输出 ARMv7 指令集。
    local os go_arch
    IFS=: read -r os go_arch <<<"$1"
    local build_env=(GOOS="${os}" GOARCH="${go_arch}" CGO_ENABLED=0)
    if [ "${go_arch}" = "arm" ]; then
        build_env+=(GOARM=7)
    fi
    local output_name="${APP_NAME}-${os}-${go_arch}"
    if [[ "${os}" == "windows" ]]; then
        output_name+=".exe"
    fi
    echo "Building ${os}/${go_arch}"
    env "${build_env[@]}" go build -trimpath -o "${OUTPUT_DIR}/bin/${output_name}" -ldflags="${LDFLAGS}" -tags=jsoniter .
}

build_android() {
    # Android 矩阵显式绑定 NDK API 21 编译器，脚本只面向 workflow 的 Ubuntu runner。
    local go_arch compiler
    IFS=: read -r go_arch compiler <<<"$1"
    local build_env=(GOOS=android GOARCH="${go_arch}" CGO_ENABLED=1 \
        CC="${ANDROID_NDK_HOME}/toolchains/llvm/prebuilt/linux-x86_64/bin/${compiler}")
    if [ "${go_arch}" = "arm" ]; then
        build_env+=(GOARM=7)
    fi
    echo "Building android/${go_arch}"
    env "${build_env[@]}" go build -trimpath -o "${OUTPUT_DIR}/bin/${APP_NAME}-android-${go_arch}" -ldflags="${LDFLAGS}" -tags=jsoniter .
}

readonly -a STANDARD_TARGETS=(
    "linux:amd64"
    "linux:arm64"
    "linux:arm"
    "linux:386"
    "windows:amd64"
    "darwin:arm64"
    "darwin:amd64"
) # 不依赖 cgo 的固定发布矩阵。
readonly -a ANDROID_TARGETS=(
    "amd64:x86_64-linux-android21-clang"
    "arm64:aarch64-linux-android21-clang"
    "arm:armv7a-linux-androideabi21-clang"
    "386:i686-linux-android21-clang"
) # Android API 21 的固定 ABI 与 NDK clang 映射。

if (( INCLUDE_ANDROID )); then
    : "${ANDROID_NDK_HOME:?ANDROID_NDK_HOME is required unless --standard-only is used}"
fi

# 构建工具不会创建父目录，因此只保留一次直接创建。
mkdir -p "${OUTPUT_DIR}/bin" "${OUTPUT_DIR}/archives" \
    "${OUTPUT_DIR}/docker/linux/amd64" "${OUTPUT_DIR}/docker/linux/386" \
    "${OUTPUT_DIR}/docker/linux/arm/v7" "${OUTPUT_DIR}/docker/linux/arm64"

if [[ -n "${LICENSE_REPORT}" ]]; then
    cp "${LICENSE_REPORT}" "${OUTPUT_DIR}/THIRD_PARTY_LICENSES.csv"
else
    GOFLAGS="-tags=jsoniter" go-licenses report . --ignore "${MODULE_PATH}" >"${OUTPUT_DIR}/THIRD_PARTY_LICENSES.csv"
fi
[[ -s "${OUTPUT_DIR}/THIRD_PARTY_LICENSES.csv" ]] || fail "许可证报告为空"

"${PYTHON}" scripts/release_contract.py frontend --version "${VERSION}" --commit "${COMMIT}"

echo "Building ${APP_NAME} ${VERSION} (${COMMIT})"
for target in "${STANDARD_TARGETS[@]}"; do
    build_standard "${target}"
done
if (( INCLUDE_ANDROID )); then
    for target in "${ANDROID_TARGETS[@]}"; do
        build_android "${target}"
    done
fi

# Docker buildx 按 TARGETPLATFORM 读取固定目录中的同名可执行文件。
cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-amd64" "${OUTPUT_DIR}/docker/linux/amd64/${APP_NAME}"
cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-386" "${OUTPUT_DIR}/docker/linux/386/${APP_NAME}"
cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-arm" "${OUTPUT_DIR}/docker/linux/arm/v7/${APP_NAME}"
cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-arm64" "${OUTPUT_DIR}/docker/linux/arm64/${APP_NAME}"

# 标准库 ZIP writer 显式保留 Unix 模式，并验证实际二进制的版本/commit。
package_args=(package --version "${VERSION}" --commit "${COMMIT}" --output "${OUTPUT_DIR}")
if (( ! INCLUDE_ANDROID )); then package_args+=(--standard-only); fi
if [[ -n "${MATERIALS_DIR}" ]]; then
    package_args+=(--materials "${MATERIALS_DIR}" --materials-sha "${MATERIALS_SHA}")
fi
"${PYTHON}" scripts/release_contract.py "${package_args[@]}"
echo "Artifacts: ${OUTPUT_DIR}/archives"
