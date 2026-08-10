#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "usage: $0 VERSION RELEASE_DIRECTORY" >&2
	exit 2
fi

version=$1
release_dir=$2
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
	echo "release version must be a semantic version" >&2
	exit 1
fi
if [[ ! -d $release_dir ]]; then
	echo "release directory does not exist: $release_dir" >&2
	exit 1
fi
release_dir=$(cd "$release_dir" && pwd)
archive="cage_${version}_darwin_arm64.tar.gz"

expected_files=$(printf '%s\n' \
	"$archive" \
	"checksums.txt" \
	"release-manifest.sha256")
actual_files=$(find "$release_dir" -maxdepth 1 -type f -print |
	sed "s|^$release_dir/||" |
	LC_ALL=C sort)
if [[ $actual_files != "$expected_files" ]]; then
	echo "release directory contains unexpected files" >&2
	diff -u <(printf '%s\n' "$expected_files") <(printf '%s\n' "$actual_files") >&2 || true
	exit 1
fi

manifest_files=$(awk '{print $2}' "$release_dir/release-manifest.sha256")
expected_manifest_files=$(printf '%s\n' "checksums.txt" "$archive")
if [[ $manifest_files != "$expected_manifest_files" ]]; then
	echo "release manifest does not contain the exact expected assets" >&2
	exit 1
fi
(
	cd "$release_dir"
	shasum -a 256 -c release-manifest.sha256 >&2
)

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
(
	cd "$release_dir"
	shasum -a 256 "$archive"
) >"$tmpdir/expected-checksums.txt"
if ! cmp "$tmpdir/expected-checksums.txt" "$release_dir/checksums.txt"; then
	echo "checksums.txt does not match the release archive" >&2
	exit 1
fi

expected_archive_files=$(printf '%s\n' \
	"LICENSE" \
	"README.md" \
	"cage" \
	"docs/man/cage.1" \
	"examples/config.toml" \
	"internal/cage/launchd/co.2h2d.cage.cache-prune.plist.tmpl")
actual_archive_files=$(tar -tzf "$release_dir/$archive" | LC_ALL=C sort)
if [[ $actual_archive_files != "$expected_archive_files" ]]; then
	echo "$archive contains unexpected files" >&2
	exit 1
fi

shasum -a 256 "$release_dir/release-manifest.sha256" | awk '{print $1}'
