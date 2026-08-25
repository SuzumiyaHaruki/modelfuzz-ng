# Model-Guided Fuzzing using Coyote Framework

This repository contains Microsoft's Coyote project as a submodule extended with ModelFuzz testing algorithm and a set of Coyote program benchmarks.

## Contents of the repository:

- `Coyote` - keeps a copy of [Microsoft's Coyote framework] (https://github.com/microsoft/coyote) (commit b6d3a83). 
	- 	This repository extends Coyote with the ModelFuzz testing algorithm.

- `Benchmarks` - keeps Coyote benchmark applications
	- `MicroBenchmark` a simple microbenchmark application 
	- `TwoPhaseCommit` a simple implementation of TwoPhaseCommit protocol
	- `Microsoft.Coyote.Samples.CloudMessaging` from Coyote samples 
	- `TestDriver` a testing wrapper to run the application with Coyote


## Concurrency testing of a benchmark application:

- Build the `Coyote` project. This will create the build files in `Coyote/bin`.
- Remove `Benchmarks/bin` if nonempty, and build the benchmark project `MicroBenchmark`. This will create the build files in `Benchmarks/bin`. 
- Run `Benchmarks/TestDriver` project to test the benchmark application.

