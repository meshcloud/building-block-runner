#!/usr/bin/env bash
# Run this script once after cloning to configure jj for this repository.
# It sets up immutability rules so that only main (and its
# remote counterpart) is protected. Feature branches remain fully mutable
# even after being pushed to origin, allowing commits to be reordered,
# amended, or rebased freely.

set -euo pipefail

echo "Configuring jj for building-block-runner..."

jj config set --repo 'revset-aliases."immutable_heads()"' \
  "present(main) | main@origin | tags()"

echo "Done. main is now immutable; feature branches are mutable."
