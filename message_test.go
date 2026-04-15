package mqbridge_test

import (
	"testing"

	"github.com/fujiwara/mqbridge"
)

func TestMarshalUnmarshalMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  *mqbridge.Message
	}{
		{
			name: "text body",
			msg:  &mqbridge.Message{Body: []byte("hello world")},
		},
		{
			name: "with headers",
			msg: &mqbridge.Message{
				Body: []byte("hello"),
				Headers: map[string]string{
					"rabbitmq.exchange":     "ex",
					"rabbitmq.routing_key":  "key",
					"rabbitmq.header.x-foo": "bar",
				},
			},
		},
		{
			name: "binary body (auto base64)",
			msg:  &mqbridge.Message{Body: []byte{0x00, 0x01, 0xff}},
		},
		{
			name: "binary body with headers",
			msg: &mqbridge.Message{
				Body: []byte{0xde, 0xad, 0xbe, 0xef},
				Headers: map[string]string{
					"rabbitmq.exchange":    "ex",
					"rabbitmq.routing_key": "key",
				},
			},
		},
		{
			name: "empty body with headers",
			msg: &mqbridge.Message{
				Body: []byte{},
				Headers: map[string]string{
					"rabbitmq.exchange":    "ex",
					"rabbitmq.routing_key": "rk",
				},
			},
		},
		{
			name: "empty body no headers",
			msg:  &mqbridge.Message{Body: []byte{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := mqbridge.MarshalMessage(tt.msg)
			if err != nil {
				t.Fatalf("MarshalMessage failed: %v", err)
			}
			got := mqbridge.UnmarshalMessage(data)
			if string(got.Body) != string(tt.msg.Body) {
				t.Errorf("body: got %x, want %x", got.Body, tt.msg.Body)
			}
			for k, v := range tt.msg.Headers {
				if got.Headers[k] != v {
					t.Errorf("header %q: got %q, want %q", k, got.Headers[k], v)
				}
			}
		})
	}
}

func TestUnmarshalMessageLegacy(t *testing.T) {
	// Legacy format: plain text (not JSON) should be treated as body only.
	data := []byte("plain text message")
	msg := mqbridge.UnmarshalMessage(data)
	if string(msg.Body) != "plain text message" {
		t.Errorf("body: got %q, want %q", msg.Body, "plain text message")
	}
	if len(msg.Headers) != 0 {
		t.Errorf("headers: got %v, want empty", msg.Headers)
	}
}

func TestValidateForRabbitMQ(t *testing.T) {
	tests := []struct {
		name    string
		msg     *mqbridge.Message
		wantErr bool
	}{
		{
			name: "valid",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					mqbridge.HeaderRabbitMQExchange:   "ex",
					mqbridge.HeaderRabbitMQRoutingKey: "key",
				},
			},
		},
		{
			name: "empty exchange is valid",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					mqbridge.HeaderRabbitMQExchange:   "",
					mqbridge.HeaderRabbitMQRoutingKey: "key",
				},
			},
		},
		{
			name: "missing exchange key",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					mqbridge.HeaderRabbitMQRoutingKey: "key",
				},
			},
			wantErr: true,
		},
		{
			name: "missing routing_key key",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					mqbridge.HeaderRabbitMQExchange: "ex",
				},
			},
			wantErr: true,
		},
		{
			name:    "no headers at all",
			msg:     &mqbridge.Message{Body: []byte("hello")},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.ValidateForRabbitMQ()
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestMessageID(t *testing.T) {
	tests := []struct {
		name   string
		msg    *mqbridge.Message
		wantID string
	}{
		{
			name:   "in-memory id takes priority",
			msg:    mqbridge.NewMessageWithIDForTest("simplemq-id", nil, map[string]string{mqbridge.HeaderRabbitMQMessageID: "rmq-id"}),
			wantID: "simplemq-id",
		},
		{
			name:   "falls back to rabbitmq.message_id header",
			msg:    &mqbridge.Message{Headers: map[string]string{mqbridge.HeaderRabbitMQMessageID: "rmq-id"}},
			wantID: "rmq-id",
		},
		{
			name:   "in-memory id only",
			msg:    mqbridge.NewMessageWithIDForTest("simplemq-id", nil, nil),
			wantID: "simplemq-id",
		},
		{
			name:   "no id set",
			msg:    &mqbridge.Message{Headers: map[string]string{}},
			wantID: "",
		},
		{
			name:   "nil headers",
			msg:    &mqbridge.Message{},
			wantID: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.msg.MessageID()
			if got != tt.wantID {
				t.Errorf("MessageID() = %q, want %q", got, tt.wantID)
			}
		})
	}
}

func TestRabbitMQPublishParams(t *testing.T) {
	tests := []struct {
		name       string
		msg        *mqbridge.Message
		wantErr    bool
		wantExch   string
		wantRK     string
		wantHdrLen int
	}{
		{
			name: "valid",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					"rabbitmq.exchange":     "ex",
					"rabbitmq.routing_key":  "key",
					"rabbitmq.header.x-foo": "bar",
				},
			},
			wantExch:   "ex",
			wantRK:     "key",
			wantHdrLen: 1,
		},
		{
			name: "empty exchange (default exchange)",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					"rabbitmq.exchange":    "",
					"rabbitmq.routing_key": "reply-queue",
				},
			},
			wantExch: "",
			wantRK:   "reply-queue",
		},
		{
			name: "empty routing_key (fanout)",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					"rabbitmq.exchange":    "fanout-ex",
					"rabbitmq.routing_key": "",
				},
			},
			wantExch: "fanout-ex",
			wantRK:   "",
		},
		{
			name: "missing exchange key",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					"rabbitmq.routing_key": "key",
				},
			},
			wantErr: true,
		},
		{
			name: "missing routing_key key",
			msg: &mqbridge.Message{
				Headers: map[string]string{
					"rabbitmq.exchange": "ex",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exchange, routingKey, headers, err := tt.msg.RabbitMQPublishParams()
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exchange != tt.wantExch {
				t.Errorf("exchange: got %q, want %q", exchange, tt.wantExch)
			}
			if routingKey != tt.wantRK {
				t.Errorf("routing_key: got %q, want %q", routingKey, tt.wantRK)
			}
			if len(headers) != tt.wantHdrLen {
				t.Errorf("headers len: got %d, want %d", len(headers), tt.wantHdrLen)
			}
			if tt.wantHdrLen > 0 {
				if headers["x-foo"] != "bar" {
					t.Errorf("header x-foo: got %q, want %q", headers["x-foo"], "bar")
				}
			}
		})
	}
}
