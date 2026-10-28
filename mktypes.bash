#!/usr/bin/env bash

set -euo pipefail

GOOSARCH="${GOOS:-}_${GOARCH:-}"
case "$GOOSARCH" in
_* | *_)
	echo "undefined target: $GOOSARCH (set GOOS and GOARCH)" 1>&2
	exit 1
	;;
esac

# Prepare every ABI file before replacing any existing generated source.
godefsDir=$(mktemp -d .godefs.XXXXXX)
trap 'rm -rf "$godefsDir"' EXIT

mkdir "$godefsDir/obj"

generate() {
	{
		printf '//go:build %s\n\n' "$3"
		go tool cgo -objdir "$godefsDir/obj" -godefs "$1"
	} | gofmt > "$godefsDir/$2"
}

typesFile="ztypes_$GOARCH.go"
typesConstraint=$GOARCH
case $GOARCH in
mips|mipsle|mips64|mips64le)
	typesFile=ztypes_mipsx.go
	typesConstraint='(mips || mipsle || mips64 || mips64le) && linux'
	;;
riscv|riscv64)
	typesFile=ztypes_riscvx.go
	typesConstraint='riscv || riscv64'
	;;
sparc|sparc64)
	typesFile=ztypes_sparcx.go
	typesConstraint='sparc || sparc64'
	;;
esac

generate types.go "$typesFile" "$typesConstraint"

case $GOOS in
freebsd|dragonfly)
	generate "types_$GOOS.go" "ztypes_$GOOSARCH.go" "$GOOS && $GOARCH"
	;;
netbsd)
	generate types_netbsd.go ztypes_netbsd_32bit_int.go '(386 || amd64 || arm || arm64) && netbsd'
	;;
openbsd)
	generate types_openbsd.go ztypes_openbsd_32bit_int.go openbsd
	;;
esac

for file in "$godefsDir"/*.go; do
	mv "$file" .
done
