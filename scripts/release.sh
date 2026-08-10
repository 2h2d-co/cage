#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: $0 VERSION" >&2
	exit 2
fi

version=$1
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
	echo "release version must be a semantic version" >&2
	exit 1
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
tag="v$version"
prerelease=false
if [[ $version == *-* ]]; then
	prerelease=true
fi

if [[ $(git branch --show-current) != main ]]; then
	echo "releases must be created from main" >&2
	exit 1
fi
if [[ -n $(git status --porcelain=v1) ]]; then
	echo "working tree must be clean before creating a release" >&2
	exit 1
fi

git fetch --quiet --tags origin main
if [[ $(git rev-parse HEAD) != "$(git rev-parse origin/main)" ]]; then
	echo "HEAD must match origin/main before creating a release" >&2
	exit 1
fi
if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then
	echo "tag $tag already exists" >&2
	exit 1
fi
if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	echo "remote tag $tag already exists" >&2
	exit 1
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

repo=2h2d-co/cage
for command_name in gh jq; do
	if ! command -v "$command_name" >/dev/null; then
		echo "release preparation requires $command_name" >&2
		exit 1
	fi
done

source_subject="chore: prepare $tag release source"
source_prepared=false
if [[ $prerelease == false && $(git log -1 --pretty=%s) == "$source_subject" ]]; then
	source_prepared=true
	if ! grep -Eq "^## \\[$version\\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md; then
		echo "existing release source has no dated $version changelog section" >&2
		exit 1
	fi
fi

if [[ $prerelease == false && $source_prepared == false ]]; then
	if grep -Eq "^## \\[$version\\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md; then
		echo "$version is already prepared by a different commit" >&2
		exit 1
	fi
	unreleased=$(awk '
		/^## (\[)?Unreleased(\])?$/ { found = 1; next }
		found && /^## / { exit }
		found { print }
	' CHANGELOG.md)
	if [[ -z ${unreleased//[[:space:]]/} ]]; then
		echo "CHANGELOG.md Unreleased section is empty" >&2
		exit 1
	fi
	release_date=$(date -u +%F)
	awk -v heading="## [$version] - $release_date" '
		!inserted && /^## (\[)?Unreleased(\])?$/ {
			print
			print ""
			print heading
			inserted = 1
			next
		}
		{ print }
		END {
			if (!inserted) {
				exit 1
			}
		}
	' CHANGELOG.md >"$tmpdir/CHANGELOG.md"
	mv "$tmpdir/CHANGELOG.md" CHANGELOG.md
	git add CHANGELOG.md
fi

staged_files=$(git diff --cached --name-only)
if [[ $prerelease == true && -n $staged_files ]]; then
	echo "prerelease metadata changed unexpected files: $staged_files" >&2
	exit 1
fi
if [[ $prerelease == false && $source_prepared == false && $staged_files != CHANGELOG.md ]]; then
	echo "stable release metadata changed unexpected files: $staged_files" >&2
	exit 1
fi

if [[ $prerelease == false && $source_prepared == false ]]; then
	git commit -S -m "$source_subject"
	source_prepared=true
	git -c gpg.format=ssh \
		-c gpg.ssh.allowedSignersFile=.github/release-signers \
		verify-commit HEAD
	git push origin main
fi

source_sha=$(git rev-parse HEAD)
git -c gpg.format=ssh \
	-c gpg.ssh.allowedSignersFile=.github/release-signers \
	verify-commit "$source_sha"
git fetch --quiet origin main
if [[ $source_sha != "$(git rev-parse origin/main)" ]]; then
	echo "release source must match origin/main before preparation" >&2
	exit 1
fi

artifact_name="go-release-${version}-${source_sha}"
run_title="Prepare $version from $source_sha"

find_preparation_run() {
	gh run list \
		--repo "$repo" \
		--workflow prepare-release.yml \
		--event workflow_dispatch \
		--limit 100 \
		--json databaseId,displayTitle,headSha,createdAt |
		jq -r \
			--arg title "$run_title" \
			--arg sha "$source_sha" \
			'[.[] | select(.displayTitle == $title and .headSha == $sha)]
			 | sort_by(.createdAt) | reverse | .[0].databaseId // empty'
}

find_preparation_artifact() {
	local run_id=$1
	gh api "repos/$repo/actions/runs/$run_id/artifacts" |
		jq -r \
			--arg name "$artifact_name" \
			'[.artifacts[] | select(.name == $name and .expired == false)]
			 | .[0].id // empty'
}

run_id=$(find_preparation_run)
artifact_id=
if [[ -n $run_id ]]; then
	run_conclusion=$(gh api "repos/$repo/actions/runs/$run_id" --jq .conclusion)
	if [[ $run_conclusion == success ]]; then
		artifact_id=$(find_preparation_artifact "$run_id")
	fi
fi

if [[ -z $artifact_id ]]; then
	previous_run_id=$run_id
	gh workflow run prepare-release.yml \
		--repo "$repo" \
		--ref main \
		--field "version=$version" \
		--field "source_sha=$source_sha"
	run_id=
	for _ in {1..60}; do
		run_id=$(find_preparation_run)
		if [[ -n $run_id && $run_id != "$previous_run_id" ]]; then
			break
		fi
		sleep 2
	done
	if [[ -z $run_id || $run_id == "$previous_run_id" ]]; then
		echo "could not find the dispatched release preparation run" >&2
		exit 1
	fi
	gh run watch "$run_id" --repo "$repo" --exit-status
	artifact_id=$(find_preparation_artifact "$run_id")
	if [[ -z $artifact_id ]]; then
		echo "successful preparation run has no expected release artifact" >&2
		exit 1
	fi
fi

workflow_id=$(gh api "repos/$repo/actions/workflows/prepare-release.yml" --jq .id)
run=$(gh api "repos/$repo/actions/runs/$run_id")
if [[ $(jq -r .workflow_id <<<"$run") != "$workflow_id" ||
	$(jq -r .event <<<"$run") != workflow_dispatch ||
	$(jq -r .head_branch <<<"$run") != main ||
	$(jq -r .head_sha <<<"$run") != "$source_sha" ||
	$(jq -r .conclusion <<<"$run") != success ]]; then
	echo "preparation run does not describe a successful build of the release source" >&2
	exit 1
fi
artifact=$(gh api "repos/$repo/actions/artifacts/$artifact_id")
if [[ $(jq -r .name <<<"$artifact") != "$artifact_name" ||
	$(jq -r .expired <<<"$artifact") != false ||
	$(jq -r .workflow_run.id <<<"$artifact") != "$run_id" ||
	$(jq -r .workflow_run.head_sha <<<"$artifact") != "$source_sha" ]]; then
	echo "preparation artifact does not belong to the expected build" >&2
	exit 1
fi

prepared_output="$tmpdir/prepared"
mkdir -p "$prepared_output"
gh run download "$run_id" \
	--repo "$repo" \
	--name "$artifact_name" \
	--dir "$prepared_output"
local_digest=$(scripts/validate-release.sh "$version" "$prepared_output")

git fetch --quiet origin main
if [[ $(git rev-parse HEAD) != "$source_sha" ||
	$(git rev-parse origin/main) != "$source_sha" ||
	-n $(git status --porcelain=v1) ]]; then
	echo "main changed while the release artifacts were prepared" >&2
	exit 1
fi

trailers=$(printf '%s\n' \
	"Release-Manifest-SHA256: $local_digest" \
	"Release-Source-SHA: $source_sha" \
	"Release-Build-Run-ID: $run_id" \
	"Release-Artifact-ID: $artifact_id")
git commit --allow-empty -S \
	-m "release: $tag" \
	-m "$trailers"

release_commit=$(git rev-parse HEAD)
git -c gpg.format=ssh \
	-c gpg.ssh.allowedSignersFile=.github/release-signers \
	verify-commit "$release_commit"
signed_digest=$(git log -1 \
	--format='%(trailers:key=Release-Manifest-SHA256,valueonly)' \
	"$release_commit")
signed_source=$(git log -1 \
	--format='%(trailers:key=Release-Source-SHA,valueonly)' \
	"$release_commit")
signed_run=$(git log -1 \
	--format='%(trailers:key=Release-Build-Run-ID,valueonly)' \
	"$release_commit")
signed_artifact=$(git log -1 \
	--format='%(trailers:key=Release-Artifact-ID,valueonly)' \
	"$release_commit")
if [[ $signed_digest != "$local_digest" ||
	$signed_source != "$source_sha" ||
	$signed_run != "$run_id" ||
	$signed_artifact != "$artifact_id" ]]; then
	echo "signed release authorization does not match the prepared artifacts" >&2
	exit 1
fi
if [[ $(git rev-parse "$release_commit^") != "$source_sha" ||
	$(git rev-parse "$release_commit^{tree}") != $(git rev-parse "$source_sha^{tree}") ]]; then
	echo "release commit must be a tree-identical child of the prepared source" >&2
	exit 1
fi

git tag "$tag"
if [[ $(git cat-file -t "refs/tags/$tag") != commit ]]; then
	echo "release tag $tag must be a lightweight tag" >&2
	exit 1
fi

echo "created signed release commit $release_commit"
echo "created lightweight tag $tag"
echo "authorized preparation run $run_id artifact $artifact_id"
echo "locally attested release manifest SHA-256: $local_digest"
echo "push with: git push --atomic origin main $tag"
