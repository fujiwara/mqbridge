package mqbridge_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/fujiwara/mqbridge"
	"github.com/fujiwara/simplemq-cli/localserver"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

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
	smqServer := localserver.NewServer(localserver.Config{APIKey: testAPIKey})
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
	e.publishToRabbitMQWithHeaders(exchange, routingKey, body, nil)
}

func (e *testEnv) publishToRabbitMQWithHeaders(exchange, routingKey, body string, headers amqp.Table) {
	e.t.Helper()
	ch, err := e.rmqConn.Channel()
	if err != nil {
		e.t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()
	err = ch.PublishWithContext(e.ctx, exchange, routingKey, false, false, amqp.Publishing{
		Body:    []byte(body),
		Headers: headers,
	})
	if err != nil {
		e.t.Fatalf("failed to publish: %v", err)
	}
}

func (e *testEnv) receiveFromSimpleMQ(queue string) *mqbridge.Message {
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
		return mqbridge.UnmarshalMessage(decoded)
	}
	return nil
}

func (e *testEnv) sendMessageToSimpleMQ(queue string, msg *mqbridge.Message) {
	e.t.Helper()
	data, err := mqbridge.MarshalMessage(msg)
	if err != nil {
		e.t.Fatalf("failed to marshal message: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	_, err = e.smqClient.SendMessage(e.ctx,
		&message.SendRequest{Content: message.MessageContent(encoded)},
		message.SendMessageParams{QueueName: message.QueueName(queue)},
	)
	if err != nil {
		e.t.Fatalf("failed to send to SimpleMQ: %v", err)
	}
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

	received := env.receiveFromSimpleMQ(dest)
	if received == nil {
		t.Fatal("expected message, got nil")
	}
	if string(received.Body) != testBody {
		t.Errorf("body: expected %q, got %q", testBody, string(received.Body))
	}
	if received.Headers[mqbridge.HeaderRabbitMQExchange] != exchange {
		t.Errorf("exchange header: expected %q, got %q", exchange, received.Headers[mqbridge.HeaderRabbitMQExchange])
	}
	if received.Headers[mqbridge.HeaderRabbitMQRoutingKey] != "test.key" {
		t.Errorf("routing_key header: expected %q, got %q", "test.key", received.Headers[mqbridge.HeaderRabbitMQRoutingKey])
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
		received := env.receiveFromSimpleMQ(dq)
		if received == nil {
			t.Errorf("queue %s: expected message, got nil", dq)
			continue
		}
		if string(received.Body) != testBody {
			t.Errorf("queue %s: expected %q, got %q", dq, testBody, string(received.Body))
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

	received := env.receiveFromSimpleMQ(dest)
	if received == nil {
		t.Fatal("expected message, got nil")
	}
	if string(received.Body) != testBody {
		t.Errorf("expected %q, got %q", testBody, string(received.Body))
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
	env.sendMessageToSimpleMQ(inbound, &mqbridge.Message{Body: []byte(testBody)})

	received := env.receiveFromSimpleMQ(dest)
	if received == nil {
		t.Fatal("expected message, got nil")
	}
	if string(received.Body) != testBody {
		t.Errorf("expected %q, got %q", testBody, string(received.Body))
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
	env.sendMessageToSimpleMQ(inbound, &mqbridge.Message{Body: []byte(testBody)})

	for _, dq := range []string{dest1, dest2, dest3} {
		received := env.receiveFromSimpleMQ(dq)
		if received == nil {
			t.Errorf("queue %s: expected message, got nil", dq)
			continue
		}
		if string(received.Body) != testBody {
			t.Errorf("queue %s: expected %q, got %q", dq, testBody, string(received.Body))
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
	env.sendMessageToSimpleMQ(inbound, &mqbridge.Message{
		Body: []byte(testBody),
		Headers: map[string]string{
			mqbridge.HeaderRabbitMQExchange:   destExchange,
			mqbridge.HeaderRabbitMQRoutingKey: "test.key",
		},
	})

	select {
	case delivery := <-deliveries:
		if string(delivery.Body) != testBody {
			t.Errorf("expected %q, got %q", testBody, string(delivery.Body))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for message in RabbitMQ")
	}
}

// traceparentRe matches a valid W3C traceparent header value.
var traceparentRe = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

func setupTestTracerProvider(t *testing.T) {
	t.Helper()
	// If OTEL_EXPORTER_OTLP_ENDPOINT is set, use the real provider so
	// spans are exported to the collector for manual inspection.
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		shutdown, err := mqbridge.SetupOTelProvidersForTest(context.Background())
		if err != nil {
			t.Fatalf("failed to setup OTel providers: %v", err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(ctx); err != nil {
				t.Logf("OTel shutdown: %v", err)
			}
		})
		return
	}
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { tp.Shutdown(t.Context()) })
}

// TestRabbitMQToSimpleMQTraceInjection verifies that the bridge injects
// a traceparent header into outgoing SimpleMQ messages even when the
// incoming RabbitMQ message has no trace context.
func TestRabbitMQToSimpleMQTraceInjection(t *testing.T) {
	setupTestTracerProvider(t)

	env := newTestEnv(t, true)
	exchange := env.uniqueName("trace-inj-exchange")
	queue := env.uniqueName("trace-inj-queue")
	dest := env.uniqueName("trace-inj-dest")

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

	received := env.receiveFromSimpleMQ(dest)
	if received == nil {
		t.Fatal("expected message, got nil")
	}
	if string(received.Body) != testBody {
		t.Errorf("body: expected %q, got %q", testBody, string(received.Body))
	}
	tp := received.Headers["traceparent"]
	if !traceparentRe.MatchString(tp) {
		t.Errorf("expected valid traceparent header, got %q", tp)
	}
	t.Logf("traceparent: %s", tp)
}

// TestRabbitMQToSimpleMQTracePreservation verifies that when a RabbitMQ message
// carries a traceparent AMQP header, the bridge preserves the same trace_id
// in the outgoing SimpleMQ message.
func TestRabbitMQToSimpleMQTracePreservation(t *testing.T) {
	setupTestTracerProvider(t)

	env := newTestEnv(t, true)
	exchange := env.uniqueName("trace-pres-exchange")
	queue := env.uniqueName("trace-pres-queue")
	dest := env.uniqueName("trace-pres-dest")

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

	// Publish with a known traceparent AMQP header
	originalTraceID := "abcdef1234567890abcdef1234567890"
	originalTraceparent := "00-" + originalTraceID + "-1234567890abcdef-01"
	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.publishToRabbitMQWithHeaders(exchange, "test.key", testBody, amqp.Table{
		"traceparent": originalTraceparent,
	})

	received := env.receiveFromSimpleMQ(dest)
	if received == nil {
		t.Fatal("expected message, got nil")
	}
	tp := received.Headers["traceparent"]
	if !traceparentRe.MatchString(tp) {
		t.Fatalf("expected valid traceparent, got %q", tp)
	}
	// The trace_id (32 hex chars after "00-") must match the original
	receivedTraceID := tp[3:35]
	if receivedTraceID != originalTraceID {
		t.Errorf("trace_id: expected %q, got %q", originalTraceID, receivedTraceID)
	}
	t.Logf("original traceparent: %s", originalTraceparent)
	t.Logf("received traceparent: %s", tp)
}

// TestRabbitMQToSimpleMQFanoutTraceID verifies that all fan-out destinations
// receive the same trace_id.
func TestRabbitMQToSimpleMQFanoutTraceID(t *testing.T) {
	setupTestTracerProvider(t)

	env := newTestEnv(t, true)
	exchange := env.uniqueName("trace-fan-exchange")
	queue := env.uniqueName("trace-fan-queue")
	dest1 := env.uniqueName("trace-fan-dest1")
	dest2 := env.uniqueName("trace-fan-dest2")

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
			},
		},
	})
	defer stop()

	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.publishToRabbitMQ(exchange, "test.key", testBody)

	recv1 := env.receiveFromSimpleMQ(dest1)
	recv2 := env.receiveFromSimpleMQ(dest2)
	if recv1 == nil || recv2 == nil {
		t.Fatal("expected messages on both destinations")
	}
	tp1 := recv1.Headers["traceparent"]
	tp2 := recv2.Headers["traceparent"]
	if !traceparentRe.MatchString(tp1) || !traceparentRe.MatchString(tp2) {
		t.Fatalf("invalid traceparent: dest1=%q, dest2=%q", tp1, tp2)
	}
	traceID1 := tp1[3:35]
	traceID2 := tp2[3:35]
	if traceID1 != traceID2 {
		t.Errorf("trace_id mismatch across fan-out: dest1=%q, dest2=%q", traceID1, traceID2)
	}
	t.Logf("fan-out trace_id: %s", traceID1)
}

// TestSimpleMQToSimpleMQTracePreservation verifies trace context propagation
// through SimpleMQ → SimpleMQ bridging.
func TestSimpleMQToSimpleMQTracePreservation(t *testing.T) {
	setupTestTracerProvider(t)

	env := newTestEnv(t, false)
	inbound := env.uniqueName("trace-smq-inbound")
	dest := env.uniqueName("trace-smq-dest")

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

	originalTraceID := "11223344556677889900aabbccddeeff"
	originalTraceparent := "00-" + originalTraceID + "-aabbccddeeff0011-01"
	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.sendMessageToSimpleMQ(inbound, &mqbridge.Message{
		Body: []byte(testBody),
		Headers: map[string]string{
			"traceparent": originalTraceparent,
		},
	})

	received := env.receiveFromSimpleMQ(dest)
	if received == nil {
		t.Fatal("expected message, got nil")
	}
	tp := received.Headers["traceparent"]
	if !traceparentRe.MatchString(tp) {
		t.Fatalf("expected valid traceparent, got %q", tp)
	}
	receivedTraceID := tp[3:35]
	if receivedTraceID != originalTraceID {
		t.Errorf("trace_id: expected %q, got %q", originalTraceID, receivedTraceID)
	}
	t.Logf("original: %s", originalTraceparent)
	t.Logf("received: %s", tp)
}

// TestSimpleMQToRabbitMQTracePreservation verifies that trace context from
// a SimpleMQ message is propagated to the RabbitMQ AMQP header.
func TestSimpleMQToRabbitMQTracePreservation(t *testing.T) {
	setupTestTracerProvider(t)

	env := newTestEnv(t, true)
	inbound := env.uniqueName("trace-s2r-inbound")
	destExchange := env.uniqueName("trace-s2r-exchange")
	destQueue := env.uniqueName("trace-s2r-rmq")

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

	originalTraceID := "ffeeddccbbaa00998877665544332211"
	originalTraceparent := "00-" + originalTraceID + "-0011223344556677-01"
	testBody := fmt.Sprintf("msg-%s-%d", t.Name(), time.Now().UnixNano())
	env.sendMessageToSimpleMQ(inbound, &mqbridge.Message{
		Body: []byte(testBody),
		Headers: map[string]string{
			mqbridge.HeaderRabbitMQExchange:   destExchange,
			mqbridge.HeaderRabbitMQRoutingKey: "test.key",
			"traceparent":                     originalTraceparent,
		},
	})

	select {
	case delivery := <-deliveries:
		if string(delivery.Body) != testBody {
			t.Errorf("body: expected %q, got %q", testBody, string(delivery.Body))
		}
		// traceparent is injected into message headers, which become
		// rabbitmq.header.traceparent in the AMQP custom headers.
		tp, ok := delivery.Headers["traceparent"]
		if !ok {
			t.Fatal("expected traceparent AMQP header")
		}
		tpStr := fmt.Sprintf("%v", tp)
		if !traceparentRe.MatchString(tpStr) {
			t.Fatalf("expected valid traceparent, got %q", tpStr)
		}
		receivedTraceID := tpStr[3:35]
		if receivedTraceID != originalTraceID {
			t.Errorf("trace_id: expected %q, got %q", originalTraceID, receivedTraceID)
		}
		t.Logf("AMQP traceparent header: %s", tpStr)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for message in RabbitMQ")
	}
}
