#!/bin/sh
# usage: verify.sh <workdir> <base-rev>
set -e
work=$1
rev=$2
git -C "$work" checkout --quiet "$rev" -- textutil_test.go
cd "$work"
exec go test ./...
