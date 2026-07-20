package etcdraft

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// deterministicRand 是 Adapter 私有的稳定伪随机流。它不依赖 Go 标准库
// math/rand 的实现，因此相同的 Seed、节点和 Epoch 会产生相同序列。
// 每个 RawNode 持有独立实例，节点的执行顺序不会消耗其他节点的随机数。
type deterministicRand struct {
	state uint64
}

func newNodeRand(seed int64, node core.NodeID, epoch core.NodeEpoch) *deterministicRand {
	var input [24]byte
	binary.LittleEndian.PutUint64(input[0:8], uint64(seed))
	binary.LittleEndian.PutUint64(input[8:16], uint64(node))
	binary.LittleEndian.PutUint64(input[16:24], uint64(epoch))
	digest := sha256.Sum256(input[:])
	return &deterministicRand{state: binary.LittleEndian.Uint64(digest[:8])}
}

func (r *deterministicRand) nextUint64() uint64 {
	// SplitMix64：状态小、速度快，适合确定性调度，不用于密码学用途。
	r.state += 0x9e3779b97f4a7c15
	value := r.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (r *deterministicRand) Intn(n int) int {
	if n <= 0 {
		panic("deterministicRand.Intn called with non-positive bound")
	}
	bound := uint64(n)
	// 拒绝会造成取模偏差的高位区间。对选举超时这样的小 n，通常一次即可返回。
	limit := ^uint64(0) - (^uint64(0) % bound)
	for {
		value := r.nextUint64()
		if value < limit {
			return int(value % bound)
		}
	}
}
