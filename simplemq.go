package mqbridge

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	simplemq "github.com/sacloud/simplemq-api-go"
	"github.com/sacloud/simplemq-api-go/apis/v1/message"
)

// apiKeySource implements message.SecuritySource.
type apiKeySource struct {
	apiKey string
}

func (s *apiKeySource) ApiKeyAuth(_ context.Context, _ message.OperationName) (message.ApiKeyAuth, error) {
	return message.ApiKeyAuth{Token: s.apiKey}, nil
}

func newSimpleMQClient(apiURL, apiKey string) (*message.Client, error) {
	if apiURL == "" {
		apiURL = simplemq.DefaultMessageAPIRootURL
	}
	return message.NewClient(apiURL, &apiKeySource{apiKey: apiKey})
}

// SimpleMQSubscriber receives messages from a SimpleMQ queue via polling.
type SimpleMQSubscriber struct {
	client          *message.Client
	queueName       string
	pollingInterval time.Duration
	logger          *slog.Logger
}

// NewSimpleMQSubscriber creates a new SimpleMQSubscriber.
func NewSimpleMQSubscriber(apiURL string, config FromSimpleMQConfig, logger *slog.Logger) (*SimpleMQSubscriber, error) {
	client, err := newSimpleMQClient(apiURL, config.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create SimpleMQ client: %w", err)
	}
	return &SimpleMQSubscriber{
		client:          client,
		queueName:       config.Queue,
		pollingInterval: config.GetPollingInterval(),
		logger:          logger,
	}, nil
}

// Subscribe polls the SimpleMQ queue and calls the handler for each message.
func (s *SimpleMQSubscriber) Subscribe(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error {
	s.logger.Info("SimpleMQ subscriber started", "queue", s.queueName, "interval", s.pollingInterval)
	ticker := time.NewTicker(s.pollingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("SimpleMQ subscriber stopping", "queue", s.queueName)
			return ctx.Err()
		case <-ticker.C:
			if err := s.poll(ctx, handler); err != nil {
				s.logger.Error("SimpleMQ poll error", "queue", s.queueName, "error", err)
			}
		}
	}
}

func (s *SimpleMQSubscriber) poll(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error {
	res, err := s.client.ReceiveMessage(ctx, message.ReceiveMessageParams{
		QueueName: message.QueueName(s.queueName),
	})
	if err != nil {
		return fmt.Errorf("failed to receive message: %w", err)
	}
	recvOK, ok := res.(*message.ReceiveMessageOK)
	if !ok {
		return fmt.Errorf("unexpected response type: %T", res)
	}
	for _, msg := range recvOK.Messages {
		body, err := base64.StdEncoding.DecodeString(string(msg.Content))
		if err != nil {
			s.logger.Error("failed to decode message content", "queue", s.queueName, "error", err)
			continue
		}
		if err := handler(ctx, body); err != nil {
			s.logger.Error("failed to handle message, skipping delete",
				"queue", s.queueName,
				"messageId", msg.ID,
				"error", err,
			)
			continue
		}
		// Delete message after successful handling
		if _, err := s.client.DeleteMessage(ctx, message.DeleteMessageParams{
			QueueName: message.QueueName(s.queueName),
			MessageId: msg.ID,
		}); err != nil {
			s.logger.Error("failed to delete message", "queue", s.queueName, "messageId", msg.ID, "error", err)
		}
	}
	return nil
}

// Close is a no-op for SimpleMQSubscriber (HTTP client needs no explicit close).
func (s *SimpleMQSubscriber) Close() error {
	return nil
}

// SimpleMQPublisher sends messages to a SimpleMQ queue.
type SimpleMQPublisher struct {
	client    *message.Client
	queueName string
}

// NewSimpleMQPublisher creates a new SimpleMQPublisher.
func NewSimpleMQPublisher(apiURL string, config ToSimpleMQConfig) (*SimpleMQPublisher, error) {
	client, err := newSimpleMQClient(apiURL, config.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create SimpleMQ client: %w", err)
	}
	return &SimpleMQPublisher{
		client:    client,
		queueName: config.Queue,
	}, nil
}

// Publish sends a message to the SimpleMQ queue.
// The message body is base64-encoded before sending.
func (p *SimpleMQPublisher) Publish(ctx context.Context, msg []byte) (*PublishResult, error) {
	encoded := base64.StdEncoding.EncodeToString(msg)
	res, err := p.client.SendMessage(ctx,
		&message.SendRequest{Content: message.MessageContent(encoded)},
		message.SendMessageParams{QueueName: message.QueueName(p.queueName)},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send message to SimpleMQ queue %q: %w", p.queueName, err)
	}
	if _, ok := res.(*message.SendMessageOK); !ok {
		return nil, fmt.Errorf("unexpected response type from SimpleMQ: %T", res)
	}
	return &PublishResult{Destination: p.queueName}, nil
}

// Type returns the publisher type name.
func (p *SimpleMQPublisher) Type() string { return "simplemq" }

// Close is a no-op for SimpleMQPublisher.
func (p *SimpleMQPublisher) Close() error {
	return nil
}
