#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "usage: $0 VERSION OUTPUT_DIRECTORY" >&2
	exit 2
fi

version=$1
output=$2
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
	echo "release version must be a semantic version" >&2
	exit 1
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
tag="v$version"
if [[ $(git cat-file -t "refs/tags/$tag") != commit ]]; then
	echo "release tag $tag must be a lightweight tag" >&2
	exit 1
fi
if [[ $(git rev-parse "refs/tags/$tag^{commit}") != "$(git rev-parse HEAD)" ]]; then
	echo "release tag $tag must point to HEAD" >&2
	exit 1
fi

if [[ -e $output && -n $(find "$output" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
	echo "release output directory must be empty: $output" >&2
	exit 1
fi
mkdir -p "$output"
output=$(cd "$output" && pwd)

archive="cage_${version}_darwin_arm64.tar.gz"
go_bin=${RELEASE_GO_PATH:-go}
if [[ $("$go_bin" env GOVERSION) != go1.26.7 ]]; then
	echo "release builds require Go 1.26.7" >&2
	exit 1
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
stage="$tmpdir/archive"
mkdir -p \
	"$stage/docs/man" \
	"$stage/examples" \
	"$stage/internal/cage/launchd"

CGO_ENABLED=1 GOFLAGS=-mod=readonly "$go_bin" build \
	-buildvcs=false \
	-trimpath \
	-ldflags="-s -w -X main.version=$version" \
	-o "$stage/cage" \
	.

install -m 0644 LICENSE "$stage/LICENSE"
install -m 0644 README.md "$stage/README.md"
install -m 0644 docs/man/cage.1 "$stage/docs/man/cage.1"
install -m 0644 examples/config.toml "$stage/examples/config.toml"
install -m 0644 \
	internal/cage/launchd/co.2h2d.cage.cache-prune.plist.tmpl \
	"$stage/internal/cage/launchd/co.2h2d.cage.cache-prune.plist.tmpl"
chmod 0755 "$stage/cage"
TZ=UTC touch -t 197001010000 \
	"$stage/LICENSE" \
	"$stage/README.md" \
	"$stage/cage" \
	"$stage/docs/man/cage.1" \
	"$stage/examples/config.toml" \
	"$stage/internal/cage/launchd/co.2h2d.cage.cache-prune.plist.tmpl"

(
	cd "$stage"
	COPYFILE_DISABLE=1 /usr/bin/tar \
		--format=ustar \
		--uid 0 \
		--gid 0 \
		--uname root \
		--gname root \
		-cf - \
		LICENSE \
		README.md \
		cage \
		docs/man/cage.1 \
		examples/config.toml \
		internal/cage/launchd/co.2h2d.cage.cache-prune.plist.tmpl
) | /usr/bin/gzip -n >"$output/$archive"

(
	cd "$output"
	shasum -a 256 "$archive" >checksums.txt
	shasum -a 256 checksums.txt "$archive" >release-manifest.sha256
)

scripts/validate-release.sh "$version" "$output"
