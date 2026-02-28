package mqbridge

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQSubscriber consumes messages from a RabbitMQ queue.
type RabbitMQSubscriber struct {
	url    string
	config FromRabbitMQConfig
	conn   *amqp.Connection
	ch     *amqp.Channel
	logger *slog.Logger
}

// NewRabbitMQSubscriber creates a new RabbitMQSubscriber.
func NewRabbitMQSubscriber(url string, config FromRabbitMQConfig, logger *slog.Logger) *RabbitMQSubscriber {
	return &RabbitMQSubscriber{
		url:    url,
		config: config,
		logger: logger,
	}
}

// Subscribe starts consuming messages from the RabbitMQ queue with automatic
// reconnection on connection loss. It uses exponential backoff between retries.
func (s *RabbitMQSubscriber) Subscribe(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error {
	const (
		initialBackoff = 1 * time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff
	var attempt int
	for {
		start := time.Now()
		err := s.subscribeOnce(ctx, handler)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Reset backoff if the connection was alive long enough,
		// indicating a transient failure rather than a persistent one.
		if time.Since(start) > maxBackoff {
			backoff = initialBackoff
			attempt = 0
		}
		attempt++
		s.logger.Error("connection lost, reconnecting...", "queue", s.config.Queue, "error", err, "attempt", attempt, "backoff", backoff)
		s.closeConn()

		// Wait with backoff, respecting context cancellation.
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// subscribeOnce connects to RabbitMQ, declares the exchange/queue, and consumes
// messages until the connection is lost or the context is cancelled.
func (s *RabbitMQSubscriber) subscribeOnce(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error {
	if err := s.connect(); err != nil {
		return err
	}
	exchangeType := s.config.ExchangeType
	if exchangeType == "" {
		exchangeType = "direct"
	}
	routingKey := s.config.RoutingKey
	if routingKey == "" {
		routingKey = "#"
	}
	var err error
	if s.config.ExchangePassive {
		err = s.ch.ExchangeDeclarePassive(
			s.config.Exchange,
			exchangeType,
			true,  // durable
			false, // auto-deleted
			false, // internal
			false, // no-wait
			nil,
		)
	} else {
		err = s.ch.ExchangeDeclare(
			s.config.Exchange,
			exchangeType,
			true,  // durable
			false, // auto-deleted
			false, // internal
			false, // no-wait
			nil,
		)
	}
	if err != nil {
		return fmt.Errorf("failed to declare exchange %q: %w", s.config.Exchange, err)
	}
	if _, err := s.ch.QueueDeclare(
		s.config.Queue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	); err != nil {
		return fmt.Errorf("failed to declare queue %q: %w", s.config.Queue, err)
	}
	if err := s.ch.QueueBind(
		s.config.Queue,
		routingKey,
		s.config.Exchange,
		false, // no-wait
		nil,
	); err != nil {
		return fmt.Errorf("failed to bind queue %q to exchange %q: %w", s.config.Queue, s.config.Exchange, err)
	}

	msgs, err := s.ch.Consume(
		s.config.Queue,
		"",    // consumer tag
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %q: %w", s.config.Queue, err)
	}

	s.logger.Info("RabbitMQ subscriber started", "queue", s.config.Queue, "exchange", s.config.Exchange)
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("RabbitMQ subscriber stopping", "queue", s.config.Queue)
			// Drain remaining deliveries so the AMQP reader goroutine is not
			// blocked when ch.Close() is called later. Unacked messages will be
			// redelivered by RabbitMQ.
			go func() {
				for range msgs {
				}
			}()
			return ctx.Err()
		case delivery, ok := <-msgs:
			if !ok {
				return fmt.Errorf("RabbitMQ channel closed for queue %q", s.config.Queue)
			}
			// Use a non-cancellable context so in-flight message processing
			// completes even when the parent context is cancelled.
			if err := handler(context.WithoutCancel(ctx), delivery.Body); err != nil {
				s.logger.Error("failed to handle message, nacking",
					"queue", s.config.Queue,
					"error", err,
				)
				if nackErr := delivery.Nack(false, true); nackErr != nil {
					s.logger.Error("failed to nack message", "error", nackErr)
				}
			} else {
				if ackErr := delivery.Ack(false); ackErr != nil {
					s.logger.Error("failed to ack message", "error", ackErr)
				}
			}
		}
	}
}

// closeConn closes the subscriber's channel and connection, ignoring errors.
// It is safe to call multiple times.
func (s *RabbitMQSubscriber) closeConn() {
	if s.ch != nil {
		s.ch.Close()
		s.ch = nil
	}
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

// Close closes the RabbitMQ connection.
func (s *RabbitMQSubscriber) Close() error {
	s.closeConn()
	return nil
}

func (s *RabbitMQSubscriber) connect() error {
	conn, err := amqp.Dial(s.url)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}
	s.conn = conn
	s.ch = ch
	return nil
}

// RabbitMQPublisher publishes messages to RabbitMQ.
// The destination exchange and routing key are determined by the message JSON.
type RabbitMQPublisher struct {
	url    string
	conn   *amqp.Connection
	ch     *amqp.Channel
	logger *slog.Logger
}

// NewRabbitMQPublisher creates a new RabbitMQPublisher.
func NewRabbitMQPublisher(url string, logger *slog.Logger) *RabbitMQPublisher {
	return &RabbitMQPublisher{url: url, logger: logger}
}

// Publish parses the message JSON and publishes to the specified exchange/routing key.
func (p *RabbitMQPublisher) Publish(ctx context.Context, msg []byte) (*PublishResult, error) {
	if err := p.ensureConnected(); err != nil {
		return nil, err
	}
	rmqMsg, err := ParseRabbitMQMessage(msg)
	if err != nil {
		return nil, err
	}
	headers := make(amqp.Table)
	for k, v := range rmqMsg.Headers {
		headers[k] = v
	}
	if err := p.ch.PublishWithContext(
		ctx,
		rmqMsg.Exchange,
		rmqMsg.RoutingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			Headers:      headers,
			Body:         []byte(rmqMsg.Body),
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now(),
		},
	); err != nil {
		return nil, err
	}
	return &PublishResult{
		Destination: rmqMsg.Exchange,
	}, nil
}

// Type returns the publisher type name.
func (p *RabbitMQPublisher) Type() string { return "rabbitmq" }

// Close closes the RabbitMQ connection.
func (p *RabbitMQPublisher) Close() error {
	var errs []error
	if p.ch != nil {
		if err := p.ch.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.conn != nil {
		if err := p.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing RabbitMQ publisher: %v", errs)
	}
	return nil
}

func (p *RabbitMQPublisher) ensureConnected() error {
	if p.conn != nil && !p.conn.IsClosed() {
		return nil
	}
	conn, err := amqp.Dial(p.url)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}
	p.conn = conn
	p.ch = ch
	p.logger.Info("RabbitMQ publisher connected")
	return nil
}
