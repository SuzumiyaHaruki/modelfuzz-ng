package main

import (
	"math/rand"
	"time"
)

// Strategy 是早期抽象出的调度策略接口。
// 当前 fuzzer 主路径更多直接使用 trace/mutator，Strategy 在 main 配置中存在感较弱。
type Strategy interface {
	GetNextNode([]uint64) uint64
	GetRandomBoolean() bool
	GetRandomInteger(int) int
}

// RandomStrategy 从候选集合中随机选择节点和随机值。
type RandomStrategy struct {
	rand *rand.Rand
}

var _ Strategy = &RandomStrategy{}

func NewRandomStrategy() *RandomStrategy {
	return &RandomStrategy{
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (r *RandomStrategy) GetNextNode(available []uint64) uint64 {
	randIndex := r.rand.Intn(len(available))
	return available[randIndex]
}

func (r *RandomStrategy) GetRandomBoolean() bool {
	return r.rand.Intn(2) == 0
}

func (r *RandomStrategy) GetRandomInteger(max int) int {
	return r.rand.Intn(max)
}

type RoundRobinStrategy struct {
	*RandomStrategy
	NumNodes int
	curNode  uint64
}

var _ Strategy = &RoundRobinStrategy{}

func NewRoundRobinStrategy(numNodes int) *RoundRobinStrategy {
	return &RoundRobinStrategy{
		RandomStrategy: NewRandomStrategy(),
		NumNodes:       numNodes + 1,
		curNode:        0,
	}
}

func (r *RoundRobinStrategy) GetNextNode(available []uint64) uint64 {
	// 在可用节点集合中轮询选择，跳过当前不可用的节点。
	m := make(map[uint64]bool)
	for _, n := range available {
		m[n] = true
	}
	next := r.curNode
	_, exists := m[next]
	for !exists {
		next = (next + 1) % uint64(r.NumNodes)
		_, exists = m[next]
	}
	r.curNode = (next + 1) % uint64(r.NumNodes)
	return next
}
