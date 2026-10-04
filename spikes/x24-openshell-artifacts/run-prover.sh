#!/bin/sh
# Runs openshell-prover 0.1.2 (release asset, checksum-verified) against the
# sample policies in policies/. Each case is <name>.yaml, checked against
# <name>.boundary.yaml when that exists, else boundary.yaml.
# Usage: run-prover.sh <path-to-openshell-prover>
set -u
prover=$1
dir=$(cd "$(dirname "$0")/policies" && pwd)
for f in "$dir"/*.yaml; do
  name=$(basename "$f" .yaml)
  case "$name" in
    boundary | *.boundary) continue ;;
  esac
  boundary="$dir/boundary.yaml"
  [ -f "$dir/$name.boundary.yaml" ] && boundary="$dir/$name.boundary.yaml"
  echo "== $name (against $(basename "$boundary"))"
  "$prover" check "$f" --boundary "$boundary"
  echo "exit=$?"
done
echo "== candidate-exceeds, json"
"$prover" check "$dir/candidate-exceeds.yaml" --boundary "$dir/boundary.yaml" -o json
echo "exit=$?"
