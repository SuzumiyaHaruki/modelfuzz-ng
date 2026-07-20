package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// NodeID 标识被测系统中的一个逻辑成员。零保留为“未指定”，不是合法节点。
type NodeID uint64

// MessageID 标识一次执行中的具体消息实例。ID 由确定性网络 Runtime 单调分配；
// 零值不合法，并且同一次执行中不会复用 ID。
type MessageID uint64

// ExecutionID 标识一次独立执行。
type ExecutionID string

// NodeEpoch 标识节点的一次生命周期。节点重启时递增 epoch，从而能够拒绝
// 来自旧生命周期的延迟 Effect。
type NodeEpoch uint64

// LinkID 标识一条有向传输链路。
type LinkID struct {
	From NodeID `json:"from"`
	To   NodeID `json:"to"`
}

func (id NodeID) Valid() bool      { return id != 0 }
func (id MessageID) Valid() bool   { return id != 0 }
func (id ExecutionID) Valid() bool { return id != "" }
func (id NodeEpoch) Valid() bool   { return id != 0 }

func (id NodeID) String() string    { return fmt.Sprintf("n%d", id) }
func (id MessageID) String() string { return fmt.Sprintf("m%d", id) }
func (id NodeEpoch) String() string { return fmt.Sprintf("e%d", id) }

func (l LinkID) Validate() error {
	if !l.From.Valid() {
		return invalidValue("link", "from", "must be non-zero")
	}
	if !l.To.Valid() {
		return invalidValue("link", "to", "must be non-zero")
	}
	return nil
}

func (l LinkID) String() string {
	return fmt.Sprintf("%s->%s", l.From, l.To)
}

// MarshalJSON 将 MessageID 编码成 "m42"。字符串编码能够避免某些工具的
// 数字类型无法精确表示全部 uint64 值，从而损坏 Trace。
func (id MessageID) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.String())
}

// UnmarshalJSON 同时接受标准的 "m42" 字符串表示和兼容旧数据的数字表示。
func (id *MessageID) UnmarshalJSON(data []byte) error {
	if id == nil {
		return invalidValue("message_id", "", "nil receiver")
	}

	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		value = strings.TrimPrefix(value, "m")
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed == 0 {
			return invalidValue("message_id", "", "must use a non-zero value such as m42")
		}
		*id = MessageID(parsed)
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value json.Number
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	parsed, err := strconv.ParseUint(value.String(), 10, 64)
	if err != nil || parsed == 0 {
		return invalidValue("message_id", "", "must be a non-zero unsigned integer")
	}
	*id = MessageID(parsed)
	return nil
}
