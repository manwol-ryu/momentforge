package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// settingsCore records what the settings and key handlers pass on.
type settingsCore struct {
	fakeCore
	saved   *app.Settings
	key     *[3]string // provider, key, persist
	saveErr error
}

func (c settingsCore) Settings() app.SettingsInfo {
	return app.SettingsInfo{Settings: app.Settings{IncludeComments: true, ViewerAdaptive: true},
		File: "/data/settings.env", KeyFile: "/data/.env"}
}

func (c settingsCore) SaveSettings(s app.Settings) (app.SettingsInfo, error) {
	if c.saveErr != nil {
		return app.SettingsInfo{}, c.saveErr
	}
	*c.saved = s
	return app.SettingsInfo{Settings: s, File: "/data/settings.env", KeyFile: "/data/.env"}, nil
}

func (c settingsCore) SetAIKey(provider, key string, persist bool) (string, error) {
	p := "false"
	if persist {
		p = "true"
	}
	*c.key = [3]string{provider, key, p}
	switch {
	case key == "":
		return "", nil
	case persist:
		return app.KeyFromFile, nil
	}
	return app.KeyInMemory, nil
}

func (c settingsCore) AIProviders() []app.AIProvider {
	return []app.AIProvider{{ID: "anthropic", Name: "Claude", Configured: true, DefaultModel: "m",
		KeySource: app.KeyFromFile, KeyName: "ANTHROPIC_API_KEY"}}
}

func newSettingsCore() settingsCore {
	return settingsCore{saved: &app.Settings{}, key: &[3]string{}}
}

func TestSettingsGetAndSave(t *testing.T) {
	core := newSettingsCore()
	h := newTestHandler(core, nil, nil)
	hdr := map[string]string{KeyHeader: testKey}

	rec := request(h, "GET", "/api/settings", hdr)
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var got map[string]any
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got["aiProvider"] != "" || got["includeComments"] != true || got["viewerAdaptive"] != true || got["sensitive"] != false ||
		got["file"] != "/data/settings.env" || got["keyFile"] != "/data/.env" {
		t.Fatalf("get body: %s", rec.Body)
	}
	if models, ok := got["aiModels"].(map[string]any); !ok || len(models) != 0 {
		t.Fatalf("aiModels must be an object: %s", rec.Body)
	}

	// Every default must be given: a missing one would silently turn an option off.
	if rec := requestBody(h, "POST", "/api/settings", `{"aiProvider":"","sensitive":true}`, hdr); rec.Code != 400 || errorCode(t, rec) != "INPUT" {
		t.Fatalf("partial: %d %s", rec.Code, rec.Body)
	}
	body := `{"aiProvider":"anthropic","aiModels":{"anthropic":"m2"},"sensitive":true,"includeComments":false,"viewerAdaptive":false,` +
		`"cutSearchRounds":2,"cutSearchKb":40}`
	if rec := requestBody(h, "POST", "/api/settings", body, hdr); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"aiModels":{"anthropic":"m2"}`) {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if s := *core.saved; s.AIProvider != "anthropic" || s.AIModels["anthropic"] != "m2" || !s.Sensitive || s.IncludeComments || s.ViewerAdaptive ||
		s.CutSearchRounds != 2 || s.CutSearchKB != 40 {
		t.Fatalf("passed on = %+v", s)
	}

	core.saveErr = domain.NewUserError("INPUT", "기본 AI 제공자를 목록에서 골라 주세요.")
	h = newTestHandler(core, nil, nil)
	if rec := requestBody(h, "POST", "/api/settings", body, hdr); rec.Code != 400 || errorCode(t, rec) != "INPUT" {
		t.Fatalf("rejected: %d %s", rec.Code, rec.Body)
	}
	if rec := requestBody(h, "POST", "/api/settings", body, nil); rec.Code != 401 {
		t.Fatalf("settings need the access key: %d", rec.Code)
	}
}

func TestAIKeyPersistIsPassedOnAndNeverEchoed(t *testing.T) {
	core := newSettingsCore()
	h := newTestHandler(core, nil, nil)
	hdr := map[string]string{KeyHeader: testKey}

	rec := requestBody(h, "POST", "/api/ai/key", `{"provider":"anthropic","key":"sk-secret","persist":true}`, hdr)
	if rec.Code != 200 || *core.key != [3]string{"anthropic", "sk-secret", "true"} {
		t.Fatalf("persist: %d %s %v", rec.Code, rec.Body, *core.key)
	}
	if strings.Contains(rec.Body.String(), "sk-secret") || !strings.Contains(rec.Body.String(), `"keySource":"file"`) {
		t.Fatalf("response: %s", rec.Body)
	}
	rec = requestBody(h, "POST", "/api/ai/key", `{"provider":"anthropic","key":"sk-secret"}`, hdr)
	if !strings.Contains(rec.Body.String(), `"keySource":"memory"`) || (*core.key)[2] != "false" {
		t.Fatalf("memory only: %s", rec.Body)
	}
	rec = requestBody(h, "POST", "/api/ai/key", `{"provider":"anthropic","key":""}`, hdr)
	if !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("clear: %s", rec.Body)
	}

	rec = request(h, "GET", "/api/ai", hdr)
	if !strings.Contains(rec.Body.String(), `"keySource":"file"`) || !strings.Contains(rec.Body.String(), `"keyName":"ANTHROPIC_API_KEY"`) {
		t.Fatalf("providers: %s", rec.Body)
	}
}
