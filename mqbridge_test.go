package mqbridge_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fujiwara/mqbridge"
	"github.com/fujiwara/simplemq-cli/localserver"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sacloud/simplemq-api-go/apis/v1/message"
)

const (
	testRabbitMQURL = "amqp://guest:guest@localhost:5672/"
	testAPIKey      = "test-api-key"
)

type testSecuritySource struct {
	token string
}

func (s *testSecuritySource) ApiKeyAuth(_ context.Context, _ message.OperationName) (message.ApiKeyAuth, error) {
	return message.ApiKeyAuth{Token: s.token}, nil
}

func newSimpleMQTestClient(t *testing.T, serverURL string) *message.Client {
	t.Helper()
	client, err := message.NewClient(serverURL, &testSecuritySource{token: testAPIKey})
	if err != nil {
		t.Fatalf("failed to create SimpleMQ client: %v", err)
	}
	return client
}

func requireRabbitMQ(t *testing.T) *amqp.Connection {
	t.Helper()
	url := testRabbitMQURL
	if v := os.Getenv("RABBITMQ_URL"); v != "" {
		url = v
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	return conn
}

func TestLoadConfig(t *testing.T) {
	cfg, err := mqbridge.LoadConfig(context.Background(), "testdata/config.jsonnet")
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
	data, err := mqbridge.RenderConfig(context.Background(), "testdata/config.jsonnet")
	if err != nil {
		t.Fatalf("RenderConfig failed: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
}

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

func TestRabbitMQToSimpleMQ(t *testing.T) {
	conn := requireRabbitMQ(t)
	defer conn.Close()

	// Start SimpleMQ localserver
	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exchangeName := fmt.Sprintf("test-exchange-%d", time.Now().UnixNano())
	queueName := fmt.Sprintf("test-queue-%d", time.Now().UnixNano())
	destQueue := fmt.Sprintf("test-dest-%d", time.Now().UnixNano())

	cfg := &mqbridge.Config{
		RabbitMQ: mqbridge.RabbitMQConfig{URL: testRabbitMQURL},
		SimpleMQ: mqbridge.SimpleMQConfig{APIURL: smqServer.URL()},
		Bridges: []mqbridge.BridgeConfig{
			{
				From: mqbridge.FromConfig{
					RabbitMQ: &mqbridge.FromRabbitMQConfig{
						Queue:        queueName,
						Exchange:     exchangeName,
						ExchangeType: "topic",
						RoutingKey:   "#",
					},
				},
				To: []mqbridge.ToConfig{
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue, APIKey: testAPIKey}},
				},
			},
		},
	}

	app, err := mqbridge.New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Start bridge in background
	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	// Wait for subscriber to be ready
	time.Sleep(500 * time.Millisecond)

	// Publish message to RabbitMQ
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()

	testBody := "hello from rabbitmq"
	err = ch.PublishWithContext(ctx, exchangeName, "test.key", false, false, amqp.Publishing{
		Body: []byte(testBody),
	})
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// Poll SimpleMQ for the message
	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	var received string
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		res, err := smqClient.ReceiveMessage(ctx, message.ReceiveMessageParams{
			QueueName: message.QueueName(destQueue),
		})
		if err != nil {
			continue
		}
		recvOK, ok := res.(*message.ReceiveMessageOK)
		if !ok || len(recvOK.Messages) == 0 {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(string(recvOK.Messages[0].Content))
		if err != nil {
			t.Fatalf("failed to decode message: %v", err)
		}
		received = string(decoded)
		break
	}

	if received != testBody {
		t.Errorf("expected %q, got %q", testBody, received)
	}

	bridgeCancel()
}

func TestSimpleMQToRabbitMQ(t *testing.T) {
	conn := requireRabbitMQ(t)
	defer conn.Close()

	// Start SimpleMQ localserver
	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inboundQueue := fmt.Sprintf("test-inbound-%d", time.Now().UnixNano())
	destExchange := fmt.Sprintf("test-dest-exchange-%d", time.Now().UnixNano())
	destQueue := fmt.Sprintf("test-dest-rmq-%d", time.Now().UnixNano())

	// Set up RabbitMQ destination exchange and queue
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()

	err = ch.ExchangeDeclare(destExchange, "topic", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("failed to declare exchange: %v", err)
	}
	_, err = ch.QueueDeclare(destQueue, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("failed to declare queue: %v", err)
	}
	err = ch.QueueBind(destQueue, "#", destExchange, false, nil)
	if err != nil {
		t.Fatalf("failed to bind queue: %v", err)
	}

	// Start consuming from RabbitMQ
	deliveries, err := ch.ConsumeWithContext(ctx, destQueue, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}

	cfg := &mqbridge.Config{
		RabbitMQ: mqbridge.RabbitMQConfig{URL: testRabbitMQURL},
		SimpleMQ: mqbridge.SimpleMQConfig{APIURL: smqServer.URL()},
		Bridges: []mqbridge.BridgeConfig{
			{
				From: mqbridge.FromConfig{
					SimpleMQ: &mqbridge.FromSimpleMQConfig{
						Queue:           inboundQueue,
						APIKey:          testAPIKey,
						PollingInterval: "100ms",
					},
				},
				To: []mqbridge.ToConfig{
					{RabbitMQ: &mqbridge.ToRabbitMQConfig{}},
				},
			},
		},
	}

	app, err := mqbridge.New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Start bridge in background
	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	// Wait for subscriber to be ready
	time.Sleep(500 * time.Millisecond)

	// Send message to SimpleMQ in the expected JSON format
	testBody := "hello from simplemq"
	rmqMsg := mqbridge.RabbitMQMessage{
		Exchange:   destExchange,
		RoutingKey: "test.key",
		Body:       testBody,
	}
	msgJSON, _ := json.Marshal(rmqMsg)
	encoded := base64.StdEncoding.EncodeToString(msgJSON)

	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	_, err = smqClient.SendMessage(ctx,
		&message.SendRequest{Content: message.MessageContent(encoded)},
		message.SendMessageParams{QueueName: message.QueueName(inboundQueue)},
	)
	if err != nil {
		t.Fatalf("failed to send to SimpleMQ: %v", err)
	}

	// Wait for the message to appear in RabbitMQ
	select {
	case delivery := <-deliveries:
		if string(delivery.Body) != testBody {
			t.Errorf("expected %q, got %q", testBody, string(delivery.Body))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for message in RabbitMQ")
	}

	bridgeCancel()
}
