package core

// Message 是协议无关的消息信封。Payload 对 Runtime 不透明，只在内存中
// 随消息队列保存并在投递时交还 Adapter，不写入 JSON Trace。
// PayloadDigest 和 Adapter 产生的模型事件用于检查稳定重放。
type Message struct {
	ID MessageID `json:"id"`

	From NodeID `json:"from"`
	To   NodeID `json:"to"`

	SenderEpoch NodeEpoch `json:"sender_epoch"`
	Sequence    uint64    `json:"link_sequence"`
	ParentID    MessageID `json:"parent_id,omitempty"`

	TypeHint      string            `json:"type_hint,omitempty"`
	PayloadDigest string            `json:"payload_digest,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Payload       any               `json:"-"`
}

func (m Message) Link() LinkID {
	return LinkID{From: m.From, To: m.To}
}

// ValidateOutbound 在 Runtime 分配具体 MessageID 和链路内序号之前，
// 校验 Adapter 刚产生的出站消息。
func (m Message) ValidateOutbound() error {
	if err := m.validateEnvelope(); err != nil {
		return err
	}
	if m.ID.Valid() || m.Sequence != 0 || m.ParentID.Valid() {
		return invalidValue("message", "", "outbound message must not contain runtime-assigned identity fields")
	}
	return nil
}

func (m Message) validateEnvelope() error {
	if err := m.Link().Validate(); err != nil {
		return err
	}
	if !m.SenderEpoch.Valid() {
		return invalidValue("message", "sender_epoch", "must be non-zero")
	}
	if m.ParentID.Valid() && m.ID.Valid() && m.ParentID == m.ID {
		return invalidValue("message", "parent_id", "cannot reference the message itself")
	}
	return nil
}

// Validate 校验已经进入确定性网络、完成 ID 注册的消息。
func (m Message) Validate() error {
	if err := m.validateEnvelope(); err != nil {
		return err
	}
	if !m.ID.Valid() {
		return invalidValue("message", "id", "must be non-zero")
	}
	if m.Sequence == 0 {
		return invalidValue("message", "link_sequence", "must be non-zero")
	}
	return nil
}

// Copy 深拷贝可变元数据。Payload 仍然是由 Adapter 持有的浅拷贝值；
// Adapter 必须在产生消息前自行复制可变的协议负载。
func (m Message) Copy() Message {
	copy := m
	copy.Metadata = cloneStringMap(m.Metadata)
	return copy
}

// cloneStringMap 深拷贝可变的 map[string]string。
func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// cloneAnyMap 深拷贝 core 当前允许放入语义字段的常见 JSON 容器。
// 未识别的值按不可变值处理；调用方仍不应在 Semantic/Params 中放入
// 带内部可变状态的自定义对象。
func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneAnyValue(value)
	}
	return result
}

func cloneAnyValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneAnyMap(value)
	case []any:
		result := make([]any, len(value))
		for i := range value {
			result[i] = cloneAnyValue(value[i])
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, len(value))
		for i := range value {
			result[i] = cloneAnyMap(value[i])
		}
		return result
	case []byte:
		return append([]byte(nil), value...)
	default:
		return value
	}
}
