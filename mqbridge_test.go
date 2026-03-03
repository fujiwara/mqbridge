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
		if os.Getenv("CI") != "" {
			t.Fatalf("RabbitMQ not available in CI: %v", err)
		}
		t.Skipf("RabbitMQ not available: %v", err)
	}
	return conn
}

type testEnv struct {
	t         *testing.T
	smqServer *localserver.Server
	smqClient *message.Client
	rmqConn   *amqp.Connection
	ctx       context.Context
}

func newTestEnv(t *testing.T, needsRabbitMQ bool) *testEnv {
	t.Helper()
	smqServer := localserver.NewServer()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(func() {
		cancel()
		smqServer.Close()
	})
	var rmqConn *amqp.Connection
	if needsRabbitMQ {
		rmqConn = requireRabbitMQ(t)
		t.Cleanup(func() { rmqConn.Close() })
	}
	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	return &testEnv{
		t:         t,
		smqServer: smqServer,
		smqClient: smqClient,
		rmqConn:   rmqConn,
		ctx:       ctx,
	}
}

func (e *testEnv) uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func (e *testEnv) runBridge(bridges []mqbridge.BridgeConfig) context.CancelFunc {
	e.t.Helper()
	cfg := &mqbridge.Config{
		RabbitMQ: mqbridge.RabbitMQConfig{URL: testRabbitMQURL},
		SimpleMQ: mqbridge.SimpleMQConfig{APIURL: e.smqServer.URL()},
		Bridges:  bridges,
	}
	app, err := mqbridge.New(cfg)
	if err != nil {
		e.t.Fatalf("New failed: %v", err)
	}
	bridgeCtx, bridgeCancel := context.WithCancel(e.ctx)
	go func() { app.Run(bridgeCtx) }()
	time.Sleep(500 * time.Millisecond)
	return bridgeCancel
}

func (e *testEnv) publishToRabbitMQ(exchange, routingKey, body string) {
	e.t.Helper()
	ch, err := e.rmqConn.Channel()
	if err != nil {
		e.t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()
	err = ch.PublishWithContext(e.ctx, exchange, routingKey, false, false, amqp.Publishing{
		Body: []byte(body),
	})
	if err != nil {
		e.t.Fatalf("failed to publish: %v", err)
	}
}

func (e *testEnv) sendToSimpleMQ(queue, body string) {
	e.t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	_, err := e.smqClient.SendMessage(e.ctx,
		&message.SendRequest{Content: message.MessageContent(encoded)},
		message.SendMessageParams{QueueName: message.QueueName(queue)},
	)
	if err != nil {
		e.t.Fatalf("failed to send to SimpleMQ: %v", err)
	}
}

func (e *testEnv) receiveFromSimpleMQ(queue string) string {
	e.t.Helper()
	for range 20 {
		time.Sleep(200 * time.Millisecond)
		res, err := e.smqClient.ReceiveMessage(e.ctx, message.ReceiveMessageParams{
			QueueName: message.QueueName(queue),
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
			e.t.Fatalf("failed to decode message from queue %s: %v", queue, err)
		}
		return string(decoded)
	}
	return ""
}

func TestRabbitMQToSimpleMQ(t *testing.T) {
	env := newTestEnv(t, true)
	exchange := env.uniqueName("exchange")
	queue := env.uniqueName("queue")
	dest := env.uniqueName("dest")

	stop := env.runBridge([]mqbridge.BridgeConfig{
		{
			From: mqbridge.FromConfig{
				RabbitMQ: &mqbridge.FromRabbitMQConfig{
					Queue: queue, Exchange: exchange,
					ExchangeType: "topic", RoutingKey: "#",
				},
			},
			To: []mqbridge.ToConfig{
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest, APIKey: testAPIKey}},
			},
		},
	})
	defer stop()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.publishToRabbitMQ(exchange, "test.key", testBody)

	if received := env.receiveFromSimpleMQ(dest); received != testBody {
		t.Errorf("expected %q, got %q", testBody, received)
	}
}

func TestRabbitMQToSimpleMQFanout(t *testing.T) {
	env := newTestEnv(t, true)
	exchange := env.uniqueName("fanout-exchange")
	queue := env.uniqueName("fanout-queue")
	dest1 := env.uniqueName("fanout-dest1")
	dest2 := env.uniqueName("fanout-dest2")
	dest3 := env.uniqueName("fanout-dest3")

	stop := env.runBridge([]mqbridge.BridgeConfig{
		{
			From: mqbridge.FromConfig{
				RabbitMQ: &mqbridge.FromRabbitMQConfig{
					Queue: queue, Exchange: exchange,
					ExchangeType: "topic", RoutingKey: "#",
				},
			},
			To: []mqbridge.ToConfig{
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest1, APIKey: testAPIKey}},
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest2, APIKey: testAPIKey}},
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest3, APIKey: testAPIKey}},
			},
		},
	})
	defer stop()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.publishToRabbitMQ(exchange, "test.key", testBody)

	for _, dq := range []string{dest1, dest2, dest3} {
		if received := env.receiveFromSimpleMQ(dq); received != testBody {
			t.Errorf("queue %s: expected %q, got %q", dq, testBody, received)
		}
	}
}

func TestRabbitMQToSimpleMQExchangePassive(t *testing.T) {
	env := newTestEnv(t, true)
	exchange := env.uniqueName("passive-exchange")
	queue := env.uniqueName("passive-queue")
	dest := env.uniqueName("passive-dest")

	// Pre-declare the exchange so that exchange_passive can find it.
	ch, err := env.rmqConn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	err = ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("failed to pre-declare exchange: %v", err)
	}
	ch.Close()

	stop := env.runBridge([]mqbridge.BridgeConfig{
		{
			From: mqbridge.FromConfig{
				RabbitMQ: &mqbridge.FromRabbitMQConfig{
					Queue: queue, Exchange: exchange,
					ExchangeType: "topic", RoutingKey: "#",
					ExchangePassive: true,
				},
			},
			To: []mqbridge.ToConfig{
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest, APIKey: testAPIKey}},
			},
		},
	})
	defer stop()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.publishToRabbitMQ(exchange, "test.key", testBody)

	if received := env.receiveFromSimpleMQ(dest); received != testBody {
		t.Errorf("expected %q, got %q", testBody, received)
	}
}

func TestSimpleMQToSimpleMQ(t *testing.T) {
	env := newTestEnv(t, false)
	inbound := env.uniqueName("smq-inbound")
	dest := env.uniqueName("smq-dest")

	stop := env.runBridge([]mqbridge.BridgeConfig{
		{
			From: mqbridge.FromConfig{
				SimpleMQ: &mqbridge.FromSimpleMQConfig{
					Queue: inbound, APIKey: testAPIKey,
					PollingInterval: "100ms",
				},
			},
			To: []mqbridge.ToConfig{
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest, APIKey: testAPIKey}},
			},
		},
	})
	defer stop()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.sendToSimpleMQ(inbound, testBody)

	if received := env.receiveFromSimpleMQ(dest); received != testBody {
		t.Errorf("expected %q, got %q", testBody, received)
	}
}

func TestSimpleMQToSimpleMQFanout(t *testing.T) {
	env := newTestEnv(t, false)
	inbound := env.uniqueName("smq-fanout-inbound")
	dest1 := env.uniqueName("smq-fanout-dest1")
	dest2 := env.uniqueName("smq-fanout-dest2")
	dest3 := env.uniqueName("smq-fanout-dest3")

	stop := env.runBridge([]mqbridge.BridgeConfig{
		{
			From: mqbridge.FromConfig{
				SimpleMQ: &mqbridge.FromSimpleMQConfig{
					Queue: inbound, APIKey: testAPIKey,
					PollingInterval: "100ms",
				},
			},
			To: []mqbridge.ToConfig{
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest1, APIKey: testAPIKey}},
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest2, APIKey: testAPIKey}},
				{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: dest3, APIKey: testAPIKey}},
			},
		},
	})
	defer stop()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.sendToSimpleMQ(inbound, testBody)

	for _, dq := range []string{dest1, dest2, dest3} {
		if received := env.receiveFromSimpleMQ(dq); received != testBody {
			t.Errorf("queue %s: expected %q, got %q", dq, testBody, received)
		}
	}
}

func TestSimpleMQToRabbitMQ(t *testing.T) {
	env := newTestEnv(t, true)
	inbound := env.uniqueName("inbound")
	destExchange := env.uniqueName("dest-exchange")
	destQueue := env.uniqueName("dest-rmq")

	// Set up RabbitMQ exchange, queue, and binding for consuming.
	ch, err := env.rmqConn.Channel()
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

	deliveries, err := ch.ConsumeWithContext(env.ctx, destQueue, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}

	stop := env.runBridge([]mqbridge.BridgeConfig{
		{
			From: mqbridge.FromConfig{
				SimpleMQ: &mqbridge.FromSimpleMQConfig{
					Queue: inbound, APIKey: testAPIKey,
					PollingInterval: "100ms",
				},
			},
			To: []mqbridge.ToConfig{
				{RabbitMQ: &mqbridge.ToRabbitMQConfig{}},
			},
		},
	})
	defer stop()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	rmqMsg := mqbridge.RabbitMQMessage{
		Exchange:   destExchange,
		RoutingKey: "test.key",
		Body:       testBody,
	}
	msgJSON, _ := json.Marshal(rmqMsg)
	env.sendToSimpleMQ(inbound, string(msgJSON))

	select {
	case delivery := <-deliveries:
		if string(delivery.Body) != testBody {
			t.Errorf("expected %q, got %q", testBody, string(delivery.Body))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for message in RabbitMQ")
	}
}
