package core

import (
	"errors"
	"testing"
)

func validOutboundMessage() Message {
	return Message{
		From:        1,
		To:          2,
		SenderEpoch: 1,
		TypeHint:    "opaque",
	}
}

func validRegisteredMessage() Message {
	message := validOutboundMessage()
	message.ID = 1
	message.Sequence = 1
	return message
}

func TestMessageValidationStages(t *testing.T) {
	message := validOutboundMessage()
	if err := message.ValidateOutbound(); err != nil {
		t.Fatalf("outbound message invalid: %v", err)
	}
	if err := message.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("unregistered message error = %v, want ErrInvalidValue", err)
	}

	message = validRegisteredMessage()
	if err := message.ValidateOutbound(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("registered outbound message error = %v, want ErrInvalidValue", err)
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("registered message invalid: %v", err)
	}

	message.ParentID = message.ID
	if err := message.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("self-parent error = %v, want ErrInvalidValue", err)
	}
}

func TestMessageCopyDoesNotAliasMetadata(t *testing.T) {
	message := validOutboundMessage()
	message.Metadata = map[string]string{"key": "old"}
	copy := message.Copy()
	copy.Metadata["key"] = "new"
	if message.Metadata["key"] != "old" {
		t.Fatal("copy mutated original metadata")
	}
}
