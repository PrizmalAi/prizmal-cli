#!/usr/bin/env bash
# Upload image files to GitHub and print the Markdown that embeds each one.
#
#   upload.sh <owner/repo> <image> [<image> ...]
#
# Needs `gh`, logged in as a user. A GitHub Actions GITHUB_TOKEN is rejected
# by the endpoint this calls.
set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: upload.sh <owner/repo> <image> [<image> ...]" >&2
  exit 2
fi

repo="$1"
shift

# A missing token fails here rather than as a confusing 404 from the upload.
token=$(gh auth token)
repo_id=$(gh api "repos/$repo" --jq .id)

for file in "$@"; do
  if [ ! -f "$file" ]; then
    echo "no such file: $file" >&2
    exit 1
  fi
  name=$(basename "$file")
  # The name travels in the query string, so a space or a "#" would truncate
  # it. Encode it; the server stores the decoded name.
  name_enc=$(jq -rn --arg n "$name" '$n|@uri')
  ext=$(printf '%s' "${name##*.}" | tr '[:upper:]' '[:lower:]')
  case "$ext" in
    png) ct=image/png ;;
    jpg | jpeg) ct=image/jpeg ;;
    gif) ct=image/gif ;;
    webp) ct=image/webp ;;
    svg) ct=image/svg+xml ;;
    *)
      echo "unsupported image extension: $file" >&2
      exit 1
      ;;
  esac
  response=$(curl -sS -X POST \
    "https://uploads.github.com/user-attachments/assets?name=$name_enc&content_type=$ct&repository_id=$repo_id" \
    -H "Authorization: Bearer $token" \
    -H "Accept: application/json" \
    --data-binary "@$file")
  url=$(printf '%s' "$response" | jq -r '.url // empty')
  if [ -z "$url" ]; then
    echo "upload of $name failed: $response" >&2
    exit 1
  fi
  printf '![%s](%s)\n' "$name" "$url"
done
