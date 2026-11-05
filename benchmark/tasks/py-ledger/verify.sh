#!/bin/sh
# usage: verify.sh <workdir> <base-rev>
# Restores the pristine tests from the fixture commit, then runs the suite.
set -e
work=$1
rev=$2
git -C "$work" checkout --quiet "$rev" -- test_ledger.py
cd "$work"
exec python3 -m unittest -q test_ledger
