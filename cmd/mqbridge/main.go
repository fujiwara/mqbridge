package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/fujiwara/mqbridge"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), signals()...)
	defer stop()
	if err := mqbridge.RunCLI(ctx); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
