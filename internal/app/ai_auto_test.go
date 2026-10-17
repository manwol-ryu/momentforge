package app

import (
	"context"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// toolKeyAI finds its key in its own program's config, like CLIProxyAPI.
type toolKeyAI struct {
	*fakeAI
	id  string
	key string
}

func (p *toolKeyAI) ID() string                { return p.id }
func (p *toolKeyAI) FindKey() (string, string) { return p.key, "/home/u/.cli-proxy-api/config.yaml" }

// readyAI signs in by itself (Codex, OpenCode) and is ready or not.
type readyAI struct {
	*fakeAI
	id    string
	ready error
}

func (p *readyAI) ID() string       { return p.id }
func (p *readyAI) Ready() error     { return p.ready }
func (p *readyAI) AuthHelp() string { return "자체 로그인" }

// serviceAI runs a local program that can be started and stopped.
type serviceAI struct {
	*fakeAI
	running bool
}

func (p *serviceAI) ID() string { return "svc" }
func (p *serviceAI) ServiceStatus(context.Context) ai.ServiceStatus {
	return ai.ServiceStatus{Installed: true, Running: p.running, Managed: p.running, Program: "/bin/svc"}
}
func (p *serviceAI) StartService(context.Context) error { p.running = true; return nil }
func (p *serviceAI) StopService(context.Context) error  { p.running = false; return nil }

func appWith(t *testing.T, providers ...ai.Provider) *App {
	t.Helper()
	a := newAIApp(t, &fakeAI{})
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(providers...)
	a.initAI()
	a.settings.auto = ""
	a.chooseStartProvider()
	return a
}

func TestFoundKeyIsUsedInMemoryOnly(t *testing.T) {
	a := appWith(t, &toolKeyAI{fakeAI: &fakeAI{}, id: "proxy", key: "mf-found"})
	p := a.AIProviders()[0]
	if !p.Configured || p.KeySource != KeyFromTool || p.KeyFoundIn != "/home/u/.cli-proxy-api/config.yaml" {
		t.Fatalf("provider %+v", p)
	}
	if a.ai.keys["proxy"] != "mf-found" {
		t.Fatal("the found key is not used")
	}
	// A key the user saves wins and the found one is forgotten.
	if _, err := a.SetAIKey("proxy", "typed", false); err != nil {
		t.Fatal(err)
	}
	if p := a.AIProviders()[0]; p.KeySource != KeyInMemory || p.KeyFoundIn != "" {
		t.Fatalf("after saving %+v", p)
	}
	// A placeholder or empty key is not taken.
	b := appWith(t, &toolKeyAI{fakeAI: &fakeAI{}, id: "proxy", key: ""})
	if b.AIProviders()[0].Configured {
		t.Fatal("no key found, yet configured")
	}
}

func TestStartProviderIsTheFirstThatCanSend(t *testing.T) {
	notReady := domain.NewUserError("AI_UNAVAILABLE", "없음")
	a := appWith(t, &fakeAI{}, &readyAI{fakeAI: &fakeAI{}, id: "cli-off", ready: notReady},
		&readyAI{fakeAI: &fakeAI{}, id: "cli-on"}, &toolKeyAI{fakeAI: &fakeAI{}, id: "proxy", key: "k"})
	s := a.Settings()
	if s.AIProvider != "cli-on" || !s.AIProviderAuto {
		t.Fatalf("start provider %q auto %v", s.AIProvider, s.AIProviderAuto)
	}
	// A saved choice wins and is not called automatic.
	if _, err := a.SaveSettings(Settings{AIProvider: "proxy"}); err != nil {
		t.Fatal(err)
	}
	if s := a.Settings(); s.AIProvider != "proxy" || s.AIProviderAuto {
		t.Fatalf("saved %q auto %v", s.AIProvider, s.AIProviderAuto)
	}
	// Nothing can send: no automatic choice (the screens show the first provider).
	b := appWith(t, &fakeAI{}, &readyAI{fakeAI: &fakeAI{}, id: "cli-off", ready: notReady})
	if s := b.Settings(); s.AIProvider != "" || s.AIProviderAuto {
		t.Fatalf("none ready %q %v", s.AIProvider, s.AIProviderAuto)
	}
}

func TestServiceControlPassesThrough(t *testing.T) {
	ctx := context.Background()
	a := appWith(t, &fakeAI{}, &serviceAI{fakeAI: &fakeAI{}})
	if p := a.AIProviders(); p[0].HasService || !p[1].HasService {
		t.Fatalf("has service %+v", p)
	}
	if s, err := a.AIServiceStatus(ctx, "svc"); err != nil || s.Running || !s.Installed || s.Program != "/bin/svc" {
		t.Fatalf("status %+v %v", s, err)
	}
	if s, err := a.StartAIService(ctx, "svc"); err != nil || !s.Running || !s.Managed {
		t.Fatalf("start %+v %v", s, err)
	}
	if s, err := a.StopAIService(ctx, "svc"); err != nil || s.Running {
		t.Fatalf("stop %+v %v", s, err)
	}
	if _, err := a.AIServiceStatus(ctx, "fakeai"); !isCode(err, "INPUT") {
		t.Fatalf("no service: %v", err)
	}
	if _, err := a.StartAIService(ctx, "nope"); !isCode(err, "INPUT") {
		t.Fatalf("unknown provider: %v", err)
	}
}
