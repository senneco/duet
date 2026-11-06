#!/bin/sh
# Ставит или обновляет duet: команду ~/.local/bin/duet, скилл и хуки в Claude Code и Codex.
# Из клона с Go — собирает из исходников. Иначе скачивает готовую программу с GitHub:
#   curl -fsSL https://raw.githubusercontent.com/senneco/duet/main/install.sh | sh
# Повторный запуск ставит новую версию и ничего не задваивает.
set -e
repo=senneco/duet

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "duet: unsupported machine $(uname -m)" >&2; exit 1 ;;
esac
asset="duet-$os-$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

src=$(cd "$(dirname "$0")" 2>/dev/null && pwd)
if [ -f "$src/go.mod" ] && grep -q "module github.com/$repo" "$src/go.mod" && command -v go >/dev/null; then
  echo "Building duet from $src"
  (cd "$src" && go build -o "$tmp/duet" ./cmd/duet)
elif curl -fsSL "https://github.com/$repo/releases/latest/download/$asset" -o "$tmp/duet" 2>/dev/null; then
  echo "Downloaded $asset"
elif command -v gh >/dev/null && gh release download latest --repo "$repo" --pattern "$asset" --output "$tmp/duet" 2>/dev/null; then
  echo "Downloaded $asset (GitHub CLI)"
else
  echo "duet: could not download $asset from https://github.com/$repo/releases. If the repository is private, log in with \`gh auth login\` and run again." >&2
  exit 1
fi

chmod +x "$tmp/duet"
"$tmp/duet" install
