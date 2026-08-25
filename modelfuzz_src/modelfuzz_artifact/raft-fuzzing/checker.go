package main

import (
	"bytes"

	"github.com/zeu5/raft-fuzzing/raft"
	pb "github.com/zeu5/raft-fuzzing/raft/raftpb"
)

// Checker 是额外的 bug oracle，不参与模型覆盖引导，只在 iteration 结束后判断执行是否异常。
//
// Guider 关注“有没有覆盖新模型状态”，Checker 关注“这次执行是否违反实现层不变量”。
// 因此 Checker 失败不会改变 TLC 覆盖统计，但会被记录到 fuzzer.stats["buggy_executions"]。
type Checker func(*RaftEnvironment) bool

func SerializabilityChecker() func(*RaftEnvironment) bool {
	return func(re *RaftEnvironment) bool {
		// 找到所有节点共同提交到的最小 commit index，只比较这段前缀。
		// 如果某些节点落后很多，只比较共同提交前缀可以避免把正常复制延迟误报为 bug。
		minCommit := 100
		for _, state := range re.curStates {
			if state.Commit < uint64(minCommit) {
				minCommit = int(state.Commit)
			}
		}
		if minCommit == 0 {
			return true
		}
		logs := make([][]pb.Entry, 0)
		for _, storage := range re.storages {
			l, err := storage.Entries(1, uint64(minCommit)+1, 100)
			if err != nil {
				return false
			}
			logs = append(logs, l)
		}

		for i := 0; i < minCommit; i++ {
			// Raft 安全性要求已提交日志前缀在所有节点上完全一致。
			l := logs[0][i]
			for j := 1; j < len(logs); j++ {
				cur := logs[j][i]
				if cur.Term != l.Term || cur.Index != l.Index || !bytes.Equal(cur.Data, l.Data) {
					return false
				}
			}
		}
		return true
	}
}

func SingleLeader() func(*RaftEnvironment) bool {
	return func(re *RaftEnvironment) bool {
		// 简单检查同一观察点最多只有一个 leader。
		// 这是很粗粒度的 oracle，没有区分 term；更严谨的检查通常要按 term 判断。
		leaders := 0
		for _, s := range re.curStates {
			if s.RaftState == raft.StateLeader {
				leaders += 1
			}
		}
		return leaders <= 1
	}
}
