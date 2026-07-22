package runtime

import (
	"fmt"
	"math"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// queuedMessage 保存 Runtime 队列中的消息及其首次入队时间。
type queuedMessage struct {
	message    core.Message
	enqueuedAt core.LogicalTime
}

// network 是一次执行独占的确定性网络。它按有向 Link 保存消息，并负责分配
// 全局 MessageID 和 Link 内单调递增的 Sequence。
type network struct {
	queues           map[core.LinkID][]queuedMessage
	nextMessageID    core.MessageID
	lastLinkSequence map[core.LinkID]uint64
	partition        *core.NetworkPartition
	nodeGroups       map[core.NodeID]int
}

func (n *network) len() int {
	total := 0
	for _, queue := range n.queues {
		total += len(queue)
	}
	return total
}

func newNetwork() *network {
	n := &network{}
	n.reset()
	return n
}

func (n *network) reset() {
	n.queues = make(map[core.LinkID][]queuedMessage)
	n.nextMessageID = 1
	n.lastLinkSequence = make(map[core.LinkID]uint64)
	n.partition = nil
	n.nodeGroups = nil
}

func (n *network) activatePartition(partition core.NetworkPartition, nodes []core.NodeID) error {
	if n.partition != nil {
		return fmt.Errorf("%w: a partition is already active", ErrPartitionState)
	}
	if !partition.Covers(nodes) {
		return fmt.Errorf("%w: partition must cover every runtime node exactly once", ErrPartitionState)
	}
	copy := partition.Normalized()
	n.partition = &copy
	n.nodeGroups = make(map[core.NodeID]int, len(nodes))
	for groupIndex, group := range copy.Groups {
		for _, node := range group {
			n.nodeGroups[node] = groupIndex
		}
	}
	return nil
}

func (n *network) heal() error {
	if n.partition == nil {
		return fmt.Errorf("%w: no partition is active", ErrPartitionState)
	}
	n.partition = nil
	n.nodeGroups = nil
	return nil
}

func (n *network) isBlocked(link core.LinkID) bool {
	if n.partition == nil {
		return false
	}
	from, fromExists := n.nodeGroups[link.From]
	to, toExists := n.nodeGroups[link.To]
	return fromExists && toExists && from != to
}

func (n *network) partitionObservation() *core.NetworkPartition {
	if n.partition == nil {
		return nil
	}
	copy := n.partition.Copy()
	return &copy
}

// registerOutbound 将 Adapter 新产生的无 ID 消息注册到确定性网络。
func (n *network) registerOutbound(message core.Message, at core.LogicalTime) (core.Message, error) {
	return n.register(message, 0, at)
}

func (n *network) register(message core.Message, parent core.MessageID, at core.LogicalTime) (core.Message, error) {
	if err := message.ValidateOutbound(); err != nil {
		return core.Message{}, fmt.Errorf("%w: invalid outbound message: %v", ErrAdapterContract, err)
	}
	if !n.nextMessageID.Valid() {
		return core.Message{}, fmt.Errorf("%w: message ID space exhausted", ErrIDExhausted)
	}

	link := message.Link()
	lastSequence := n.lastLinkSequence[link]
	if lastSequence == math.MaxUint64 {
		return core.Message{}, fmt.Errorf("%w: link sequence exhausted for %s", ErrIDExhausted, link)
	}

	message.ID = n.nextMessageID
	message.Sequence = lastSequence + 1
	message.ParentID = parent
	if err := message.Validate(); err != nil {
		return core.Message{}, fmt.Errorf("%w: registered message is invalid: %v", ErrAdapterContract, err)
	}

	if n.nextMessageID == core.MessageID(math.MaxUint64) {
		n.nextMessageID = 0
	} else {
		n.nextMessageID++
	}
	n.lastLinkSequence[link] = message.Sequence
	n.queues[link] = append(n.queues[link], queuedMessage{
		message:    message.Copy(),
		enqueuedAt: at,
	})
	return message.Copy(), nil
}

// resolve 要求 MessageID、Link 和当前 Position 同时匹配。
func (n *network) resolve(id core.MessageID, selector core.MessageSelector) (queuedMessage, error) {
	if !id.Valid() {
		return queuedMessage{}, fmt.Errorf("%w: message ID is zero", ErrMessageUnavailable)
	}
	if err := selector.Validate(); err != nil {
		return queuedMessage{}, fmt.Errorf("%w: invalid selector: %v", ErrMessageUnavailable, err)
	}
	queue := n.queues[selector.Link]
	if selector.Position >= len(queue) {
		return queuedMessage{}, fmt.Errorf(
			"%w: %s position %d is outside queue length %d",
			ErrMessageUnavailable, selector.Link, selector.Position, len(queue),
		)
	}
	selected := queue[selector.Position]
	if selected.message.ID != id {
		return queuedMessage{}, fmt.Errorf(
			"%w: selector points to %s, not requested %s",
			ErrMessageUnavailable, selected.message.ID, id,
		)
	}
	return queuedMessage{message: selected.message.Copy(), enqueuedAt: selected.enqueuedAt}, nil
}

func (n *network) remove(id core.MessageID, selector core.MessageSelector) (core.Message, error) {
	selected, err := n.resolve(id, selector)
	if err != nil {
		return core.Message{}, err
	}

	queue := n.queues[selector.Link]
	copy(queue[selector.Position:], queue[selector.Position+1:])
	queue[len(queue)-1] = queuedMessage{}
	queue = queue[:len(queue)-1]
	if len(queue) == 0 {
		delete(n.queues, selector.Link)
	} else {
		n.queues[selector.Link] = queue
	}
	return selected.message, nil
}

// duplicate 保留原消息，并把带新 ID、Sequence 和 ParentID 的副本追加到同一 Link 尾部。
func (n *network) duplicate(id core.MessageID, selector core.MessageSelector, at core.LogicalTime) (core.Message, error) {
	selected, err := n.resolve(id, selector)
	if err != nil {
		return core.Message{}, err
	}

	duplicate := selected.message.Copy()
	duplicate.ID = 0
	duplicate.Sequence = 0
	duplicate.ParentID = 0
	return n.register(duplicate, selected.message.ID, at)
}

// observations 返回网络中所有消息的快照，按 Link 和 Position 排序。
func (n *network) observations() []core.MessageObservation {
	links := make([]core.LinkID, 0, len(n.queues))
	for link := range n.queues {
		links = append(links, link)
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].From == links[j].From {
			return links[i].To < links[j].To
		}
		return links[i].From < links[j].From
	})

	result := make([]core.MessageObservation, 0)
	for _, link := range links {
		for position, queued := range n.queues[link] {
			message := queued.message
			result = append(result, core.MessageObservation{
				ID:            message.ID,
				From:          message.From,
				To:            message.To,
				SenderEpoch:   message.SenderEpoch,
				LinkSequence:  message.Sequence,
				ParentID:      message.ParentID,
				Position:      position,
				EnqueuedAt:    queued.enqueuedAt,
				TypeHint:      message.TypeHint,
				PayloadDigest: message.PayloadDigest,
				Metadata:      cloneMetadata(message.Metadata),
				Blocked:       n.isBlocked(message.Link()),
			})
		}
	}
	return result
}

func cloneMetadata(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
