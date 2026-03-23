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

func TestRoutingKeysUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    []string
		wantErr bool
	}{
		{
			name: "single string",
			json: `{"routing_key": "#"}`,
			want: []string{"#"},
		},
		{
			name: "array of strings",
			json: `{"routing_key": ["foo.*", "bar.#"]}`,
			want: []string{"foo.*", "bar.#"},
		},
		{
			name: "empty array",
			json: `{"routing_key": []}`,
			want: []string{},
		},
		{
			name: "omitted defaults to nil",
			json: `{}`,
			want: nil,
		},
		{
			name:    "invalid type",
			json:    `{"routing_key": 123}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg struct {
				RoutingKey mqbridge.RoutingKeys `json:"routing_key"`
			}
			err := json.Unmarshal([]byte(tt.json), &cfg)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cfg.RoutingKey) != len(tt.want) {
				t.Fatalf("got %v, want %v", cfg.RoutingKey, tt.want)
			}
			for i := range tt.want {
				if cfg.RoutingKey[i] != tt.want[i] {
					t.Errorf("index %d: got %q, want %q", i, cfg.RoutingKey[i], tt.want[i])
				}
			}
		})
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
			name: "missing rabbitmq url with rabbitmq bridge",
			config: mqbridge.Config{
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
			wantErr: true,
		},
		{
			name: "simplemq only without rabbitmq url",
			config: mqbridge.Config{
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
			name: "rabbitmq to rabbitmq not supported",
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
							{RabbitMQ: &mqbridge.ToRabbitMQConfig{}},
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
		{
			name: "per-bridge rabbitmq url override",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://global"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							SimpleMQ: &mqbridge.FromSimpleMQConfig{
								Queue:  "q",
								APIKey: "key",
							},
						},
						To: []mqbridge.ToConfig{
							{RabbitMQ: &mqbridge.ToRabbitMQConfig{
								RabbitMQConfig: mqbridge.RabbitMQConfig{URL: "amqp://override"},
							}},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "per-bridge rabbitmq url without global",
			config: mqbridge.Config{
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							RabbitMQ: &mqbridge.FromRabbitMQConfig{
								RabbitMQConfig: mqbridge.RabbitMQConfig{URL: "amqp://per-bridge"},
								Queue:          "q",
								Exchange:       "ex",
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
			name: "missing rabbitmq url everywhere",
			config: mqbridge.Config{
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
			wantErr: true,
		},
		{
			name: "per-bridge simplemq api_url override",
			config: mqbridge.Config{
				SimpleMQ: mqbridge.SimpleMQConfig{APIURL: "http://global"},
				Bridges: []mqbridge.BridgeConfig{
					{
						From: mqbridge.FromConfig{
							SimpleMQ: &mqbridge.FromSimpleMQConfig{
								SimpleMQConfig: mqbridge.SimpleMQConfig{APIURL: "http://override"},
								Queue:          "q",
								APIKey:         "key",
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
			name: "mixed: some bridges override, some inherit",
			config: mqbridge.Config{
				RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://global"},
				Bridges: []mqbridge.BridgeConfig{
					{
						// inherits global URL
						From: mqbridge.FromConfig{
							RabbitMQ: &mqbridge.FromRabbitMQConfig{
								Queue:    "q1",
								Exchange: "ex1",
							},
						},
						To: []mqbridge.ToConfig{
							{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: "dest1", APIKey: "key"}},
						},
					},
					{
						// overrides global URL
						From: mqbridge.FromConfig{
							RabbitMQ: &mqbridge.FromRabbitMQConfig{
								RabbitMQConfig: mqbridge.RabbitMQConfig{URL: "amqp://other"},
								Queue:          "q2",
								Exchange:       "ex2",
							},
						},
						To: []mqbridge.ToConfig{
							{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: "dest2", APIKey: "key"}},
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
