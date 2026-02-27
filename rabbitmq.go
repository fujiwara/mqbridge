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
}

// NewRabbitMQSubscriber creates a new RabbitMQSubscriber.
func NewRabbitMQSubscriber(url string, config FromRabbitMQConfig) *RabbitMQSubscriber {
	return &RabbitMQSubscriber{
		url:    url,
		config: config,
	}
}

// Subscribe starts consuming messages from the RabbitMQ queue.
// It declares the exchange and queue, binds them, and calls the handler for each message.
func (s *RabbitMQSubscriber) Subscribe(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error {
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
	if err := s.ch.ExchangeDeclare(
		s.config.Exchange,
		exchangeType,
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
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

	msgs, err := s.ch.ConsumeWithContext(
		ctx,
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

	slog.Info("RabbitMQ subscriber started", "queue", s.config.Queue, "exchange", s.config.Exchange)
	for {
		select {
		case <-ctx.Done():
			slog.Info("RabbitMQ subscriber stopping", "queue", s.config.Queue)
			return ctx.Err()
		case delivery, ok := <-msgs:
			if !ok {
				return fmt.Errorf("RabbitMQ channel closed for queue %q", s.config.Queue)
			}
			if err := handler(ctx, delivery.Body); err != nil {
				slog.Error("failed to handle message, nacking",
					"queue", s.config.Queue,
					"error", err,
				)
				if nackErr := delivery.Nack(false, true); nackErr != nil {
					slog.Error("failed to nack message", "error", nackErr)
				}
			} else {
				if ackErr := delivery.Ack(false); ackErr != nil {
					slog.Error("failed to ack message", "error", ackErr)
				}
			}
		}
	}
}

// Close closes the RabbitMQ connection.
func (s *RabbitMQSubscriber) Close() error {
	var errs []error
	if s.ch != nil {
		if err := s.ch.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.conn != nil {
		if err := s.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing RabbitMQ subscriber: %v", errs)
	}
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
	url  string
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewRabbitMQPublisher creates a new RabbitMQPublisher.
func NewRabbitMQPublisher(url string) *RabbitMQPublisher {
	return &RabbitMQPublisher{url: url}
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
	slog.Info("RabbitMQ publisher connected")
	return nil
}
