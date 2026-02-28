package mqbridge_test

import (
	"encoding/json"
	"testing"

	"github.com/fujiwara/mqbridge"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := mqbridge.LoadConfig(t.Context(), "testdata/config.jsonnet")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.RabbitMQ.URL != "amqp://guest:guest@localhost:5672/" {
		t.Errorf("unexpected rabbitmq.url: %s", cfg.RabbitMQ.URL)
	}
	if cfg.SimpleMQ.APIURL != "http://localhost:18080" {
		t.Errorf("unexpected simplemq.api_url: %s", cfg.SimpleMQ.APIURL)
	}
	if len(cfg.Bridges) != 2 {
		t.Errorf("expected 2 bridges, got %d", len(cfg.Bridges))
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate failed: %v", err)
	}
}

func TestRenderConfig(t *testing.T) {
	data, err := mqbridge.RenderConfig(t.Context(), "testdata/config.jsonnet")
	if err != nil {
		t.Fatalf("RenderConfig failed: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  mqbridge.Config
		wantErr bool
	}{
		{
			name:    "empty config",
			config:  mqbridge.Config{},
			wantErr: true,
		},
		{
			name: "missing rabbitmq url",
			config: mqbridge.Config{
				Bridges: []mqbridge.BridgeConfig{{}},
			},
			wantErr: true,
		},
		{
			name: "empty from",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://localhost"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{},
						To:   []mqbridge.ToConfig{{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: "q", APIKey: "k"}}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid rabbitmq to simplemq",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://localhost"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							RabbitMQ: &mqbridge.FromRabbitMQConfig{
								Queue:    "q",
								Exchange: "ex",
							},
						},
						To: []mqbridge.ToConfig{
							{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: "dest", APIKey: "key"}},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid simplemq to simplemq",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://localhost"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							SimpleMQ: &mqbridge.FromSimpleMQConfig{
								Queue:  "q",
								APIKey: "key",
							},
						},
						To: []mqbridge.ToConfig{
							{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: "dest", APIKey: "key"}},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "simplemq to simplemq missing dest queue",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://localhost"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							SimpleMQ: &mqbridge.FromSimpleMQConfig{
								Queue:  "q",
								APIKey: "key",
							},
						},
						To: []mqbridge.ToConfig{
							{SimpleMQ: &mqbridge.ToSimpleMQConfig{APIKey: "key"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "simplemq to simplemq missing dest api_key",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://localhost"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							SimpleMQ: &mqbridge.FromSimpleMQConfig{
								Queue:  "q",
								APIKey: "key",
							},
						},
						To: []mqbridge.ToConfig{
							{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: "dest"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid simplemq to rabbitmq",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://localhost"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							SimpleMQ: &mqbridge.FromSimpleMQConfig{
								Queue:  "q",
								APIKey: "key",
							},
						},
						To: []mqbridge.ToConfig{
							{RabbitMQ: &mqbridge.ToRabbitMQConfig{}},
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
