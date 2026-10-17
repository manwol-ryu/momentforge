package app

import (
	"context"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// AIService is how a provider's local program (CLIProxyAPI) stands.
type AIService struct {
	Installed bool   // the program was found
	Running   bool   // something answers at the provider's address
	Managed   bool   // MomentForge can stop it (it started it, or the program's pid file names it)
	Program   string // where the program is
	Config    string // the configuration file it runs with
	Log       string // where its output goes when MomentForge starts it
	Note      string // something the user should know
}

func (a *App) aiService(provider string) (ai.Service, error) {
	p, ok := a.cfg.AI.Get(provider)
	if !ok {
		return nil, domain.NewUserError("INPUT", "AI 제공자를 선택해 주세요.")
	}
	s, ok := p.(ai.Service)
	if !ok {
		return nil, domain.NewUserError("INPUT", "이 AI 제공자에는 켜고 끌 프로그램이 없습니다.")
	}
	return s, nil
}

func toAIService(s ai.ServiceStatus) AIService {
	return AIService{Installed: s.Installed, Running: s.Running, Managed: s.Managed, Program: s.Program,
		Config: s.Config, Log: s.Log, Note: s.Note}
}

// AIServiceStatus says whether a provider's local program is installed and running.
func (a *App) AIServiceStatus(ctx context.Context, provider string) (AIService, error) {
	s, err := a.aiService(provider)
	if err != nil {
		return AIService{}, err
	}
	return toAIService(s.ServiceStatus(ctx)), nil
}

// StartAIService starts a provider's local program and returns how it stands.
func (a *App) StartAIService(ctx context.Context, provider string) (AIService, error) {
	s, err := a.aiService(provider)
	if err != nil {
		return AIService{}, err
	}
	if err := s.StartService(ctx); err != nil {
		return AIService{}, err
	}
	return toAIService(s.ServiceStatus(ctx)), nil
}

// StopAIService stops a provider's local program and returns how it stands.
func (a *App) StopAIService(ctx context.Context, provider string) (AIService, error) {
	s, err := a.aiService(provider)
	if err != nil {
		return AIService{}, err
	}
	if err := s.StopService(ctx); err != nil {
		return AIService{}, err
	}
	return toAIService(s.ServiceStatus(ctx)), nil
}
