#!/bin/sh
set -eu

fail() { echo "duet: $*" >&2; exit 1; }
case $(uname -s) in Darwin) os=darwin;; Linux) os=linux;; *) fail 'Эта система не поддерживается.';; esac
case $(uname -m) in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) fail 'Этот процессор не поддерживается.';; esac
target="$os-$arch"
verify() {
    expected=$(sed -n "s/.*\"$target\":[[:space:]]*\"\([0-9a-f]*\)\".*/\1/p" "$1")
    [ "${#expected}" -eq 64 ] || fail 'В выпуске нет подходящей сборки.'
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$2")
    else
        actual=$(shasum -a 256 "$2")
    fi
    [ "${actual%% *}" = "$expected" ] || fail 'Контрольная сумма duet не совпала.'
}

if [ -f "$0" ]; then
    package=$(CDPATH='' cd "$(dirname "$0")" && pwd)
    if [ -x "$package/duet" ] && [ -f "$package/release.json" ]; then
        verify "$package/release.json" "$package/duet"
        exec "$package/duet" install --from-release "$package/release.json"
    fi
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

fetch() {
    curl -q -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 120 --retry 2 "$1" -o "$2"
}
repo=https://github.com/senneco/duet/releases
fetch "$repo/latest/download/release.json" "$tmp/release.json" || fail 'Не удалось прочитать описание выпуска.'
version=$(sed -n 's/.*"version":[[:space:]]*"\(v[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)".*/\1/p' "$tmp/release.json")
[ -n "$version" ] || fail 'Некорректная версия выпуска.'
fetch "$repo/download/$version/duet-$target" "$tmp/duet" || fail 'Не удалось скачать duet.'
verify "$tmp/release.json" "$tmp/duet"
chmod 755 "$tmp/duet"
"$tmp/duet" install --from-release "$tmp/release.json"
echo 'Готово. Начните новый чат с агентом.'
