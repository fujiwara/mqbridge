package mqbridge_test

import (
	"testing"

	"github.com/fujiwara/mqbridge"
)

func TestParseRabbitMQMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "valid message",
			input: `{"exchange":"ex","routing_key":"key","body":"hello"}`,
		},
		{
			name:  "valid with headers",
			input: `{"exchange":"ex","routing_key":"key","headers":{"content-type":"application/json"},"body":"hello"}`,
		},
		{
			name:    "missing exchange",
			input:   `{"routing_key":"key","body":"hello"}`,
			wantErr: true,
		},
		{
			name:    "missing routing_key",
			input:   `{"exchange":"ex","body":"hello"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   `not json`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := mqbridge.ParseRabbitMQMessage([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.Exchange == "" {
				t.Error("expected non-empty exchange")
			}
			if msg.RoutingKey == "" {
				t.Error("expected non-empty routing_key")
			}
		})
	}
}
