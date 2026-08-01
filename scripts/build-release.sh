#!/usr/bin/env bash
set -euo pipefail

version=${1:?usage: build-release.sh <vX.Y.Z[-prerelease]> [output-dir]}
output_dir=${2:-dist}

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
  printf 'invalid release version: %s\n' "$version" >&2
  exit 2
fi

mkdir -p "$output_dir"
if [[ -n "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
  printf 'release output directory must be empty: %s\n' "$output_dir" >&2
  exit 2
fi

targets=(
  linux/amd64
  linux/arm64
)

for target in "${targets[@]}"; do
  IFS=/ read -r goos goarch <<<"$target"
  name="abdim_${version#v}_${goos}_${goarch}"
  stage="$output_dir/$name"
  binary="$stage/abdim"

  mkdir "$stage"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$binary" ./cmd/abdim
  tar -C "$stage" -czf "$output_dir/$name.tar.gz" "$(basename "$binary")"
done

(
  cd "$output_dir"
  sha256sum ./*.tar.gz > SHA256SUMS
)
