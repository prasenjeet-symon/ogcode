#!/bin/sh
# usage: verify.sh <workdir> <base-rev>
set -e
work=$1
rev=$2
git -C "$work" checkout --quiet "$rev" -- test_duration.py
cd "$work"
exec python3 -m unittest -q test_duration
