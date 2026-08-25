#!/bin/bash

# Check if exactly one argument is provided
if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <benchmark>"
    echo "Allowed values for <benchmark>: redis, etcd, 2pc, micro"
    exit 1
fi

benchmark="$1"

# Validate argument
if [[ "$benchmark" != "redis" && "$benchmark" != "etcd" && "$benchmark" != "2pc" && "$benchmark" != "micro" ]]; then
    echo "Error: Invalid benchmark '$benchmark'. Allowed values are 'redis' or 'etcd'."
    exit 1
fi

echo "Starting TLC."
# tlc_run.sh Raft RAFT_5_3 > /dev/null 2>&1 &
if [[ "$benchmark" == "redis" || "$benchmark" == "etcd" ]]; then
    tlc_run.sh Raft RAFT_5_3 -mapperparams "name=raft;abstract" > /tmp/tlc.log 2>&1 &
    tlc_pid=$!
elif [[ "$benchmark" == "2pc" ]]; then
    tlc_run.sh ignored ignored --2pc > /dev/null 2>&1 &
    tlc_pid=$!
elif [[ "$benchmark" == "micro" ]]; then
    tlc_run.sh MicroBenchmark MB_6_40 > /dev/null 2>&1 &
    tlc_pid=$!
else
    echo "Unknown benchmark: $benchmark"
fi

until curl --output /dev/null --silent --head --fail http://0.0.0.0:2023/health; do
    printf '.'
    sleep 5
    if [ ! -d "/proc/$tlc_pid" ]; then
        echo "TLC server failed to start."
        exit 1
    fi
done

echo "TLC server up and running."

echo "Running line, modelfuzz, trace and random coverage for $benchmark"

if [ "$benchmark" == "redis" ]; then
    pushd /Fuzzing/redisraft-fuzzing > /dev/null
    ./run_all.sh
    popd > /dev/null
fi

if [ "$benchmark" == "etcd" ]; then
    pushd /Fuzzing/raft-fuzzing > /dev/null
    ./run_all.sh
    popd > /dev/null
fi

if [ "$benchmark" == "2pc" ]; then
    pushd /Fuzzing/2PC-Fuzzing > /dev/null
    bash run-all.sh
    popd > /dev/null
fi

if [ "$benchmark" == "micro" ]; then
    pushd /Fuzzing/coyote-concurrency-testing/Benchmarks/TestDriver > /dev/null
    bash run.sh
    popd > /dev/null
fi

killall tlc_run.sh
killall java