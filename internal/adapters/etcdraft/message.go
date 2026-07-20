package etcdraft

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

const deliveredMessageEvent = "raft.message_delivered"

// deliveredMessageEffect 保存 TLA+ Raft 模型需要的消息字段。
// Entries 内部字段沿用原 ModelFuzz RaftActionMapper 使用的大小写。
func deliveredMessageEffect(at core.LogicalTime, message *raftpb.Message) core.Effect {
	entries := make([]map[string]any, len(message.GetEntries()))
	for i, entry := range message.GetEntries() {
		projected := map[string]any{
			"Term":  entry.GetTerm(),
			"Index": entry.GetIndex(),
			"Type":  entry.GetType().String(),
		}
		// 原 RaftActionMapper 把缺失的 Data 解释为值 0，正好对应
		// etcd-raft 的空 no-op entry；空字符串反而无法被解析成整数。
		if len(entry.GetData()) != 0 {
			projected["Data"] = string(entry.GetData())
		}
		entries[i] = projected
	}
	params := map[string]any{
		"type":     message.GetType().String(),
		"from":     message.GetFrom(),
		"to":       message.GetTo(),
		"term":     message.GetTerm(),
		"log_term": message.GetLogTerm(),
		"index":    message.GetIndex(),
		"commit":   message.GetCommit(),
		"reject":   message.GetReject(),
		"entries":  entries,
	}
	return core.Effect{
		At:   at,
		Kind: core.EffectModelEvent,
		ModelEvent: &core.ModelEvent{
			Name:   deliveredMessageEvent,
			Node:   core.NodeID(message.GetTo()),
			Params: params,
		},
	}
}

func messageDigest(message *raftpb.Message) (string, error) {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// outboundMessage 将 Raft 消息复制成尚未注册 ID 的 core.Message。
// Runtime 随后负责分配 MessageID、链路序号并加入确定性队列。
func (a *Adapter) outboundMessage(message *raftpb.Message) (core.Message, error) {
	if message == nil || message.GetFrom() == 0 || message.GetTo() == 0 {
		return core.Message{}, fmt.Errorf("%w: from and to must be non-zero", ErrInvalidMessage)
	}
	if raftNode, exists := a.nodes[core.NodeID(message.GetFrom())]; !exists {
		return core.Message{}, fmt.Errorf("%w: sender n%d is unknown", ErrInvalidMessage, message.GetFrom())
	} else if !raftNode.running {
		return core.Message{}, fmt.Errorf("%w: sender n%d is crashed", ErrInvalidMessage, message.GetFrom())
	}
	if _, exists := a.nodes[core.NodeID(message.GetTo())]; !exists {
		return core.Message{}, fmt.Errorf("%w: receiver n%d is unknown", ErrInvalidMessage, message.GetTo())
	}

	cloned := proto.Clone(message).(*raftpb.Message)
	digest, err := messageDigest(cloned)
	if err != nil {
		return core.Message{}, fmt.Errorf("digest raft message: %w", err)
	}
	sender := a.nodes[core.NodeID(message.GetFrom())]
	result := core.Message{
		From:          core.NodeID(message.GetFrom()),
		To:            core.NodeID(message.GetTo()),
		SenderEpoch:   sender.epoch,
		TypeHint:      message.GetType().String(),
		PayloadDigest: digest,
		Metadata: map[string]string{
			"term":        strconv.FormatUint(message.GetTerm(), 10),
			"index":       strconv.FormatUint(message.GetIndex(), 10),
			"log_term":    strconv.FormatUint(message.GetLogTerm(), 10),
			"commit":      strconv.FormatUint(message.GetCommit(), 10),
			"reject":      strconv.FormatBool(message.GetReject()),
			"entry_count": strconv.Itoa(len(message.GetEntries())),
		},
		Payload: cloned,
	}
	if err := result.ValidateOutbound(); err != nil {
		return core.Message{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return result, nil
}

// decodeMessage 将 core.Message 的 Payload 反序列化为 raftpb.Message，并验证其一致性。
func decodeMessage(message core.Message) (*raftpb.Message, error) {
	var payload *raftpb.Message
	switch value := message.Payload.(type) {
	case *raftpb.Message:
		if value != nil {
			payload = proto.Clone(value).(*raftpb.Message)
		}
	case raftpb.Message:
		payload = proto.Clone(&value).(*raftpb.Message)
	}
	if payload == nil {
		return nil, fmt.Errorf("%w: payload is not *raftpb.Message", ErrInvalidMessage)
	}
	if payload.GetFrom() != uint64(message.From) || payload.GetTo() != uint64(message.To) {
		return nil, fmt.Errorf("%w: payload endpoints do not match envelope", ErrInvalidMessage)
	}
	if payload.GetType().String() != message.TypeHint {
		return nil, fmt.Errorf("%w: payload type does not match type hint", ErrInvalidMessage)
	}
	digest, err := messageDigest(payload)
	if err != nil {
		return nil, fmt.Errorf("digest delivered message: %w", err)
	}
	if digest != message.PayloadDigest {
		return nil, fmt.Errorf("%w: payload digest does not match envelope", ErrInvalidMessage)
	}
	return payload, nil
}
