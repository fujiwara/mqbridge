package mqbridge

import (
	"encoding/json"
	"fmt"
)

// RabbitMQMessage represents the JSON format for messages
// sent from SimpleMQ to RabbitMQ.
type RabbitMQMessage struct {
	Exchange   string            `json:"exchange"`
	RoutingKey string            `json:"routing_key"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"`
}

// ParseRabbitMQMessage parses a JSON message from SimpleMQ.
func ParseRabbitMQMessage(data []byte) (*RabbitMQMessage, error) {
	var msg RabbitMQMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to parse RabbitMQ message: %w", err)
	}
	if msg.Exchange == "" {
		return nil, fmt.Errorf("exchange is required in message")
	}
	if msg.RoutingKey == "" {
		return nil, fmt.Errorf("routing_key is required in message")
	}
	return &msg, nil
}
