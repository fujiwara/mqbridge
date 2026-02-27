package mqbridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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
	From Subscriber
	To   []Publisher
}

// Run starts the bridge, consuming messages from the subscriber
// and publishing to all publishers.
func (b *Bridge) Run(ctx context.Context) error {
	return b.From.Subscribe(ctx, func(ctx context.Context, msg []byte) error {
		for _, pub := range b.To {
			if err := pub.Publish(ctx, msg); err != nil {
				return fmt.Errorf("publish error: %w", err)
			}
		}
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
func (a *App) Run(ctx context.Context) error {
	slog.Info("starting mqbridge", "bridges", len(a.bridges))
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

func buildBridges(cfg *Config) ([]*Bridge, error) {
	var bridges []*Bridge
	for i, bc := range cfg.Bridges {
		bridge, err := buildBridge(cfg, bc)
		if err != nil {
			return nil, fmt.Errorf("bridges[%d]: %w", i, err)
		}
		bridges = append(bridges, bridge)
	}
	return bridges, nil
}

func buildBridge(cfg *Config, bc BridgeConfig) (*Bridge, error) {
	var sub Subscriber
	var pubs []Publisher

	if bc.From.RabbitMQ != nil {
		sub = NewRabbitMQSubscriber(cfg.RabbitMQ.URL, *bc.From.RabbitMQ)
	} else if bc.From.SimpleMQ != nil {
		var err error
		sub, err = NewSimpleMQSubscriber(cfg.SimpleMQ.APIURL, *bc.From.SimpleMQ)
		if err != nil {
			return nil, fmt.Errorf("failed to create SimpleMQ subscriber: %w", err)
		}
	}

	for j, to := range bc.To {
		if to.RabbitMQ != nil {
			pubs = append(pubs, NewRabbitMQPublisher(cfg.RabbitMQ.URL))
		} else if to.SimpleMQ != nil {
			pub, err := NewSimpleMQPublisher(cfg.SimpleMQ.APIURL, *to.SimpleMQ)
			if err != nil {
				return nil, fmt.Errorf("to[%d]: failed to create SimpleMQ publisher: %w", j, err)
			}
			pubs = append(pubs, pub)
		}
	}

	return &Bridge{From: sub, To: pubs}, nil
}
