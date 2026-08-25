# ModelFuzz 工件

论文《Model guided fuzzing of distributed systems》的实验工件。

主要工件可在[这里](https://doi.org/10.5281/zenodo.15753950)获取。

## 系统要求

我们建议为 Docker daemon 或虚拟机分配至少 32GB RAM 内存（细节见下文）。

内存限制来自于需要在内存中存储 TLA+ 模型的大型动作空间，这样才能支持高效的实时模拟和模糊测试。

## 设置

该工件可以通过 Docker 环境运行。

工件中包含预构建的 Docker 镜像。使用下面的命令加载并运行 Docker 镜像：

```bash
wget -o modelfuzz_docker.tar.gz <docker_image_url>
docker load --input modelfuzz_docker.tar.gz
docker run -it modelfuzz:latest /bin/bash
```

也可以使用下面的命令从源码构建镜像。请注意，根据系统配置不同，该过程大约需要 5 到 10 分钟。（需要已安装 Docker）

```bash
wget -o modelfuzz_src.zip <src_repo_url>
unzip modelfuzz_src.zip
cd modelfuzz_artifact
./scripts/docker_build.sh
./scripts/startup.sh
```

在上述两种方式中，脚本都会在容器内启动一个 shell，后续所有实验都将在该容器 shell 中运行。因此，如果退出 shell，生成的任何数据都会被丢弃。必要时，我们会提供记录数据的说明。

### Cloudlab 设置

如果无法访问满足上述硬件要求的机器，我们还提供了可供审稿人使用的 Cloudlab 设置。为了避免去匿名化，这里不共享具体项目的细节。请通过 AEC 主席联系我们以获取访问权限。

获得访问权限后，实例化一个名为 `DedicatedMachine` 的预创建 profile，登录到创建出的节点 shell，然后运行 `sudo su && cd /Fuzzing`。其余步骤如下。

## 快速试运行阶段

在 etcd、redis、2PC 和 Microbenchmark 上运行 ModelFuzz。

启动后，使用下面的命令运行其中一个系统：

```bash
./scripts/kt.sh <redis|etcd|2pc|micro>
```

脚本会运行一分钟，预期输出取决于所选 benchmark。

例如，使用 `etcd` benchmark 时，预期输出如下：

```
# 启动 TLC model checker 服务。
Starting TLC.
......TLC server up and running.
# TLC 服务已就绪，开始运行 etcd benchmark 的测试脚本。
Running test script for etcd
Starting run 1...
# 运行 ModelFuzz，即使用 TLA+ 模型状态覆盖率作为引导，内部名称为 tlcstate。
Running for benchmark: tlcstate
Running iteration: 10/10
Run time: 552.864297ms
# 运行纯随机探索策略。
Running for benchmark: random
Running iteration: 10/10
Run time: 504.945875ms
# 运行 trace coverage 引导策略。
Running for benchmark: traceCov
Running iteration: 10/10
Run time: 496.666775ms
# 运行 line coverage 引导策略。
Running for benchmark: lineCov
Running iteration: 10/10
Run time: 684.012926ms
# line coverage 策略测得的代码行覆盖率。
Percentage of lines covered: 45.847176
Completed running.
# 开始对各策略的结果进行汇总分析。
Starting analysis...
# 分别输出各策略最终的平均 TLA+ 模型状态覆盖数。
Final average state coverage of tlcstate is 104
Final average state coverage of random is 123
Final average state coverage of traceCov is 113
Final average state coverage of lineCov is 109
Completed analysis.
```

对于 `etcd` 和 `redis`，脚本会运行 fuzzer，并测量 Modelfuzz（tlcstate）、Random、Trace coverage（traceCov）和 Line（lineCov）的覆盖率。对于 `2pc`，还会包含 BonusMaxRL 实验；对于 `micro`，只运行 Random、Trace 和 Modelfuzz。

### 故障排查

如果脚本退出并显示 "TLC server failed to start."，建议增加分配给 Docker daemon 或容器的内存。

对于 Docker daemon，请参考[这里](https://docs.docker.com/desktop/settings/mac/#advanced)（Mac）和[这里](https://docs.docker.com/desktop/settings/windows/#advanced)（Windows）。

此外，`scripts/startup.sh` 中包含 `docker run` 命令，可以在其中加入 `-m 30g` 参数以分配额外内存。

如果错误仍然存在，请附上位于 `/tmp/tlc.log` 的日志文件进行反馈。

## 目录结构

不同 benchmark 存放在仓库的不同目录中。

1. `scripts` 包含用于执行评估的辅助脚本（构建和运行）。
2. `tlc-controlled` 包含更新后的 TLC model checker。这些修改引入了一个可通过 HTTP endpoint 访问的实时模拟器。（参见 `tlc-controlled/src/tlc2/TLCServer.java`）
3. `tlc-controlled-with-benchmarks/tla-benchmarks` 包含测试中使用的 TLA+ 模型和配置。
4. `redisraft-fuzzing` 和 `raft-rl-test` 包含用于测试 RedisRaft benchmark 的源码；前者包含 fuzzer，后者包含运行 RL 对比实验的源码。（fuzzer 运行经过插桩的 `redisraft` 源码，该源码位于 `redis-instrumented/redisraft` 文件夹）
5. 类似地，`raft-fuzzing` 和 `dist-rl-testing` 分别包含在 `etcd` benchmark 上运行 fuzzing 和 BonusMaxRL 的源码。经过插桩的 `etcd` 实现位于 `raft-fuzzing/raft` 目录。
6. `2PC-Fuzzing` 包含用于测试 Two-Phase Commit benchmark 的源码。除了 fuzzers 之外，它还包含 BonusMaxRL 的实现。
7. `coyote-concurrency-testing` 包含 Microsoft Coyote 框架以及 MicroBenchmark。`coyote-concurrency-testing/Coyote` 目录包含框架本身和 fuzzing engine，而 `coyote-concurrency-testing/Benchmarks` 目录包含 MicroBenchmark 以及运行它所需的 TestDriver。
8. `modelfuzz`：作为 `go` 库提供的核心算法。（见 Reusability guide）

## 完整评估

完整评估需要运行 fuzzer 多次，持续时间跨越多天。下面详细说明执行脚本。

整体而言，脚本会运行 ModelFuzz、Line coverage guidance、Random exploration、Trace Coverage guidance 和 Waypoint RL，最终获得论文表格中展示的覆盖率数值。

对于 `etcd` 和 `redis` benchmark，脚本分为两个部分。

1. 在 benchmark 上运行 Line、Trace、Random 和 Modelfuzz。
2. 运行 WaypointRL 获取 traces，并在模型上独立测量这些 traces 的覆盖率。

运行第 1 部分：

```bash
./scripts/fuzzing.sh <redis|etcd>
```

结果会打印到 shell 中。或者，完整数据会存储在 `redisraft-fuzzing/results`（redis）和 `raft-fuzzing/results`（etcd）中。

随后运行第 2 部分：

```bash
./scripts/waypoint_rl.sh <redis|etcd>
```

覆盖率结果会被打印出来，完整数据会存储在 `redisraft-fuzzing/rl_cov`（redis）和 `raft-fuzzing/rl_cov`（etcd）中。

对于 `Two Phase Commit` benchmark，脚本会运行 Line、Trace、Random、Modelfuzz 和 Waypoint RL 的完整实验。

```bash
./scripts/fuzzing.sh 2pc
```

对于 `MicroBenchmark`，脚本会运行 Trace、Random 和 Modelfuzz 的完整实验。

```bash
./scripts/fuzzing.sh micro
```

## 可复用性指南

该工件包含多个针对不同 benchmark 和不同语言定制的 Modelfuzz 实现。不过，核心算法以一个带有抽象接口的 `go` 库形式提供，可用于测试多种语言编写的实现。该库及其文档可在 `modelfuzz` 目录中找到。
