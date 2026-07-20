module github.com/SuzumiyaHaruki/modelfuzz-ng

go 1.26

toolchain go1.26.4

require go.etcd.io/raft/v3 v3.7.0

require google.golang.org/protobuf v1.36.11

replace go.etcd.io/raft/v3 => ../raft
