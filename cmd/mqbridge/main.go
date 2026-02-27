package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/alecthomas/kong"
	app "github.com/fujiwara/mqbridge"
)

type CLI struct {
	Config   string           `kong:"required,short='c',help='Config file path (Jsonnet/JSON)'" `
	Run      RunCmd           `cmd:"" help:"Run the bridge"`
	Validate ValidateCmd      `cmd:"" help:"Validate config"`
	Render   RenderCmd        `cmd:"" help:"Render config as JSON to stdout"`
	Version  kong.VersionFlag `help:"Show version"`
}

type RunCmd struct{}

func (c *RunCmd) Run(globals *CLI) error {
	ctx, stop := signal.NotifyContext(context.Background(), signals()...)
	defer stop()

	cfg, err := app.LoadConfig(ctx, globals.Config)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a, err := app.New(cfg)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

type ValidateCmd struct{}

func (c *ValidateCmd) Run(globals *CLI) error {
	ctx := context.Background()
	cfg, err := app.LoadConfig(ctx, globals.Config)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	slog.Info("config is valid")
	return nil
}

type RenderCmd struct{}

func (c *RenderCmd) Run(globals *CLI) error {
	ctx := context.Background()
	data, err := app.RenderConfig(ctx, globals.Config)
	if err != nil {
		return err
	}
	// Pretty-print the JSON
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		// If not valid JSON, output as-is
		fmt.Println(string(data))
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func main() {
	cli := &CLI{}
	kctx := kong.Parse(cli,
		kong.Name("mqbridge"),
		kong.Description("Message bridge between RabbitMQ and SimpleMQ"),
		kong.Vars{"version": app.Version},
	)
	if err := kctx.Run(cli); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
