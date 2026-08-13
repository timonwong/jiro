#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo 'usage: container-metadata.sh IMAGE TAG OUTPUT_FILE' >&2
  exit 2
fi

image="$1"
tag="$2"
output_file="$3"
version="${tag#v}"
tags="${image}:${tag}"

if printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  tags="${tags}
${image}:latest"
fi

{
  echo "version=$version"
  echo 'tags<<EOF'
  echo "$tags"
  echo EOF
} >> "$output_file"
