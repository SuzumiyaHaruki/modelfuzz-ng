#!/bin/bash

# Check if exactly one argument is provided
if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <benchmark>"
    echo "Allowed values for <benchmark>: redis, etcd"
    exit 1
fi


benchmark="$1"

# Validate argument
if [[ "$benchmark" != "redis" && "$benchmark" != "etcd" ]]; then
    echo "Error: Invalid benchmark '$benchmark'. Allowed values are 'redis' or 'etcd'."
    exit 1
fi

start_tlc () {
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
}

stop_tlc () {
    killall tlc_run.sh
    killall java
}

if [[ "$benchmark" == "redis" ]]; then 
    pushd /Fuzzing > /dev/null
    cd raft-rl-test
    ./run_bonusmaxrl_redis.sh 20000
    cd ..
    start_tlc
    cd redisraft-fuzzing
    mkdir rl_cov
    ./redisraft-fuzzing measure --out rl_cov --traces ../raft-rl-test/results/eventTraces
    stop_tlc
    popd > /dev/null
then

if [[ "$benchmark" == "etcd" ]]; then
    pushd /Fuzzing > /dev/null
    cd dist-rl-testing
    ./run_bonusmaxrl_etcd.sh 20000
    cd ..
    start_tlc
    cd raft-fuzzing
    mkdir fuzz_coverage
    export GOCOVERDIR=fuzz_coverage
    ./raft-fuzzing measure --out rl_cov --traces ../dist-rl-testing/results/event-traces/0/BonusMax
    stop_tlc
    popd > /dev/null
fi
