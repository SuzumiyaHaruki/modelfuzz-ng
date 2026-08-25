# ModelFuzz artifact

Artifact for the paper "Model guided fuzzing of distributed systems"

The main artifact is available [here](https://doi.org/10.5281/zenodo.15753950)

## System requirements

We recommend a system with at least 32gb of RAM memory to be allocated to the docker daemon or the VM (details follow)

The memory constraint is due to storing a large action space of a TLA+ model in memory which enables efficient real time simulation and fuzzing.

## Setup

The artifact can be run using a docker setup.

A prebuild docker image can be found bundled within the artifact. To load and run the docker image use the instructions below. 

```bash
wget -o modelfuzz_docker.tar.gz <docker_image_url>
docker load --input modelfuzz_docker.tar.gz
docker run -it modelfuzz:latest /bin/bash
```

Alternatively, the image can be built from source using the following commands. Note that the process takes about 5-10 minutes depending on the configuration of the system. (Requires docker installed)

```bash
wget -o modelfuzz_src.zip <src_repo_url>
unzip modelfuzz_src.zip
cd modelfuzz_artifact
./scripts/docker_build.sh
./scripts/startup.sh
```

In both scenarios, the script will invoke a shell inside the container and all subsequent experiments will be run inside the shell of the contained. Therefore, any data generated will be discarded if you exit the shell. When necessary, we provide instructions to record the data.

### Cloudlab setup

Alternatively, if access to such hardware is not available. We have a cloudlab setup that the reviewers can use. We do not share the details of the exact project here to avoid de-anonymization. Please contact throught the AEC chairs to obtain access.

Once access is gained, instantiate a pre created profile named `DedicatedMachine`, login to the shell of the node that is created and run `sudo su && cd /Fuzzing`. The rest of the steps follow as below.

## Kick-the-tires phase

Run ModelFuzz on etcd, redis, 2PC and Microbenchmark

Once started, run one of the systems using the following command

```bash
./scripts/kt.sh <redis|etcd|2pc|micro>
```

The scripts runs for a minute and the expected output depends on the benchmark. 

For example with `etcd` benchmark the expected output is as follows, 

```
Starting TLC.
......TLC server up and running.
Running test script for etcd
Starting run 1...
Running for benchmark: tlcstate
Running iteration: 10/10
Run time: 552.864297ms
Running for benchmark: random
Running iteration: 10/10
Run time: 504.945875ms
Running for benchmark: traceCov
Running iteration: 10/10
Run time: 496.666775ms
Running for benchmark: lineCov
Running iteration: 10/10
Run time: 684.012926ms
Percentage of lines covered: 45.847176
Completed running.
Starting analysis...
Final average state coverage of tlcstate is 104
Final average state coverage of random is 123
Final average state coverage of traceCov is 113
Final average state coverage of lineCov is 109
Completed analysis.
```

For `etcd` and `redis`, the script runs the fuzzer and measures coverage for Modelfuzz (tlcstate), Random, Trace coverage (traceCov) and Line (lineCov). With `2pc` it also includes the experiment with BonusMaxRL, and only runs Random, Trace and Modelfuzz for `micro`.

### Troubleshooting

If you encounter the script exiting with the message "TLC server failed to start." then we recommend increasing the memory allocated to the docker daemon or the container. 

For the daemon, refer [here](https://docs.docker.com/desktop/settings/mac/#advanced) (for Mac) and [here](https://docs.docker.com/desktop/settings/windows/#advanced) (for windows)

Additionally, the `scripts/startup.sh` contains the `docker run` command which can be updated to include the parameter `-m 30g` for additional memory allocation.

In case the error persists, please report with the log file located in /tmp/tlc.log

## Directory structure 

The different benchmarks are stored in different directories of the repository.

1. `scripts` contains helper scripts to perform the evaluation (Building and running)
2. `tlc-controlled` contains the updated TLC model checker. The changes introduce a real-time simulator accessible throught a HTTP endpoint. (Refer to `tlc-controlled/src/tlc2/TLCServer.java`)
3. `tlc-controlled-with-benchmarks/tla-benchmarks` contains the TLA+ models and configurations that we use to test.
4. `redisraft-fuzzing` and `raft-rl-test` contains the source code for testing the Redisraft benchmark with the former containing the fuzzer and the later containing the source to run RL comparison. (The fuzzer runs an instrumented version of the `redisraft` source which can be found in `redis-instrumented/redisraft` folder)
5. Similarly `raft-fuzzing` and `dist-rl-testing` contains the source to run fuzzing (and BonusMaxRL respectively) on `etcd` benchmark. The instrumented `etcd` implementation can be found within `raft-fuzzing/raft` directory.
6. `2PC-Fuzzing` contains the source code for testing the Two-Phase Commit benchmark. In addition to the fuzzers, it also includes the implementation of BonusMaxRL.
7. `coyote-concurrency-testing` includes the Microsoft Coyote framework along with the MicroBenchmark. The `coyote-concurrency-testing/Coyote` directory contains the framework itself and the fuzzing engine, while the `coyote-concurrency-testing/Benchmarks` directory includes the MicroBenchmark and the TestDriver required to run it.
8. `modelfuzz` - Core algorithm as a `go` library. (See Reusability guide)

## Full evaluation

The full evaluation involves running the fuzzer for several runs that span multiple days. The execution scripts are detailed below. 

Overall the scripts run ModelFuzz, Line coverage guidance, Random exploration, Trace Coverage guidance and Waypoint RL to finally obtain the coverage values presented in the table. 

For `etcd` and `redis` benchmarks, the scripts are split into two parts.

1. Running Line, Trace, Random and Modelfuzz on the benchmark
2. Running WaypointRL to obtain traces and independantly measure coverage of the traces on the model.

To run 1, 

```bash
./scripts/fuzzing.sh <redis|etcd>
```

The results will be printed out to the shell. Alternatively, the complete data will be stored in `redisraft-fuzzing/results` (for redis) and `raft-fuzzing/results` (for etcd)

Subsequently to run 2, 

```bash
./scripts/waypoint_rl.sh <redis|etcd>
```

The coverage results will be printout with the full data stored in `redisraft-fuzzing/rl_cov` (for redis) and `raft-fuzzing/rl_cov` (for etcd)

For `Two Phase Commit` benchmark, the script runs the full experiments for Line, Trace, Random, Modelfuzz and Waypoint RL.

```bash
./scripts/fuzzing.sh 2pc
```

For `MicroBenchmark`, the script runs the full experiments for Trace, Random and Modelfuzz.

```bash
./scripts/fuzzing.sh micro
```

## Reusability guide

The artifact contains multiple implementations of the Modelfuzz tailored for the different benchmarks and multiple languages. However, the core algorithm is available as a `go` library with abstract interfaces that allow testing implementations in many different languages. The library and the documentation can be found in the `modelfuzz` directory.
