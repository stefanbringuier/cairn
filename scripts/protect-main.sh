#!/bin/sh
# Maintainer action: require PRs and passing CI, including for administrators.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$script_dir/.."
repo=$(gh repo view --json nameWithOwner --jq '.nameWithOwner')
gh api --method PUT "repos/$repo/branches/main/protection" \
  --input "$script_dir/../.github/main-protection.json" \
  --jq '{required_status_checks, enforce_admins, required_pull_request_reviews, required_linear_history, allow_force_pushes, allow_deletions, required_conversation_resolution}'
