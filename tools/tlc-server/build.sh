#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
version=1.8.0
archive="$script_dir/.cache/tla2tools-$version.jar"
archive_url="https://github.com/tlaplus/tlaplus/releases/download/v$version/tla2tools.jar"
archive_sha256="cc4803dce2a8ffaf0f5920a9dc39df4b5ee34ab4cb53fb58ac557277a7e516b3"
classes="$script_dir/build/classes"
server_jar="$script_dir/build/modelfuzz-ng-tlc-server.jar"

mkdir -p "$script_dir/.cache" "$script_dir/build"
if [[ ! -f "$archive" ]]; then
  curl --fail --silent --show-error --location --output "$archive.tmp" "$archive_url"
  mv "$archive.tmp" "$archive"
fi

actual_sha256=$(sha256sum "$archive" | awk '{print $1}')
if [[ "$actual_sha256" != "$archive_sha256" ]]; then
  echo "tla2tools checksum mismatch: got $actual_sha256" >&2
  exit 1
fi

rm -rf "$classes"
mkdir -p "$classes"
find "$script_dir/src/main/java" -name '*.java' -print0 |
  xargs -0 javac --release 17 -cp "$archive" -d "$classes"
jar --create --file "$server_jar" --main-class org.modelfuzzng.tlc.StrictTLCServer -C "$classes" .

echo "$server_jar"
