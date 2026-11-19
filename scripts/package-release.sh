#!/usr/bin/env bash
set -euo pipefail
version=$1
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
mkdir -p dist
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for target in darwin-arm64 darwin-amd64 linux-arm64 linux-amd64; do
    GOOS=${target%-*} GOARCH=${target#*-} CGO_ENABLED=0 GOTOOLCHAIN=local \
        go build -trimpath -buildvcs=false -o "dist/duet-$target" ./cmd/duet
done
python3 - "$version" "$(git rev-parse HEAD)" <<'PYTHON'
import hashlib
import json
import sys
from pathlib import Path

dist = Path("dist")
targets = ("darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64")
manifest = {
    "protocol": 1,
    "dataFormat": 1,
    "version": sys.argv[1],
    "revision": sys.argv[2],
    "binaries": {
        target: hashlib.sha256((dist / f"duet-{target}").read_bytes()).hexdigest()
        for target in targets
    },
}
(dist / "release.json").write_text(json.dumps(manifest, indent=2) + "\n")
PYTHON
cp install.sh dist/release.json "$stage/"
for target in darwin-arm64 darwin-amd64 linux-arm64 linux-amd64; do
    cp "dist/duet-$target" "$stage/duet"
    tar -czf "dist/duet-$version-$target.tar.gz" -C "$stage" duet install.sh release.json
done
cp install.sh dist/install.sh
(cd dist && sha256sum duet-* release.json > SHA256SUMS)
