package main

import (
	"encoding/json"
	"math/rand"
)

// Event 是交给 TLC/TLA+ 模型解释的“模型可见事件”。
//
// 它和 SchedulingChoice 不同：Choice 记录 fuzzer 选择了什么，Event 记录系统实际发生了什么。
// 例如一个 Node choice 表示“尝试投递 1->2 的消息”，而对应的 Event 可能包括
// DeliverMessage、BecomeLeader、AdvanceCommitIndex 等真实执行中观察到的动作。
type Event struct {
	Name   string
	Node   uint64 `json:"-"`
	Params map[string]interface{}
	Reset  bool
}

var (
	// Node 表示一次网络调度选择：从 From->To 的消息队列中最多投递 MaxMessages 条。
	Node SchedulingChoiceType = "Node"
	// RandomBoolean/RandomInteger 用于记录可重放的内部随机选择；当前接入较弱，主要是预留。
	RandomBoolean SchedulingChoiceType = "RandomBoolean"
	RandomInteger SchedulingChoiceType = "RandomInteger"
	// StartNode/StopNode 表示在指定 step 恢复或宕机某个 raft 节点。
	StartNode SchedulingChoiceType = "StartNode"
	StopNode  SchedulingChoiceType = "StopNode"
	// ClientRequest 表示在指定 step 注入一个客户端 proposal。
	ClientRequest SchedulingChoiceType = "ClientRequest"
)

type SchedulingChoiceType string

// SchedulingChoice 是 fuzzer 可重放/可变异的调度决策。
//
// 这个结构是搜索空间的主要载体。Guider 不直接修改 Event trace，而是让 Mutator 修改
// SchedulingChoice trace；修改后的 trace 再驱动真实 RaftEnvironment 生成新的 Event trace。
// 与通用版 modelfuzz 相比，这里额外带了 RandomBoolean/RandomInteger，
// 目的是把实现内部随机性也纳入 trace。
type SchedulingChoice struct {
	Type          SchedulingChoiceType
	Node          uint64
	From          uint64
	To            uint64
	MaxMessages   int
	BooleanChoice bool `json:",omitempty"`
	IntegerChoice int  `json:",omitempty"`
	Step          int  `json:",omitempty"`
	Request       int  `json:",omitempty"`
}

func (s *SchedulingChoice) Copy() *SchedulingChoice {
	return &SchedulingChoice{
		Type:          s.Type,
		Node:          s.Node,
		From:          s.From,
		To:            s.To,
		MaxMessages:   s.MaxMessages,
		BooleanChoice: s.BooleanChoice,
		IntegerChoice: s.IntegerChoice,
		Step:          s.Step,
		Request:       s.Request,
	}
}

type Queue[T any] struct {
	// 简单 FIFO，用于模拟网络方向队列和待执行 trace 队列。
	q []T
}

func NewQueue[T any]() *Queue[T] {
	return &Queue[T]{
		q: make([]T, 0),
	}
}

func (q *Queue[T]) Push(elem T) {
	q.q = append(q.q, elem)
}

func (q *Queue[T]) PushAll(elems ...T) {
	q.q = append(q.q, elems...)
}

func (q *Queue[T]) Pop() (elem T, ok bool) {
	if len(q.q) < 1 {
		ok = false
		return
	}
	elem = q.q[0]
	q.q = q.q[1:]
	ok = true
	return
}

func (q *Queue[T]) Size() int {
	return len(q.q)
}

func (q *Queue[T]) Reset() {
	q.q = make([]T, 0)
}

type List[T any] struct {
	// List 是为了方便 JSON 序列化、复制和按下标修改 trace。
	l []T
}

func NewList[T any]() *List[T] {
	return &List[T]{
		l: make([]T, 0),
	}
}

func (l *List[T]) Append(elem T) {
	l.l = append(l.l, elem)
}

func (l *List[T]) Size() int {
	return len(l.l)
}

func (l *List[T]) Get(index int) (elem T, ok bool) {
	if len(l.l) <= index {
		ok = false
		return
	}
	elem = l.l[index]
	ok = true
	return
}

func (l *List[T]) Set(index int, elem T) bool {
	if len(l.l) <= index {
		return false
	}
	l.l[index] = elem
	return true
}

func (l *List[T]) Iter() []T {
	return l.l
}

func (l *List[T]) Reset() {
	l.l = make([]T, 0)
}

func (l *List[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.l)
}

func (l *List[T]) UnmarshalJSON(data []byte) error {
	values := make([]T, 0)
	err := json.Unmarshal(data, &values)
	if err != nil {
		return err
	}
	l.l = values
	return nil
}

type State struct {
	// Repr 是 TLC 返回的可读状态字符串，Key 是 TLC 为状态生成的稳定 hash。
	Repr string
	Key  int64
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a > b {
		return b
	}
	return a
}

func sample(l []int, size int, r *rand.Rand) []int {
	// 从候选 step 中无放回采样，用于随机安排 crash/start/request 的位置。
	if size >= len(l) {
		return l
	}
	indexes := make(map[int]bool)
	for len(indexes) < size {
		i := r.Intn(len(l))
		indexes[i] = true
	}
	samples := make([]int, size)
	i := 0
	for k := range indexes {
		samples[i] = l[k]
		i++
	}
	return samples
}

func intRange(start, end int) []int {
	res := make([]int, end-start)
	for i := start; i < end; i++ {
		res[i-start] = i
	}
	return res
}
