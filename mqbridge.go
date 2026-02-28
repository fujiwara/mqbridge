package mqbridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Subscriber consumes messages from a source.
type Subscriber interface {
	Subscribe(ctx context.Context, handler func(ctx context.Context, msg []byte) error) error
	Close() error
}

// PublishResult holds metadata about a published message.
type PublishResult struct {
	Destination string // destination identifier (e.g. queue name, "exchange/routing_key")
}

// Publisher sends messages to a destination.
type Publisher interface {
	Publish(ctx context.Context, msg []byte) (*PublishResult, error)
	// Type returns the publisher type name (e.g. "rabbitmq", "simplemq").
	Type() string
	Close() error
}

// Bridge connects one Subscriber to multiple Publishers.
type Bridge struct {
	From       Subscriber
	To         []Publisher
	metrics    *Metrics
	srcAttrs   attribute.Set
	srcType    string
	srcQueue   string
	bridgeName string
	logger     *slog.Logger
}

// Run starts the bridge, consuming messages from the subscriber
// and publishing to all publishers.
func (b *Bridge) Run(ctx context.Context) error {
	return b.From.Subscribe(ctx, func(ctx context.Context, msg []byte) error {
		b.metrics.messagesReceived.Add(ctx, 1, metric.WithAttributeSet(b.srcAttrs))
		b.logger.Debug("message received",
			"source_type", b.srcType,
			"source_queue", b.srcQueue,
			"size", len(msg),
		)
		start := time.Now()
		for _, pub := range b.To {
			result, err := pub.Publish(ctx, msg)
			if err != nil {
				b.metrics.messageErrors.Add(ctx, 1, metric.WithAttributeSet(b.srcAttrs))
				b.logger.Error("failed to publish message",
					"destination_type", pub.Type(),
					"error", err,
				)
				return fmt.Errorf("publish error: %w", err)
			}
			dstAttrs := attribute.NewSet(
				attribute.String("bridge", b.bridgeName),
				attribute.String("destination_type", pub.Type()),
				attribute.String("destination_queue", result.Destination),
			)
			b.metrics.messagesPublished.Add(ctx, 1, metric.WithAttributeSet(dstAttrs))
			b.logger.Debug("message published",
				"destination_type", pub.Type(),
				"destination_queue", result.Destination,
			)
		}
		b.metrics.processingDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributeSet(b.srcAttrs))
		return nil
	})
}

// Close closes all subscribers and publishers.
func (b *Bridge) Close() error {
	var errs []error
	if err := b.From.Close(); err != nil {
		errs = append(errs, err)
	}
	for _, pub := range b.To {
		if err := pub.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing bridge: %v", errs)
	}
	return nil
}

// App holds the application state.
type App struct {
	Config  *Config
	bridges []*Bridge
}

// New creates a new App from a config.
func New(cfg *Config) (*App, error) {
	bridges, err := buildBridges(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build bridges: %w", err)
	}
	return &App{
		Config:  cfg,
		bridges: bridges,
	}, nil
}

// Run starts all bridges concurrently and waits for context cancellation.
func (a *App) Run(ctx context.Context) error {
	slog.Info("starting mqbridge", "bridges", len(a.bridges))

	var wg sync.WaitGroup
	for _, b := range a.bridges {
		wg.Add(1)
		go func(bridge *Bridge) {
			defer wg.Done()
			bridge.logger.Info("starting bridge")
			if err := bridge.Run(ctx); err != nil {
				if ctx.Err() != nil {
					return // expected: context cancelled by signal
				}
				panic(fmt.Sprintf("unexpected bridge error: %v", err))
			}
		}(b)
	}

	wg.Wait()

	// Close all bridges
	for _, b := range a.bridges {
		if err := b.Close(); err != nil {
			slog.Error("error closing bridge", "error", err)
		}
	}
	return nil
}

func buildBridges(cfg *Config) ([]*Bridge, error) {
	m, err := newMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics: %w", err)
	}
	var bridges []*Bridge
	for i, bc := range cfg.Bridges {
		var bridgeLabel string
		if bc.Name != "" {
			bridgeLabel = bc.Name
		} else {
			bridgeLabel = fmt.Sprintf("%d", i)
		}
		logger := slog.Default().With("bridge", bridgeLabel)
		bridge, err := buildBridge(cfg, bc, m, bridgeLabel, logger)
		if err != nil {
			return nil, fmt.Errorf("bridges[%d]: %w", i, err)
		}
		bridges = append(bridges, bridge)
	}
	return bridges, nil
}

func buildBridge(cfg *Config, bc BridgeConfig, m *Metrics, bridgeLabel string, logger *slog.Logger) (*Bridge, error) {
	var sub Subscriber
	var pubs []Publisher

	var srcType, srcQueue string
	if bc.From.RabbitMQ != nil {
		sub = NewRabbitMQSubscriber(cfg.RabbitMQ.URL, *bc.From.RabbitMQ, logger)
		srcType = "rabbitmq"
		srcQueue = bc.From.RabbitMQ.Queue
	} else if bc.From.SimpleMQ != nil {
		var err error
		sub, err = NewSimpleMQSubscriber(cfg.SimpleMQ.APIURL, *bc.From.SimpleMQ, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create SimpleMQ subscriber: %w", err)
		}
		srcType = "simplemq"
		srcQueue = bc.From.SimpleMQ.Queue
	}

	srcAttrs := attribute.NewSet(
		attribute.String("bridge", bridgeLabel),
		attribute.String("source_type", srcType),
		attribute.String("source_queue", srcQueue),
	)

	for j, to := range bc.To {
		if to.RabbitMQ != nil {
			pubs = append(pubs, NewRabbitMQPublisher(cfg.RabbitMQ.URL, logger))
		} else if to.SimpleMQ != nil {
			pub, err := NewSimpleMQPublisher(cfg.SimpleMQ.APIURL, *to.SimpleMQ)
			if err != nil {
				return nil, fmt.Errorf("to[%d]: failed to create SimpleMQ publisher: %w", j, err)
			}
			pubs = append(pubs, pub)
		}
	}

	return &Bridge{
		From:       sub,
		To:         pubs,
		metrics:    m,
		srcAttrs:   srcAttrs,
		srcType:    srcType,
		srcQueue:   srcQueue,
		bridgeName: bridgeLabel,
		logger:     logger,
	}, nil
}
