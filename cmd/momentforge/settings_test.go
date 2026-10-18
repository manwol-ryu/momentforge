package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The settings screen's API on the real server: defaults go to
// settings.env, a key the user keeps goes to .env, and both are there after
// a restart. No request leaves the machine (no key is used).
func TestSettingsAndKeptKeyAcrossRestart(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	dir := t.TempDir()
	s := startTest(t, dir)
	key, _ := os.ReadFile(filepath.Join(dir, "runtime", "access-key"))

	code, body := get(t, s.URL+"/api/settings", string(key))
	if code != 200 || !strings.Contains(body, `"includeComments":true`) || !strings.Contains(body, `"aiProvider":""`) {
		t.Fatalf("defaults: %d %s", code, body)
	}
	code, body = post(t, s.URL+"/api/settings", string(key),
		`{"aiProvider":"openai","aiModels":{"openai":"my-model"},"sensitive":true,"includeComments":false,"viewerAdaptive":true,`+
			`"cutSearchRounds":3,"cutSearchKb":50}`)
	if code != 200 || !strings.Contains(body, `"cutSearchRounds":3`) || !strings.Contains(body, `"cutSearchKb":50`) {
		t.Fatalf("save: %d %s", code, body)
	}
	if code, body = post(t, s.URL+"/api/settings", string(key),
		`{"aiProvider":"nope","aiModels":{},"sensitive":true,"includeComments":false,"viewerAdaptive":true}`); code != 400 {
		t.Fatalf("unknown provider: %d %s", code, body)
	}
	if code, body = post(t, s.URL+"/api/ai/key", string(key), `{"provider":"anthropic","key":"sk-ant-test-1","persist":true}`); code != 200 ||
		strings.Contains(body, "sk-ant-test-1") {
		t.Fatalf("keep key: %d %s", code, body)
	}
	if code, body = post(t, s.URL+"/api/ai/key", string(key), `{"provider":"openai","key":"sk-oa-test-2"}`); code != 200 {
		t.Fatalf("memory key: %d %s", code, body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); !strings.Contains(string(b), "ANTHROPIC_API_KEY=sk-ant-test-1\n") ||
		strings.Contains(string(b), "sk-oa-test-2") {
		t.Fatalf(".env:\n%s", b)
	}
	shutdown(t, s)

	s = startTest(t, dir)
	defer shutdown(t, s)
	key, _ = os.ReadFile(filepath.Join(dir, "runtime", "access-key"))
	_, body = get(t, s.URL+"/api/settings", string(key))
	if !strings.Contains(body, `"aiProvider":"openai"`) || !strings.Contains(body, `"aiModels":{"openai":"my-model"}`) ||
		!strings.Contains(body, `"sensitive":true`) || !strings.Contains(body, `"includeComments":false`) {
		t.Fatalf("settings after restart: %s", body)
	}
	_, body = get(t, s.URL+"/api/ai", string(key))
	var res struct {
		Providers []struct {
			ID         string `json:"id"`
			Configured bool   `json:"configured"`
			KeySource  string `json:"keySource"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	state := map[string]string{}
	for _, p := range res.Providers {
		state[p.ID] = p.KeySource
	}
	if state["anthropic"] != "file" || state["openai"] != "" {
		t.Fatalf("keys after restart: %s", body)
	}
	if strings.Contains(body, "sk-ant-test-1") {
		t.Fatal("the provider list must not carry the key")
	}
	// Clearing on screen takes the key out of the file.
	post(t, s.URL+"/api/ai/key", string(key), `{"provider":"anthropic","key":""}`)
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatalf("the key file should be gone with its last key: %v", err)
	}
}
