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

func receiveOneFromSimpleMQ(t *testing.T, ctx context.Context, smqClient *message.Client, queueName string) string {
	t.Helper()
	for range 20 {
		time.Sleep(200 * time.Millisecond)
		res, err := smqClient.ReceiveMessage(ctx, message.ReceiveMessageParams{
			QueueName: message.QueueName(queueName),
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
			t.Fatalf("failed to decode message from queue %s: %v", queueName, err)
		}
		return string(decoded)
	}
	return ""
}

func TestRabbitMQToSimpleMQ(t *testing.T) {
	conn := requireRabbitMQ(t)
	defer conn.Close()

	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
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

	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	time.Sleep(500 * time.Millisecond)

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	err = ch.PublishWithContext(ctx, exchangeName, "test.key", false, false, amqp.Publishing{
		Body: []byte(testBody),
	})
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	received := receiveOneFromSimpleMQ(t, ctx, smqClient, destQueue)
	if received != testBody {
		t.Errorf("expected %q, got %q", testBody, received)
	}

	bridgeCancel()
}

func TestRabbitMQToSimpleMQFanout(t *testing.T) {
	conn := requireRabbitMQ(t)
	defer conn.Close()

	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	exchangeName := fmt.Sprintf("test-fanout-exchange-%d", time.Now().UnixNano())
	queueName := fmt.Sprintf("test-fanout-queue-%d", time.Now().UnixNano())
	destQueue1 := fmt.Sprintf("test-fanout-dest1-%d", time.Now().UnixNano())
	destQueue2 := fmt.Sprintf("test-fanout-dest2-%d", time.Now().UnixNano())
	destQueue3 := fmt.Sprintf("test-fanout-dest3-%d", time.Now().UnixNano())

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
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue1, APIKey: testAPIKey}},
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue2, APIKey: testAPIKey}},
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue3, APIKey: testAPIKey}},
				},
			},
		},
	}

	app, err := mqbridge.New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	time.Sleep(500 * time.Millisecond)

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	err = ch.PublishWithContext(ctx, exchangeName, "test.key", false, false, amqp.Publishing{
		Body: []byte(testBody),
	})
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	for _, dq := range []string{destQueue1, destQueue2, destQueue3} {
		received := receiveOneFromSimpleMQ(t, ctx, smqClient, dq)
		if received != testBody {
			t.Errorf("queue %s: expected %q, got %q", dq, testBody, received)
		}
	}

	bridgeCancel()
}

func TestRabbitMQToSimpleMQExchangePassive(t *testing.T) {
	conn := requireRabbitMQ(t)
	defer conn.Close()

	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	exchangeName := fmt.Sprintf("test-passive-exchange-%d", time.Now().UnixNano())
	queueName := fmt.Sprintf("test-passive-queue-%d", time.Now().UnixNano())
	destQueue := fmt.Sprintf("test-passive-dest-%d", time.Now().UnixNano())

	// Pre-declare the exchange so that exchange_passive can find it.
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	err = ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("failed to pre-declare exchange: %v", err)
	}
	ch.Close()

	cfg := &mqbridge.Config{
		RabbitMQ: mqbridge.RabbitMQConfig{URL: testRabbitMQURL},
		SimpleMQ: mqbridge.SimpleMQConfig{APIURL: smqServer.URL()},
		Bridges: []mqbridge.BridgeConfig{
			{
				From: mqbridge.FromConfig{
					RabbitMQ: &mqbridge.FromRabbitMQConfig{
						Queue:           queueName,
						Exchange:        exchangeName,
						ExchangeType:    "topic",
						RoutingKey:      "#",
						ExchangePassive: true,
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

	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	time.Sleep(500 * time.Millisecond)

	ch, err = conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	err = ch.PublishWithContext(ctx, exchangeName, "test.key", false, false, amqp.Publishing{
		Body: []byte(testBody),
	})
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	received := receiveOneFromSimpleMQ(t, ctx, smqClient, destQueue)
	if received != testBody {
		t.Errorf("expected %q, got %q", testBody, received)
	}

	bridgeCancel()
}

func TestSimpleMQToSimpleMQ(t *testing.T) {
	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	inboundQueue := fmt.Sprintf("test-smq-inbound-%d", time.Now().UnixNano())
	destQueue := fmt.Sprintf("test-smq-dest-%d", time.Now().UnixNano())

	cfg := &mqbridge.Config{
		RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://dummy:dummy@localhost:5672/"},
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
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue, APIKey: testAPIKey}},
				},
			},
		},
	}

	app, err := mqbridge.New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	time.Sleep(500 * time.Millisecond)

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	encoded := base64.StdEncoding.EncodeToString([]byte(testBody))

	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	_, err = smqClient.SendMessage(ctx,
		&message.SendRequest{Content: message.MessageContent(encoded)},
		message.SendMessageParams{QueueName: message.QueueName(inboundQueue)},
	)
	if err != nil {
		t.Fatalf("failed to send to SimpleMQ: %v", err)
	}

	received := receiveOneFromSimpleMQ(t, ctx, smqClient, destQueue)
	if received != testBody {
		t.Errorf("expected %q, got %q", testBody, received)
	}

	bridgeCancel()
}

func TestSimpleMQToSimpleMQFanout(t *testing.T) {
	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	inboundQueue := fmt.Sprintf("test-smq-fanout-inbound-%d", time.Now().UnixNano())
	destQueue1 := fmt.Sprintf("test-smq-fanout-dest1-%d", time.Now().UnixNano())
	destQueue2 := fmt.Sprintf("test-smq-fanout-dest2-%d", time.Now().UnixNano())
	destQueue3 := fmt.Sprintf("test-smq-fanout-dest3-%d", time.Now().UnixNano())

	cfg := &mqbridge.Config{
		RabbitMQ: mqbridge.RabbitMQConfig{URL: "amqp://dummy:dummy@localhost:5672/"},
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
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue1, APIKey: testAPIKey}},
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue2, APIKey: testAPIKey}},
					{SimpleMQ: &mqbridge.ToSimpleMQConfig{Queue: destQueue3, APIKey: testAPIKey}},
				},
			},
		},
	}

	app, err := mqbridge.New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	time.Sleep(500 * time.Millisecond)

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	encoded := base64.StdEncoding.EncodeToString([]byte(testBody))

	smqClient := newSimpleMQTestClient(t, smqServer.URL())
	_, err = smqClient.SendMessage(ctx,
		&message.SendRequest{Content: message.MessageContent(encoded)},
		message.SendMessageParams{QueueName: message.QueueName(inboundQueue)},
	)
	if err != nil {
		t.Fatalf("failed to send to SimpleMQ: %v", err)
	}

	for _, dq := range []string{destQueue1, destQueue2, destQueue3} {
		received := receiveOneFromSimpleMQ(t, ctx, smqClient, dq)
		if received != testBody {
			t.Errorf("queue %s: expected %q, got %q", dq, testBody, received)
		}
	}

	bridgeCancel()
}

func TestSimpleMQToRabbitMQ(t *testing.T) {
	conn := requireRabbitMQ(t)
	defer conn.Close()

	smqServer := localserver.NewServer()
	defer smqServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	inboundQueue := fmt.Sprintf("test-inbound-%d", time.Now().UnixNano())
	destExchange := fmt.Sprintf("test-dest-exchange-%d", time.Now().UnixNano())
	destQueue := fmt.Sprintf("test-dest-rmq-%d", time.Now().UnixNano())

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

	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(bridgeCtx)
	}()

	time.Sleep(500 * time.Millisecond)

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
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
