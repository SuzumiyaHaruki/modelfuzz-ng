#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
server_jar=$($script_dir/build.sh)
tlc_jar="$script_dir/.cache/tla2tools-1.8.0.jar"

exec java -cp "$server_jar:$tlc_jar" org.modelfuzzng.tlc.StrictTLCServer "$@"
