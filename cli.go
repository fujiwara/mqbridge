package mqbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/alecthomas/kong"
	"github.com/fujiwara/sloghandler"
)

// CLI defines the command-line interface for mqbridge.
type CLI struct {
	Config    string           `kong:"required,short='c',env='MQBRIDGE_CONFIG',help='Config file path (Jsonnet/JSON)'" `
	LogFormat string           `kong:"default='text',enum='text,json',env='MQBRIDGE_LOG_FORMAT',help='Log format (text or json)'" `
	LogLevel  string           `kong:"default='info',enum='debug,info,warn,error',env='MQBRIDGE_LOG_LEVEL',help='Log level (debug, info, warn, error)'" `
	Run       RunCmd           `cmd:"" help:"Run the bridge"`
	Validate  ValidateCmd      `cmd:"" help:"Validate config"`
	Render    RenderCmd        `cmd:"" help:"Render config as JSON to stdout"`
	Version   kong.VersionFlag `help:"Show version"`
}

// RunCLI parses command-line arguments and executes the appropriate subcommand.
func RunCLI(ctx context.Context) error {
	cli := &CLI{}
	kctx := kong.Parse(cli,
		kong.Name("mqbridge"),
		kong.Description("Message bridge between RabbitMQ and SimpleMQ"),
		kong.Vars{"version": Version},
		kong.BindTo(ctx, (*context.Context)(nil)),
	)
	setupLogger(cli.LogFormat, cli.LogLevel)
	shutdownMetrics, err := setupMeterProvider(ctx)
	if err != nil {
		return fmt.Errorf("failed to setup meter provider: %w", err)
	}
	defer shutdownMetrics(ctx)
	return kctx.Run(cli)
}

// RunCmd is the "run" subcommand.
type RunCmd struct{}

func (c *RunCmd) Run(ctx context.Context, globals *CLI) error {
	cfg, err := LoadConfig(ctx, globals.Config)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a, err := New(cfg)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

// ValidateCmd is the "validate" subcommand.
type ValidateCmd struct{}

func (c *ValidateCmd) Run(ctx context.Context, globals *CLI) error {
	cfg, err := LoadConfig(ctx, globals.Config)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	slog.Info("config is valid")
	return nil
}

// RenderCmd is the "render" subcommand.
type RenderCmd struct{}

func (c *RenderCmd) Run(ctx context.Context, globals *CLI) error {
	return RenderConfigTo(ctx, globals.Config, os.Stdout)
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func setupLogger(format, level string) {
	logLevel := parseLogLevel(level)
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{AddSource: true, Level: logLevel})
	default:
		handler = sloghandler.NewLogHandler(os.Stderr, &sloghandler.HandlerOptions{
			HandlerOptions: slog.HandlerOptions{Level: logLevel},
			Color:          true,
			Source:         true,
		})
	}
	slog.SetDefault(slog.New(handler))
}

// RenderConfigTo evaluates a config file and writes pretty-printed JSON to w.
func RenderConfigTo(ctx context.Context, path string, w io.Writer) error {
	data, err := RenderConfig(ctx, path)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		fmt.Fprintln(w, string(data))
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
