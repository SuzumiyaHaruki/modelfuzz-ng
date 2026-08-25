# 2PC-fuzzing
Fuzzing the Two-Phase commit protocol.

# Build and Run Instructions
To build the project use: 

```go build -cover -o 2pc-fuzzing .```

To run you can use: 

```./2pc-fuzzing -type ModelFuzz -duration 1 -filename mf_stats```

For more information on the parameters you can use: 

```./2pc-fuzzing -help```

For replication of the original experiments, use: 

```bash run-all.sh```
