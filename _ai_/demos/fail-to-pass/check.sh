#!/usr/bin/env bash
# Verify a commit is fail-to-pass: its tests must fail when its non-test
# changes are reverted, and pass when they are not.
#
# This is the reproduce phase's exit condition made mechanical. It is a
# stronger check than running the new test against the parent commit, which
# often fails to compile for an unrelated reason; reverting only the
# non-test hunks isolates "does this test detect this bug" from everything
# else in the tree.
#
# Usage: check.sh <commit> [<commit> ...]
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
work=$(mktemp -d)
trap 'git -C "$repo_root" worktree remove --force "$work/wt" 2>/dev/null || true; rm -rf "$work"' EXIT

# test_paths and code_paths split a commit's touched Go files by role. A
# commit that changed no test file cannot be fail-to-pass at all, which is
# itself the finding worth reporting.
test_paths() { git -C "$repo_root" show --name-only --format= "$1" | grep -E '_test\.go$' || true; }
code_paths() { git -C "$repo_root" show --name-only --format= "$1" | grep -E '\.go$' | grep -vE '_test\.go$' || true; }

# packages_of maps touched test files to package paths, skipping any whose
# directory does not exist in the checkout (a commit that deleted a package
# still lists its test files).
packages_of() {
	local paths=$1 root=$2 out=()
	while IFS= read -r p; do
		[ -n "$p" ] && [ -d "$root/$(dirname "$p")" ] && out+=("./$(dirname "$p")")
	done <<<"$paths"
	[ ${#out[@]} -eq 0 ] && return 0
	printf '%s\n' "${out[@]}" | sort -u | tr '\n' ' '
}

check_one() {
	local commit=$1
	local subject
	subject=$(git -C "$repo_root" log -1 --format=%s "$commit")

	local tests code
	tests=$(test_paths "$commit")
	code=$(code_paths "$commit")

	if [ -z "$tests" ]; then
		printf '%-9s %-56s %s\n' "$commit" "${subject:0:56}" "NO TEST      (not fail-to-pass)"
		return
	fi
	if [ -z "$code" ]; then
		printf '%-9s %-56s %s\n' "$commit" "${subject:0:56}" "TEST ONLY    (nothing to revert)"
		return
	fi

	git -C "$repo_root" worktree add --detach -q "$work/wt" "$commit"

	local pkgs
	pkgs=$(packages_of "$tests" "$work/wt")
	if [ -z "${pkgs// /}" ]; then
		printf '%-9s %-56s %s\n' "$commit" "${subject:0:56}" "NO PACKAGE   (test dirs gone at this commit)"
		git -C "$repo_root" worktree remove --force "$work/wt"
		return
	fi

	# Baseline: the commit as written must pass, or the comparison is noise.
	# shellcheck disable=SC2086 # $pkgs is a space-separated package list, split on purpose
	if ! (cd "$work/wt" && go test $pkgs >/dev/null 2>&1); then
		printf '%-9s %-56s %s\n' "$commit" "${subject:0:56}" "BASELINE RED (commit does not pass)"
		git -C "$repo_root" worktree remove --force "$work/wt"
		return
	fi

	# Revert only the non-test hunks, leaving the new tests in place.
	# shellcheck disable=SC2086 # word splitting is how the path list is passed
	# --no-ext-diff is required: a configured external differ (difftastic and
	# friends) renders for a human and emits no applicable patch at all, so
	# without it every revert silently produces an empty diff.
	git -C "$repo_root" diff --no-ext-diff "$commit" "$commit"^ -- $code |
		(cd "$work/wt" && git apply -) 2>/dev/null || {
		printf '%-9s %-56s %s\n' "$commit" "${subject:0:56}" "REVERT FAILED"
		git -C "$repo_root" worktree remove --force "$work/wt"
		return
	}

	local verdict
	# shellcheck disable=SC2086 # $pkgs is a space-separated package list, split on purpose
	if (cd "$work/wt" && go test $pkgs >/dev/null 2>&1); then
		verdict="LIVED        (test passes without the fix)"
	else
		verdict="FAIL-TO-PASS (test detects the bug)"
	fi

	printf '%-9s %-56s %s\n' "$commit" "${subject:0:56}" "$verdict"
	git -C "$repo_root" worktree remove --force "$work/wt"
}

printf '%-9s %-56s %s\n' "commit" "subject" "verdict"
for c in "$@"; do check_one "$c"; done
