#!/usr/bin/env bash
set -euo pipefail

mode="${1:?build or publish}"
root="${2:?artifact directory}"
version="${VERSION:?VERSION is required}"
commit="${COMMIT:?COMMIT is required}"
[[ "${version}" =~ ^v0\.1\.0(-rc\.[1-9][0-9]*)?$ ]] || exit 2
[[ "${commit}" =~ ^[0-9a-f]{40}$ ]] || exit 2
repository="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
repository="${repository,,}"
image="ghcr.io/${repository}"
arches=(amd64 arm64 arm 386)

case "${mode}" in
    build)
        mkdir "${root}/images"
        for arch in "${arches[@]}"; do
            platform="linux/${arch}"
            if [[ "${arch}" == arm ]]; then platform=linux/arm/v7; fi
            base="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["alpine"][sys.argv[2]]["base_image"])' "${root}/materials/materials.json" "${arch}")"
            docker buildx build --platform "${platform}" --provenance=false \
                --build-arg "ALPINE_IMAGE=${base}" \
                --label "org.opencontainers.image.version=${version}" \
                --label "org.opencontainers.image.revision=${commit}" \
                --label "org.opencontainers.image.source=https://github.com/${repository}" \
                --tag "${image}:${version}-${arch}" \
                --output "type=docker,dest=${root}/images/${arch}.tar" \
                -f "${root}/docker-context/scripts/dockerfile/Release.Dockerfile" "${root}/docker-context"
        done
        ;;
    publish)
        # Load sealed images; no compilation/apk resolution in this job.
        for arch in "${arches[@]}"; do
            docker load --input "${root}/images/${arch}.tar"
            docker push "${image}:${version}-${arch}"
        done
        refs=()
        for arch in "${arches[@]}"; do refs+=("${image}:${version}-${arch}"); done
        docker buildx imagetools create --tag "${image}:${version}" "${refs[@]}"
        if [[ "${version}" != *-* ]]; then
            docker buildx imagetools create --tag "${image}:latest" "${image}:${version}"
        fi
        ;;
    *) exit 2 ;;
esac
