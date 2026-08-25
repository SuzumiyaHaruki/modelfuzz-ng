#!/bin/bash

# 1. Compile TLC
tlc_compile.sh

# # 2. Compile the go repositories for running experiments
cd /Fuzzing/redisraft-fuzzing
go build -o redisraft-fuzzing .

cd /Fuzzing/raft-fuzzing
go build -cover -covermode=atomic -o raft-fuzzing .

cd /Fuzzing/dist-rl-testing
go build -o dist-rl-testing .

cd /Fuzzing/raft-rl-test
go build -o raft-rl-test .

cd /Fuzzing/2PC-Fuzzing
go build -cover -o 2pc-fuzzing .

# 3. Compile redisraft to measure line coverage

## 3.1 Install redisraft dependencies 

# Json-C
cd /Fuzzing
git clone https://github.com/json-c/json-c
mkdir json-c-build
cd json-c-build
cmake ../json-c
make
make install

# Libcurl
apt-get install -y libcurl4-openssl-dev

## 3.2 Compile redisraft
cd /Fuzzing/redis-instrumented/redisraft
if [ -d "build" ]; then
    rm -rf build
fi
mkdir build
cd build
cmake ..
make redisraft
cd /Fuzzing

cd /Fuzzing/coyote-concurrency-testing/Coyote
dotnet build
cd ../Benchmarks/MicroBenchmark
dotnet build
cd ../TestDriver
dotnet build