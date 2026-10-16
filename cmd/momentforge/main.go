// Command momentforge runs the local MomentForge server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/web"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `MomentForge — 방송 채팅에서 편집할 순간을 찾는 로컬 앱

사용법:
  momentforge [serve] [옵션]   로컬 서버를 실행하고 브라우저를 엽니다
  momentforge version          버전을 출력합니다

serve 옵션:
  --port 8765          사용할 포트 (127.0.0.1에만 열립니다)
  --data .momentforge  데이터 폴더 (채팅 DB·내보내기·실행 정보)
  --no-browser         브라우저를 자동으로 열지 않습니다
`

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args, stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, "momentforge", version)
		return 0
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "알 수 없는 명령입니다: %s\n\n%s", cmd, usage)
		return 2
	}
}

func serve(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	port := fl.Int("port", 8765, "")
	data := fl.String("data", ".momentforge", "")
	noBrowser := fl.Bool("no-browser", false, "")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := start(ctx, options{Port: *port, DataDir: *data, Version: version, Assets: web.Dist()})
	if err != nil {
		if ue, ok := domain.AsUserError(err); ok {
			fmt.Fprintln(stderr, ue.Message)
		} else {
			fmt.Fprintln(stderr, "시작하지 못했습니다:", err)
		}
		return 1
	}
	fmt.Fprintf(stdout, "MomentForge %s: %s\n데이터 폴더: %s\n종료: Ctrl+C\n", version, srv.URL, srv.DataDir)
	if !*noBrowser {
		openBrowser(srv.URL, stderr)
	}

	code := 0
	select {
	case <-ctx.Done():
	case err := <-srv.Failed():
		fmt.Fprintln(stderr, "서버가 멈췄습니다:", err)
		code = 1
	}
	fmt.Fprintln(stdout, "종료하는 중입니다…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintln(stderr, "종료 중 오류:", err)
		code = 1
	}
	return code
}

func openBrowser(url string, stderr io.Writer) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		fmt.Fprintf(stderr, "브라우저를 열지 못했습니다. 직접 접속하세요: %s\n", url)
		return
	}
	go c.Wait()
}
