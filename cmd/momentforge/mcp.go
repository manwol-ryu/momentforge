package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/mcp"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// runMCP serves the data folder to an MCP client over stdin/stdout. The
// database is opened read-only, so it can run while momentforge serve is
// collecting; stdout carries the protocol and logs go to stderr.
func runMCP(args []string, stderr io.Writer) int {
	fl := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	data := fl.String("data", ".momentforge", "")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.OpenReadOnly(ctx, *data)
	if err != nil {
		if ue, ok := domain.AsUserError(err); ok {
			fmt.Fprintln(stderr, ue.Message)
		} else {
			fmt.Fprintln(stderr, "데이터를 열지 못했습니다:", err)
		}
		return 1
	}
	defer st.Close()
	// No bus, rules or AI providers: the tools only read.
	core := app.New(app.Config{Version: version, DataDir: *data, Store: st, Params: detect.DefaultParams(),
		Platforms: registerPlatforms(nil)})
	if err := mcp.Run(ctx, core, version); err != nil && ctx.Err() == nil {
		fmt.Fprintln(stderr, "MCP 서버가 멈췄습니다:", err)
		return 1
	}
	return 0
}
