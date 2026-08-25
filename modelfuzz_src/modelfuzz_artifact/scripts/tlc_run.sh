#!/bin/bash

# Check at least algo and model are provided
if [[ $# -lt 2 ]]; then
    echo "Usage: tlc_run <algo> <model> [--2pc] <params>"
    exit 1
fi

# Flag to decide whether to use TPCL_EX model
USE_TPC=false
PARAMS=()

# Parse --2pc flag from arguments starting from $3
for arg in "${@:3}"; do
    if [[ "$arg" == "--2pc" ]]; then
        USE_TPC=true
    else
        PARAMS+=("$arg")
    fi
done

JAR=dist/tla2tools_server.jar

# Choose file paths
if $USE_TPC; then
    TLA_FILE="../2PC-Fuzzing/tla/TPCL_Ex.tla"
    TLA_CONFIG="../2PC-Fuzzing/tla/TPCL_Ex.cfg"
else
    TLA_FILE="../tlc-controlled-with-benchmarks/tla-benchmarks/$1/model/$2.tla"
    TLA_CONFIG="../tlc-controlled-with-benchmarks/tla-benchmarks/$1/model/$2.cfg"
fi

cd /Fuzzing/tlc-controlled || exit 1
java -Xms30g -Xmx30g -jar "$JAR" "$TLA_FILE" -config "$TLA_CONFIG" "${PARAMS[@]}"
