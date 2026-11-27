#!/usr/bin/env sh
# Print the Go version fss is built with: go.mod's `toolchain` line if it has
# one (e.g. "1.27.4"), else its `go` line. The `go` line is only the minimum
# promised to code importing fss.
#
# Used by the Docker workflows to keep the Dockerfile's GO_VERSION build-arg
# in sync with go.mod, and by the CI lint job to guard against drift between
# the Dockerfile default and go.mod. Local builders can run this directly:
#
#     docker build --build-arg GO_VERSION=$(scripts/go-version.sh) .
#
# Or rely on the Dockerfile's fallback default (kept in sync by the guard).

set -eu

cd -- "$(dirname -- "$0")/.."

version=$(awk '/^toolchain go[0-9]+\.[0-9]+(\.[0-9]+)?$/ { sub(/^go/, "", $2); print $2; exit }' go.mod)
[ -n "$version" ] || version=$(awk '/^go [0-9]+\.[0-9]+(\.[0-9]+)?$/ { print $2; exit }' go.mod)

if [ -z "$version" ]; then
	echo "go-version.sh: could not find a 'toolchain goX.Y[.Z]' or 'go X.Y[.Z]' directive in go.mod" >&2
	exit 1
fi

printf '%s\n' "$version"
