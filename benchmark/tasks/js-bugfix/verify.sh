#!/bin/sh
# usage: verify.sh <workdir> <base-rev>
set -e
work=$1
rev=$2
git -C "$work" checkout --quiet "$rev" -- slugify.test.js
cd "$work"
exec node --test
