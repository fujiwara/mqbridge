package mqbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	armed "github.com/fujiwara/jsonnet-armed"
)

// Config is the top-level configuration for mqbridge.
type Config struct {
	RabbitMQ RabbitMQConfig `json:"rabbitmq"`
	SimpleMQ SimpleMQConfig `json:"simplemq"`
	Bridges  []BridgeConfig `json:"bridges"`
}

// RabbitMQConfig holds the global RabbitMQ connection settings.
type RabbitMQConfig struct {
	URL string `json:"url"`
}

// SimpleMQConfig holds the global SimpleMQ settings.
type SimpleMQConfig struct {
	APIURL string `json:"api_url"`
}

// BridgeConfig defines a single bridge rule.
type BridgeConfig struct {
	From FromConfig `json:"from"`
	To   []ToConfig `json:"to"`
}

// FromConfig defines the source of a bridge.
type FromConfig struct {
	RabbitMQ *FromRabbitMQConfig `json:"rabbitmq,omitempty"`
	SimpleMQ *FromSimpleMQConfig `json:"simplemq,omitempty"`
}

// ToConfig defines a destination of a bridge.
type ToConfig struct {
	RabbitMQ *ToRabbitMQConfig `json:"rabbitmq,omitempty"`
	SimpleMQ *ToSimpleMQConfig `json:"simplemq,omitempty"`
}

// FromRabbitMQConfig defines a RabbitMQ source.
type FromRabbitMQConfig struct {
	Queue        string `json:"queue"`
	Exchange     string `json:"exchange"`
	ExchangeType string `json:"exchange_type"`
	RoutingKey   string `json:"routing_key"`
}

// FromSimpleMQConfig defines a SimpleMQ source.
type FromSimpleMQConfig struct {
	Queue           string `json:"queue"`
	APIKey          string `json:"api_key"`
	PollingInterval string `json:"polling_interval"`
}

// GetPollingInterval returns the polling interval as a time.Duration.
func (c *FromSimpleMQConfig) GetPollingInterval() time.Duration {
	if c.PollingInterval == "" {
		return time.Second
	}
	d, err := time.ParseDuration(c.PollingInterval)
	if err != nil {
		return time.Second
	}
	return d
}

// ToRabbitMQConfig defines a RabbitMQ destination.
// The actual exchange/routing_key/headers are determined by the message JSON.
type ToRabbitMQConfig struct{}

// ToSimpleMQConfig defines a SimpleMQ destination.
type ToSimpleMQConfig struct {
	Queue  string `json:"queue"`
	APIKey string `json:"api_key"`
}

// LoadConfig loads and parses a configuration file (Jsonnet or JSON).
func LoadConfig(ctx context.Context, path string) (*Config, error) {
	jsonBytes, err := evaluateJsonnet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate config: %w", err)
	}
	return parseConfig(jsonBytes)
}

// RenderConfig evaluates a Jsonnet config file and returns the resulting JSON.
func RenderConfig(ctx context.Context, path string) ([]byte, error) {
	return evaluateJsonnet(ctx, path)
}

func evaluateJsonnet(ctx context.Context, path string) ([]byte, error) {
	var buf bytes.Buffer
	cli := &armed.CLI{Filename: path}
	cli.SetWriter(&buf)
	cli.AddFunctions(secretNativeFunction(ctx))
	if err := cli.Run(ctx); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func parseConfig(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return &cfg, nil
}

// Validate checks the configuration for correctness.
func (c *Config) Validate() error {
	if c.RabbitMQ.URL == "" {
		return fmt.Errorf("rabbitmq.url is required")
	}
	if len(c.Bridges) == 0 {
		return fmt.Errorf("at least one bridge is required")
	}
	for i, b := range c.Bridges {
		if err := b.validate(); err != nil {
			return fmt.Errorf("bridges[%d]: %w", i, err)
		}
	}
	return nil
}

func (b *BridgeConfig) validate() error {
	if b.From.RabbitMQ == nil && b.From.SimpleMQ == nil {
		return fmt.Errorf("from must specify either rabbitmq or simplemq")
	}
	if b.From.RabbitMQ != nil && b.From.SimpleMQ != nil {
		return fmt.Errorf("from must specify only one of rabbitmq or simplemq")
	}
	if len(b.To) == 0 {
		return fmt.Errorf("at least one destination is required")
	}
	if b.From.RabbitMQ != nil {
		if b.From.RabbitMQ.Queue == "" {
			return fmt.Errorf("from.rabbitmq.queue is required")
		}
		if b.From.RabbitMQ.Exchange == "" {
			return fmt.Errorf("from.rabbitmq.exchange is required")
		}
		for j, to := range b.To {
			if to.SimpleMQ == nil {
				return fmt.Errorf("to[%d]: RabbitMQ source requires SimpleMQ destination", j)
			}
			if to.SimpleMQ.Queue == "" {
				return fmt.Errorf("to[%d]: simplemq.queue is required", j)
			}
			if to.SimpleMQ.APIKey == "" {
				return fmt.Errorf("to[%d]: simplemq.api_key is required", j)
			}
		}
	}
	if b.From.SimpleMQ != nil {
		if b.From.SimpleMQ.Queue == "" {
			return fmt.Errorf("from.simplemq.queue is required")
		}
		if b.From.SimpleMQ.APIKey == "" {
			return fmt.Errorf("from.simplemq.api_key is required")
		}
		for j, to := range b.To {
			if to.RabbitMQ == nil {
				return fmt.Errorf("to[%d]: SimpleMQ source requires RabbitMQ destination", j)
			}
		}
	}
	return nil
}
