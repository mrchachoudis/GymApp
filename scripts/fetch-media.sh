#!/usr/bin/env bash
# Fetch the exercise demo images and animations.
#
# They are deliberately not in this repository. There are 2,648 files totalling
# about 139 MB, and the upstream dataset's notice asks that anyone
# redistributing the media review its licence first. Storing filenames and
# fetching the bytes keeps the repo small and the licensing question upstream,
# which is the same choice openGym made.
#
# Usage:  scripts/fetch-media.sh [dest]
# Then:   export GYM_MEDIA_DIR=<dest>
#
# Without this the exercise library still works; it just shows no demos.
set -euo pipefail

DEST="${1:-./media}"
SRC="https://github.com/hasaneyldrm/exercises-dataset"

if [ -d "$DEST/gif" ] && [ -d "$DEST/img" ]; then
    echo "media already present in $DEST"
    exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "cloning $SRC (shallow, ~139 MB)..."
git clone --depth 1 "$SRC" "$tmp"

mkdir -p "$DEST"
for kind in gif img; do
    if [ -d "$tmp/$kind" ]; then
        cp -r "$tmp/$kind" "$DEST/"
    elif [ -d "$tmp/media/$kind" ]; then
        cp -r "$tmp/media/$kind" "$DEST/"
    else
        echo "warning: no '$kind' directory in the upstream checkout" >&2
    fi
done

echo "media in $DEST"
echo "set GYM_MEDIA_DIR=$(cd "$DEST" && pwd)"
