package main

import (
	"context"
	"os/signal"
	"syscall"

	"git.cooluc.com/sbwml/go-flagx"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	defer cancel()

	app := flagx.New()
	app.AddCommand("run", &XunleiDaemon{})
	app.Run(ctx)
}
