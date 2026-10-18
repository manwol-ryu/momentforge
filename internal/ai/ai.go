// Package ai prepares what MomentForge sends to an AI provider and checks
// what comes back. Providers live in subpackages (anthropic, openai) and are
// registered by cmd/momentforge. Nothing here sends anything: the app shows
// a Plan to the user first and runs it only when confirmed.
package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Provider sends one prepared chunk to an AI service.
type Provider interface {
	ID() string           // "anthropic", "openai"
	Name() string         // shown to users
	KeyEnv() string       // environment variable read at start for the key ("" none)
	DefaultModel() string // model ID filled in for the user ("" when the user must choose)
	MaxOutputTokens() int // answer limit of one request, thinking included
	// Review sends chunk with Prompt and Schema and returns the model's JSON
	// text (validated by Validate) and token usage. Errors are ProviderErrors
	// or UserErrors; a send whose outcome is unknown must not be retried.
	Review(ctx context.Context, key, model string, chunk Chunk) (json.RawMessage, Usage, error)
}

// Usage is what one request cost in tokens (0 when unknown). Input tokens
// are split by price: uncached, read from the prompt cache, written to it.
type Usage struct {
	InputTokens      int `json:"input_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_input_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_creation_input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
	// CostUSD is the cost the provider itself reported (0 when it reports
	// none); the app prefers its own price table when it has one.
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// Add returns the sum of two usages.
func (u Usage) Add(v Usage) Usage {
	return Usage{InputTokens: u.InputTokens + v.InputTokens, CacheReadTokens: u.CacheReadTokens + v.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + v.CacheWriteTokens, OutputTokens: u.OutputTokens + v.OutputTokens,
		TotalTokens: u.TotalTokens + v.TotalTokens, CostUSD: u.CostUSD + v.CostUSD}
}

// AllInput is every input token, cached or not.
func (u Usage) AllInput() int { return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens }

// Optional provider features. The app checks for them with type assertions.
type (
	// Priced providers know their models' list prices.
	Priced interface {
		Price(model string) (Price, bool)
	}
	// TokenCounter counts a chunk's input tokens with the provider's own
	// counter. It sends the chunk, so it runs only when the user asks.
	TokenCounter interface {
		CountTokens(ctx context.Context, key, model string, chunk Chunk) (int, error)
	}
	// ModelLister lists the models the key can use.
	ModelLister interface {
		Models(ctx context.Context, key string) ([]Model, error)
	}
	// ModelChecker rejects a model ID the provider can never send to, so the
	// preview says why before anything is sent.
	ModelChecker interface {
		CheckModel(model string) error
	}
	// Endpointed providers send to an address the user sets: a proxy they
	// run (CLIProxyAPI, OpenCodex). The address is saved with the settings.
	Endpointed interface {
		DefaultEndpoint() string
		Endpoint() string
		SetEndpoint(url string) error // "" goes back to the default
		EndpointHelp() string         // what the address is, shown under it in 설정
	}
	// KeyFinder providers can find their key in their own program's
	// configuration (CLIProxyAPI's access.api-keys). The app uses it, in
	// memory only, when no key was saved or given.
	KeyFinder interface {
		FindKey() (key, where string) // "" when there is none
	}
	// Service providers send to a local program that MomentForge can start
	// and stop for the user (CLIProxyAPI, OpenCodex).
	Service interface {
		ServiceStatus(ctx context.Context) ServiceStatus
		StartService(ctx context.Context) error
		StopService(ctx context.Context) error
	}
	// SelfAuthenticated providers sign in with another program's own login
	// (the OpenCode CLI); MomentForge holds no key for them and passes "".
	SelfAuthenticated interface {
		Ready() error     // nil when the program is there to use
		AuthHelp() string // how the user signs in, shown on screen
	}
	// BatchProvider sends many chunks as one discounted batch whose results
	// arrive later, matched by request ID (in any order).
	BatchProvider interface {
		SubmitBatch(ctx context.Context, key, model string, reqs []BatchRequest) (batchID string, err error)
		BatchStatus(ctx context.Context, key, batchID string) (BatchStatus, error)
		BatchResults(ctx context.Context, key, batchID string) ([]BatchResult, error)
		CancelBatch(ctx context.Context, key, batchID string) error
	}
)

// ServiceStatus is how a provider's local program stands.
type ServiceStatus struct {
	Installed bool   // the program was found
	Running   bool   // something answers at the provider's address
	Managed   bool   // the running program is one MomentForge can stop (it started it, or the pid file names it)
	Program   string // where the program is
	Config    string // the configuration file it runs with
	Log       string // where its output goes when MomentForge starts it
	Note      string // something the user should know (it listens on every network interface, ...)
	// The program's own dashboard, when it runs and has one turned on.
	Dashboard    string // its address
	DashboardOff bool   // it runs, but its dashboard is turned off in its config
	// How to sign in to the dashboard, or (DashboardOff) how to turn it on.
	DashboardHelp string
}

// Model is a model a key can use.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// BatchRequest is one chunk of a batch; ID is unique within the batch.
type BatchRequest struct {
	ID    string
	Chunk Chunk
}

// BatchStatus is a submitted batch's progress.
type BatchStatus struct {
	Ended      bool
	Processing int
	Succeeded  int
	Errored    int
	Canceled   int
	Expired    int
}

// BatchResult is one request's outcome; Err is set when it did not succeed.
type BatchResult struct {
	ID     string
	Output json.RawMessage
	Usage  Usage
	Err    error
}

// ProviderError is a failed request; Retryable ones (rate limit, server
// error) may be sent again after RetryAfter.
type ProviderError struct {
	*domain.UserError
	Retryable  bool
	RetryAfter time.Duration
}

func (e *ProviderError) Unwrap() error { return e.UserError }

// Registry holds the registered providers.
type Registry struct{ list []Provider }

// Register adds providers; IDs must be unique.
func (r *Registry) Register(ps ...Provider) {
	for _, p := range ps {
		if _, dup := r.Get(p.ID()); dup {
			panic(fmt.Sprintf("AI provider %q registered twice", p.ID()))
		}
		r.list = append(r.list, p)
	}
}

// Get returns the provider with id.
func (r *Registry) Get(id string) (Provider, bool) {
	for _, p := range r.list {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// All returns providers in registration order.
func (r *Registry) All() []Provider { return append([]Provider(nil), r.list...) }

// Candidate is a moment the user selected for review.
type Candidate struct {
	ID         string         `json:"id"`
	SourceType string         `json:"source_type"` // rule | viewer_comment
	AnchorMs   int64          `json:"anchor_ms"`
	Title      string         `json:"title"`
	Statistics map[string]any `json:"statistics"` // the rule candidate's peak numbers; null otherwise
}

// Evidence is one thing the model may cite: a chat message or a comment.
type Evidence struct {
	Ref     string  `json:"ref"` // chat:<event id> | comment:<recommendation id>
	TimeMs  int64   `json:"time_ms"`
	Author  *string `json:"author,omitempty"` // pseudonym; absent for comments
	Content string  `json:"content"`
}

// Chunk is one request's input.
type Chunk struct {
	StartMs    int64       `json:"start_ms"`
	EndMs      int64       `json:"end_ms"`
	Candidates []Candidate `json:"candidates"`
	Evidence   []Evidence  `json:"evidence"`
}

// Omitted is a part of the selection that will not be sent, and why.
type Omitted struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Reason  string `json:"reason"` // comment_input_too_large | message_too_large | input_budget | request_budget | cost_cap
}

// Range is a span of the video.
type Range struct {
	StartMs int64 `json:"start_ms"`
	EndMs   int64 `json:"end_ms"`
}

// Limits of one plan (CzzEdit's, except the larger batch budget).
const (
	maxChunkBytes   = 10000
	maxBaseBytes    = 9500
	maxMessageBytes = 9000
	stepMs          = 600000
	overlapMs       = 30000
	framingTokens   = 500 // message framing and the provider's schema text, beyond the bytes sent
	// MaxRequests is the request budget of a plan sent one by one (CzzEdit's);
	// MaxBatchRequests that of a batch, which the cost cap bounds instead.
	MaxRequests      = 20
	MaxBatchRequests = 100
)

// Deliveries: one request after another, or one discounted batch.
const (
	DeliverySingle = "single"
	DeliveryBatch  = "batch"
)

// requestOverhead is the input beyond the chunk in every request, in the
// byte-based estimate: the instruction and the schema.
var requestOverhead = len(Prompt) + len(Encode(Schema())) + framingTokens

// Plan is everything a run will send. The user sees it before anything is sent.
type Plan struct {
	SessionID            string    `json:"session_id"`
	Kind                 string    `json:"kind,omitempty"` // "" or explain: chunk review; cut: the cut analysis (internal/ai/cut)
	Provider             string    `json:"provider"`
	Model                string    `json:"model"`
	Delivery             string    `json:"delivery"` // single | batch
	PromptVersion        int       `json:"prompt_version"`
	Prompt               string    `json:"prompt"`
	Ranges               []Range   `json:"ranges"`
	Chunks               []Chunk   `json:"chunks"`
	Omitted              []Omitted `json:"omitted"`
	RequestCount         int       `json:"request_count"`
	PlannedInputEstimate int       `json:"planned_input_token_estimate"`
	InputUpperEstimate   int       `json:"input_token_upper_estimate"`
	OutputTokenLimit     int       `json:"output_token_limit"`
	AttemptLimit         int       `json:"request_attempt_limit"`
	EstimateNote         string    `json:"estimate_note"`
	Price                *Price    `json:"price,omitempty"`          // nil when the model's price is unknown
	CostUpperUSD         float64   `json:"cost_upper_usd,omitempty"` // most the plan can cost (0 without a price)
	CostCapUSD           float64   `json:"cost_cap_usd,omitempty"`   // the user's limit (0 none)
	Fingerprint          string    `json:"fingerprint"`
}

// MaxOutputTokens is the default answer limit of one request (CzzEdit's).
const MaxOutputTokens = 2000

// PlanInput is what Prepare needs; Chats returns the messages in
// [from, to) in time order (pseudonymous).
type PlanInput struct {
	SessionID       string
	Provider        string
	Model           string
	Delivery        string // single (default) | batch
	MaxOutputTokens int    // per request; MaxOutputTokens when 0
	Price           *Price // the model's price, when known
	CostCapUSD      float64
	Ranges          []Range
	Candidates      []Candidate
	Comments        map[string]Evidence // viewer candidates' comments by candidate ID
	Chats           func(from, to int64) ([]domain.ChatEvent, error)
}

// Encode is the compact JSON sent to providers (UTF-8, no HTML escaping).
func Encode(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// ChunkInputEstimate is a request's input in the byte-based estimate. A
// token covers at least one byte, so it is never below the real count.
func ChunkInputEstimate(c Chunk) int { return len(Encode(c)) + requestOverhead }

// Prepare splits the selected ranges into ≤ 10 KB requests of 10-minute
// windows (30 s overlap), at most 20 (CzzEdit ai.prepare), or 100 for a
// batch. With a cost cap, requests that would pass it are left out.
func Prepare(in PlanInput) (Plan, error) {
	delivery, budget := DeliverySingle, MaxRequests
	if in.Delivery == DeliveryBatch {
		delivery, budget = DeliveryBatch, MaxBatchRequests
	}
	outLimit := in.MaxOutputTokens
	if outLimit <= 0 {
		outLimit = MaxOutputTokens
	}
	if in.CostCapUSD < 0 || (in.CostCapUSD > 0 && in.Price == nil) {
		return Plan{}, domain.NewUserError("INPUT", "가격을 알 수 없는 모델에는 비용 한도를 적용할 수 없습니다.")
	}
	p := Plan{SessionID: in.SessionID, Provider: in.Provider, Model: in.Model, Delivery: delivery, PromptVersion: PromptVersion,
		Prompt: Prompt, Chunks: []Chunk{}, Omitted: []Omitted{}, AttemptLimit: budget, Price: in.Price, CostCapUSD: in.CostCapUSD,
		EstimateNote: "입력 토큰은 UTF-8 바이트 수로 잡은 상한이며 실제 토큰 수는 보통 더 적습니다. " +
			"비용은 입력 전체를 캐시 쓰기 가격으로, 출력은 상한까지 쓴다고 본 최대값입니다."}
	ranges := append([]Range(nil), in.Ranges...)
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].StartMs < ranges[j].StartMs })
	for _, r := range ranges {
		if n := len(p.Ranges); n > 0 && r.StartMs <= p.Ranges[n-1].EndMs {
			p.Ranges[n-1].EndMs = max(p.Ranges[n-1].EndMs, r.EndMs)
		} else {
			p.Ranges = append(p.Ranges, r)
		}
	}
	var chunks []Chunk
	for _, r := range p.Ranges {
		for t := r.StartMs; t < r.EndMs; t += stepMs {
			lo, hi := max(r.StartMs, t-overlapMs), min(r.EndMs, t+stepMs+overlapMs)
			// A candidate belongs to the one window whose own step (not the
			// overlap) holds its anchor, so each candidate is explained once.
			coreLo, coreHi := t, t+stepMs
			if t == r.StartMs {
				coreLo = lo
			}
			if t+stepMs >= r.EndMs {
				coreHi = hi
			}
			var cands []Candidate
			comments := map[string]Evidence{}
			base := Chunk{StartMs: lo, EndMs: hi, Candidates: []Candidate{}, Evidence: []Evidence{}}
			for _, c := range in.Candidates {
				if coreLo <= c.AnchorMs && c.AnchorMs < coreHi {
					cands = append(cands, c)
					base.Candidates = append(base.Candidates, c)
					if ev, ok := in.Comments[c.ID]; ok {
						comments[c.ID] = ev
						base.Evidence = append(base.Evidence, ev)
					}
				}
			}
			// The size of every candidate is counted in each piece, so a piece never grows past the limit.
			baseBytes := len(Encode(base))
			if baseBytes > maxBaseBytes {
				p.Omitted = append(p.Omitted, Omitted{lo, hi, "comment_input_too_large"})
				continue
			}
			chats, err := in.Chats(lo, hi)
			if err != nil {
				return p, err
			}
			// The window's chat, cut into pieces of at most maxChunkBytes.
			empty := Chunk{StartMs: lo, EndMs: hi, Candidates: []Candidate{}, Evidence: []Evidence{}}
			pieces := []Chunk{cloneChunk(empty)}
			size := baseBytes
			for _, c := range chats {
				author := c.AuthorKey
				ev := Evidence{Ref: "chat:" + c.ID, TimeMs: c.OffsetMs, Content: c.Text}
				if author != "" {
					ev.Author = &author
				}
				evBytes := len(Encode(ev))
				if evBytes > maxMessageBytes {
					p.Omitted = append(p.Omitted, Omitted{c.OffsetMs, c.OffsetMs + 1, "message_too_large"})
					continue
				}
				if size+evBytes+1 > maxChunkBytes {
					if len(pieces[len(pieces)-1].Evidence) > 0 {
						pieces = append(pieces, cloneChunk(empty))
					}
					size = baseBytes
					if size+evBytes+1 > maxChunkBytes {
						p.Omitted = append(p.Omitted, Omitted{c.OffsetMs, c.OffsetMs + 1, "input_budget"})
						continue
					}
				}
				last := &pieces[len(pieces)-1]
				last.Evidence = append(last.Evidence, ev)
				size += evBytes + 1
			}
			// Each candidate goes into the piece whose chat holds its anchor
			// (the last piece that starts at or before it), with its comment.
			for _, c := range cands {
				at := 0
				for i, pc := range pieces {
					if len(pc.Evidence) > 0 && pc.Evidence[0].TimeMs <= c.AnchorMs {
						at = i
					}
				}
				pieces[at].Candidates = append(pieces[at].Candidates, c)
				if ev, ok := comments[c.ID]; ok {
					pieces[at].Evidence = append([]Evidence{ev}, pieces[at].Evidence...)
				}
			}
			for _, pc := range pieces {
				if len(pc.Evidence) > 0 {
					chunks = append(chunks, pc)
				}
			}
		}
	}
	for _, c := range chunks[min(len(chunks), budget):] {
		p.Omitted = append(p.Omitted, Omitted{c.StartMs, c.EndMs, "request_budget"})
	}
	batch := delivery == DeliveryBatch
	for _, c := range chunks[:min(len(chunks), budget)] {
		est := ChunkInputEstimate(c)
		if in.Price != nil {
			cost := in.Price.UpperCost(est, outLimit, batch)
			if in.CostCapUSD > 0 && p.CostUpperUSD+cost > in.CostCapUSD {
				p.Omitted = append(p.Omitted, Omitted{c.StartMs, c.EndMs, "cost_cap"})
				continue
			}
			p.CostUpperUSD += cost
		}
		p.Chunks = append(p.Chunks, c)
		p.PlannedInputEstimate += est
	}
	if len(p.Chunks) == 0 {
		if in.CostCapUSD > 0 && len(chunks) > 0 {
			return p, domain.NewUserError("COST_CAP", "비용 한도 안에서 보낼 수 있는 요청이 없습니다. 한도를 늘려 주세요.")
		}
		return p, domain.NewUserError("NO_INPUT", "선택 범위에 보낼 근거 자료가 없습니다.")
	}
	p.RequestCount = len(p.Chunks)
	p.InputUpperEstimate = p.RequestCount * (maxChunkBytes + requestOverhead)
	p.OutputTokenLimit = p.RequestCount * outLimit
	p.Fingerprint = digest(p)
	return p, nil
}

func cloneChunk(c Chunk) Chunk {
	c.Candidates = slices.Clone(c.Candidates)
	c.Evidence = slices.Clone(c.Evidence)
	return c
}

func digest(v any) string {
	sum := sha256.Sum256(Encode(v))
	return hex.EncodeToString(sum[:])
}

// Digest is the fingerprint of any plan-like value (what the user confirmed).
func Digest(v any) string { return digest(v) }

// Recommendation is an accepted AI recommendation.
type Recommendation struct {
	ID                 string   `json:"id"`
	SourceCandidateIDs []string `json:"source_candidate_ids"`
	StartMs            int64    `json:"start_ms"`
	EndMs              int64    `json:"end_ms"`
	Title              string   `json:"title"`
	Rationale          string   `json:"rationale"`
	EvidenceRefs       []string `json:"evidence_refs"`
	Uncertainty        string   `json:"uncertainty"`
	Priority           string   `json:"priority"`
}

// Validate keeps the recommendations that cite only this chunk's evidence
// and candidates, inside its time span, with every field filled
// (CzzEdit ai.validate). An answer of the wrong shape is AI_FORMAT.
func Validate(raw json.RawMessage, chunk Chunk) (accepted []Recommendation, rejected int, err error) {
	var out map[string]json.RawMessage
	formatErr := domain.NewUserError("AI_FORMAT", "AI 응답 형식을 확인하지 못했습니다.")
	if json.Unmarshal(raw, &out) != nil {
		return nil, 0, formatErr
	}
	var status string
	var recs []json.RawMessage
	if json.Unmarshal(out["status"], &status) != nil || (status != "ok" && status != "insufficient_evidence") ||
		json.Unmarshal(out["recommendations"], &recs) != nil || out["recommendations"] == nil {
		return nil, 0, formatErr
	}
	evidence := map[string]int64{}
	for _, e := range chunk.Evidence {
		evidence[e.Ref] = e.TimeMs
	}
	candidates := map[string]bool{}
	for _, c := range chunk.Candidates {
		candidates[c.ID] = true
	}
	for _, r := range recs {
		if rec, ok := validRec(r, chunk, evidence, candidates); ok && status == "ok" {
			refs := slices.Clone(rec.EvidenceRefs)
			slices.Sort(refs)
			rec.ID = digest([]any{rec.StartMs, rec.EndMs, slices.Compact(refs)})[:32]
			accepted = append(accepted, rec)
		} else {
			rejected++
		}
	}
	return accepted, rejected, nil
}

func validRec(raw json.RawMessage, chunk Chunk, evidence map[string]int64, candidates map[string]bool) (Recommendation, bool) {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil || len(keys) != len(fields) {
		return Recommendation{}, false
	}
	for _, f := range fields {
		if _, ok := keys[f]; !ok {
			return Recommendation{}, false
		}
	}
	for _, f := range []string{"source_candidate_ids", "evidence_refs"} {
		if !bytes.HasPrefix(bytes.TrimSpace(keys[f]), []byte("[")) {
			return Recommendation{}, false // a list, not null
		}
	}
	var rec Recommendation
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&rec) != nil {
		return rec, false
	}
	if !(chunk.StartMs <= rec.StartMs && rec.StartMs < rec.EndMs && rec.EndMs <= chunk.EndMs) || len(rec.EvidenceRefs) == 0 {
		return rec, false
	}
	for _, ref := range rec.EvidenceRefs {
		t, ok := evidence[ref]
		if !ok || t < rec.StartMs || t >= rec.EndMs {
			return rec, false
		}
	}
	for _, id := range rec.SourceCandidateIDs {
		if !candidates[id] {
			return rec, false
		}
	}
	for _, s := range []string{rec.Title, rec.Rationale, rec.Uncertainty} {
		if n := len([]rune(s)); n == 0 || n > 2000 {
			return rec, false
		}
	}
	switch rec.Priority {
	case "high", "medium", "low":
		return rec, true
	}
	return rec, false
}
