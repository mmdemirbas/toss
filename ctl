#!/usr/bin/env bash
# toss driver: one function per operation declared in ajans.toml, over
# Taskfile.yml. Extra arguments go to task, except for dev, where they are
# toss's own flags (-port).
set -euo pipefail
cd "$(dirname "$0")"

fail() {
	printf '✗ %s\n' "$*" >&2
	exit 1
}

# The tag a release gets: the next major, minor or patch after the newest v*
# tag (none yet means after v0.0.0), or an explicit vX.Y.Z.
next_tag() {
	local latest
	if [[ "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		printf '%s' "$1"
		return
	fi
	case "$1" in
	major | minor | patch) ;;
	*) fail "deploy release takes major, minor, patch or vX.Y.Z (got: $1)" ;;
	esac
	latest="$(git for-each-ref --count=1 --sort=-v:refname --format='%(refname:short)' 'refs/tags/v*')"
	[[ "${latest:-v0.0.0}" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] ||
		fail "the newest tag, $latest, is not vX.Y.Z; name the version: ./ctl deploy release vX.Y.Z"
	case "$1" in
	major) printf 'v%d.0.0' $((BASH_REMATCH[1] + 1)) ;;
	minor) printf 'v%d.%d.0' "${BASH_REMATCH[1]}" $((BASH_REMATCH[2] + 1)) ;;
	patch) printf 'v%d.%d.%d' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" $((BASH_REMATCH[3] + 1)) ;;
	esac
}

# The Taskfile's dev task is this same `go run`; calling it directly lets flags
# such as -port reach toss.
op_dev() { exec go run ./cmd/toss "$@"; }

op_build() { task build "$@"; }
op_build_dist() { task build-all "$@"; }

op_test() { task test "$@"; }

op_check() {
	./ctl --check
	task quality "$@"
}

op_vendor() { task vendor "$@"; }

op_clean() { task clean "$@"; }

# .github/workflows/release.yml builds and publishes the binaries when a v* tag
# arrives, so a release is: a clean tree, the gate, the tag, the push.
op_deploy_release() {
	local tag
	tag="$(next_tag "${1:-patch}")"
	[[ -z "$(git status --porcelain)" ]] || fail "the working tree has changes; commit them first"
	if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
		fail "$tag already exists"
	fi
	printf '▸ releasing %s\n' "$tag"
	op_check
	git tag "$tag"
	git push origin "$tag"
	printf '✓ %s pushed; the release workflow publishes it\n' "$tag"
}

# >>> ajans runtime 2 >>>
# Written by `ajans contract sync`; edits here are overwritten. The operations
# are declared in ajans.toml and implemented above as op_<verb>[_<target>].

# Where this file is. A relative path was relative to where ctl was started,
# and the driver may have changed directory since - usually into its own, with
# `cd "$(dirname "$0")"` - so it is tried against here, then against where the
# last cd came from, and failing both the directory the driver changed into.
# A function rather than inline: bash 3.2 cannot parse a `case` inside `$(...)`.
_ajans_where() {
	local src="${BASH_SOURCE[0]}" base
	case "$src" in
		/*) ;;
		*)
			for base in "$PWD" "${OLDPWD:-}"; do
				if [[ -n "$base" && -f "$base/$src" ]]; then
					src="$base/$src"
					break
				fi
			done
			;;
	esac
	case "$src" in
		/*) ;;
		*) src="$PWD/${src##*/}" ;;
	esac
	(cd "$(dirname "$src")" && pwd)
}
_ajans_root="$(_ajans_where)"

_ajans_fn() {
	local name="op_$*"
	name="${name// /_}"
	name="${name//-/_}"
	printf '%s' "${name//./_}"
}

# Reads ajans.toml with the first python3 that has tomllib. Modes: list [verb], keys.
_ajans_toml() {
	local py
	if [[ ! -f "$_ajans_root/ajans.toml" ]]; then
		printf '✗ no ajans.toml beside %s\n' "$_ajans_root/ctl" >&2
		return 66
	fi
	for py in python3 /opt/homebrew/bin/python3 /usr/local/bin/python3; do
		if ! command -v "$py" >/dev/null 2>&1 || ! "$py" -c 'import tomllib' 2>/dev/null; then
			continue
		fi
		"$py" - "$_ajans_root/ajans.toml" "$@" <<'PY'
import re, sys, tomllib
try:
    with open(sys.argv[1], 'rb') as f:
        manifest = tomllib.load(f)
except tomllib.TOMLDecodeError as e:
    sys.exit(f'✗ ajans.toml: {e}')
ops, verbs = manifest.get('operations', {}), manifest.get('verbs', {})
mode, rest = sys.argv[2], sys.argv[3:]
if mode == 'keys':
    for key in ops:
        print('op_' + re.sub(r'[ .-]', '_', key))
elif rest:
    for key, op in ops.items():
        verb, _, target = key.partition(' ')
        if verb == rest[0] and target:
            print(f"{target}\t{op.get('summary', '')}")
else:
    seen = []
    for key in ops:
        verb = key.partition(' ')[0]
        if verb not in seen:
            seen.append(verb)
            print(f"{verb}\t{ops[verb]['summary'] if verb in ops else verbs.get(verb, '')}")
PY
		return
	done
	printf '✗ reading ajans.toml needs python3 3.11 or later (tomllib)\n' >&2
	return 69
}

_ajans_check() {
	local declared defined missing extra fn count noun
	declared="$(_ajans_toml keys)" || return
	declared="$(printf '%s\n' "$declared" | sort)"
	defined="$(declare -F | awk '$3 ~ /^op_/ { print $3 }' | sort)"
	missing="$(comm -23 <(printf '%s\n' "$declared") <(printf '%s\n' "$defined") | sed '/^$/d')"
	extra="$(comm -13 <(printf '%s\n' "$declared") <(printf '%s\n' "$defined") | sed '/^$/d')"
	if [[ -z "$missing" && -z "$extra" ]]; then
		count="$(printf '%s\n' "$declared" | grep -c .)"
		if [[ "$count" == 1 ]]; then noun=operation; else noun=operations; fi
		printf '✓ ajans.toml and ctl agree: %s %s\n' "$count" "$noun"
		return 0
	fi
	if [[ -n "$missing" ]]; then
		while IFS= read -r fn; do printf '✗ declared, no function: %s\n' "$fn" >&2; done <<<"$missing"
	fi
	if [[ -n "$extra" ]]; then
		while IFS= read -r fn; do printf '✗ function, not declared: %s\n' "$fn" >&2; done <<<"$extra"
	fi
	return 1
}

_ajans_main() {
	case "${1:-}" in
	'' | --list | -h | --help)
		_ajans_toml list
		return
		;;
	--check)
		_ajans_check
		return
		;;
	esac
	if [[ "${2:-}" == --list ]]; then
		_ajans_toml list "$1"
		return
	fi
	local fn
	if [[ $# -ge 2 ]]; then
		fn="$(_ajans_fn "$1 $2")"
		if declare -F "$fn" >/dev/null; then
			shift 2
			"$fn" "$@"
			return
		fi
	fi
	fn="$(_ajans_fn "$1")"
	if declare -F "$fn" >/dev/null; then
		shift
		"$fn" "$@"
		return
	fi
	if declare -F | awk -v p="$fn"_ '$3 ~ "^" p { found = 1 } END { exit !found }'; then
		printf '✗ %s needs a target: ./ctl %s --list\n' "$1" "$1" >&2
	else
		printf '✗ %s: not an operation of this project (./ctl --list)\n' "$*" >&2
	fi
	return 64
}

_ajans_main "$@"
# <<< ajans runtime <<<
