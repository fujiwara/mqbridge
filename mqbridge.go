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

// Publisher sends messages to a destination.
type Publisher interface {
	Publish(ctx context.Context, msg []byte) error
	Close() error
}

// Bridge connects one Subscriber to multiple Publishers.
type Bridge struct {
	From     Subscriber
	To       []Publisher
	metrics  *Metrics
	srcAttrs attribute.Set
	dstAttrs []attribute.Set
}

// Run starts the bridge, consuming messages from the subscriber
// and publishing to all publishers.
func (b *Bridge) Run(ctx context.Context) error {
	return b.From.Subscribe(ctx, func(ctx context.Context, msg []byte) error {
		b.metrics.messagesReceived.Add(ctx, 1, metric.WithAttributeSet(b.srcAttrs))
		start := time.Now()
		for i, pub := range b.To {
			if err := pub.Publish(ctx, msg); err != nil {
				b.metrics.messageErrors.Add(ctx, 1, metric.WithAttributeSet(b.srcAttrs))
				return fmt.Errorf("publish error: %w", err)
			}
			b.metrics.messagesPublished.Add(ctx, 1, metric.WithAttributeSet(b.dstAttrs[i]))
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
		return nil, err
	}
	return &App{
		Config:  cfg,
		bridges: bridges,
	}, nil
}

// Run starts all bridges concurrently and waits for them to complete or context cancellation.
// If any bridge fails, all other bridges are cancelled.
func (a *App) Run(ctx context.Context) error {
	slog.Info("starting mqbridge", "bridges", len(a.bridges))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, len(a.bridges))

	for i, b := range a.bridges {
		wg.Add(1)
		go func(idx int, bridge *Bridge) {
			defer wg.Done()
			slog.Info("starting bridge", "index", idx)
			if err := bridge.Run(ctx); err != nil && ctx.Err() == nil {
				slog.Error("bridge error", "index", idx, "error", err)
				errCh <- fmt.Errorf("bridge[%d]: %w", idx, err)
				cancel()
			}
		}(i, b)
	}

	wg.Wait()
	close(errCh)

	// Close all bridges
	for _, b := range a.bridges {
		if err := b.Close(); err != nil {
			slog.Error("error closing bridge", "error", err)
		}
	}

	// Return first error if any
	for err := range errCh {
		return err
	}
	return nil
}

// DestinationInfo holds type and queue name for a destination, used for test bridge construction.
type DestinationInfo struct {
	Type  string
	Queue string
}

// NewBridgeForTest creates a Bridge with the given subscriber, publishers, and metric attributes.
// This is intended for testing only.
func NewBridgeForTest(sub Subscriber, pubs []Publisher, srcType, srcQueue string, dsts []DestinationInfo) *Bridge {
	m, _ := newMetrics()
	srcAttrs := attribute.NewSet(
		attribute.String("source_type", srcType),
		attribute.String("source_queue", srcQueue),
	)
	var dstAttrs []attribute.Set
	for _, dst := range dsts {
		dstAttrs = append(dstAttrs, attribute.NewSet(
			attribute.String("destination_type", dst.Type),
			attribute.String("destination_queue", dst.Queue),
		))
	}
	return &Bridge{
		From:     sub,
		To:       pubs,
		metrics:  m,
		srcAttrs: srcAttrs,
		dstAttrs: dstAttrs,
	}
}

func buildBridges(cfg *Config) ([]*Bridge, error) {
	m, err := newMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics: %w", err)
	}
	var bridges []*Bridge
	for i, bc := range cfg.Bridges {
		bridge, err := buildBridge(cfg, bc, m)
		if err != nil {
			return nil, fmt.Errorf("bridges[%d]: %w", i, err)
		}
		bridges = append(bridges, bridge)
	}
	return bridges, nil
}

func buildBridge(cfg *Config, bc BridgeConfig, m *Metrics) (*Bridge, error) {
	var sub Subscriber
	var pubs []Publisher

	var srcType, srcQueue string
	if bc.From.RabbitMQ != nil {
		sub = NewRabbitMQSubscriber(cfg.RabbitMQ.URL, *bc.From.RabbitMQ)
		srcType = "rabbitmq"
		srcQueue = bc.From.RabbitMQ.Queue
	} else if bc.From.SimpleMQ != nil {
		var err error
		sub, err = NewSimpleMQSubscriber(cfg.SimpleMQ.APIURL, *bc.From.SimpleMQ)
		if err != nil {
			return nil, fmt.Errorf("failed to create SimpleMQ subscriber: %w", err)
		}
		srcType = "simplemq"
		srcQueue = bc.From.SimpleMQ.Queue
	}

	srcAttrs := attribute.NewSet(
		attribute.String("source_type", srcType),
		attribute.String("source_queue", srcQueue),
	)

	var dstAttrs []attribute.Set
	for j, to := range bc.To {
		if to.RabbitMQ != nil {
			pubs = append(pubs, NewRabbitMQPublisher(cfg.RabbitMQ.URL))
			dstAttrs = append(dstAttrs, attribute.NewSet(
				attribute.String("destination_type", "rabbitmq"),
				attribute.String("destination_queue", ""),
			))
		} else if to.SimpleMQ != nil {
			pub, err := NewSimpleMQPublisher(cfg.SimpleMQ.APIURL, *to.SimpleMQ)
			if err != nil {
				return nil, fmt.Errorf("to[%d]: failed to create SimpleMQ publisher: %w", j, err)
			}
			pubs = append(pubs, pub)
			dstAttrs = append(dstAttrs, attribute.NewSet(
				attribute.String("destination_type", "simplemq"),
				attribute.String("destination_queue", to.SimpleMQ.Queue),
			))
		}
	}

	return &Bridge{
		From:     sub,
		To:       pubs,
		metrics:  m,
		srcAttrs: srcAttrs,
		dstAttrs: dstAttrs,
	}, nil
}
