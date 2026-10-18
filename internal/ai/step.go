package ai

import (
	"context"
	"encoding/json"
)

// StepRequest is one request of a multi-step analysis (the cut analysis of
// internal/ai/cut): a fixed instruction that providers may cache, the JSON
// schema of the answer, and this step's input. Review's chunk is the same
// thing with the review instruction, so providers implement both with one
// sender.
type StepRequest struct {
	Name            string         // analyze | plan | verify_clip | verify_source | review (for logs and exports)
	Prompt          string         // fixed instruction; never varies between requests
	Schema          map[string]any // JSON schema of the answer (strict)
	SchemaName      string         // a name some APIs want for the schema
	Input           any            // this step's input, encoded with Encode
	MaxOutputTokens int            // answer limit of this request (MaxOutputTokens when 0)
}

// StepProvider sends one step of a multi-step analysis. The app checks for
// it with a type assertion; a provider without it cannot run a cut analysis.
type StepProvider interface {
	Step(ctx context.Context, key, model string, step StepRequest) (json.RawMessage, Usage, error)
}

// Step is the review request of one chunk: the fixed review instruction,
// its schema and the chunk.
func (c Chunk) Step() StepRequest {
	return StepRequest{Name: "review", Prompt: Prompt, Schema: Schema(), SchemaName: "edit_recommendations", Input: c,
		MaxOutputTokens: MaxOutputTokens}
}

// OutputLimit is the step's answer limit, MaxOutputTokens when unset.
func (s StepRequest) OutputLimit() int {
	if s.MaxOutputTokens > 0 {
		return s.MaxOutputTokens
	}
	return MaxOutputTokens
}

// InputJSON is the step's input as the compact JSON that is sent.
func (s StepRequest) InputJSON() []byte { return Encode(s.Input) }

// TextMessage is the user turn for services without structured output
// (proxies, CLI harnesses): the answer format in words, the schema and the
// input. The fixed instruction goes in the system turn.
func (s StepRequest) TextMessage() string {
	return "아래 입력 JSON을 지시대로 처리하세요. 답은 다음 JSON 스키마를 따르는 JSON 객체 하나로만 쓰고, " +
		"코드 블록이나 설명을 붙이지 마세요.\n\n스키마:\n" + string(Encode(s.Schema)) +
		"\n\n입력:\n" + string(s.InputJSON()) + "\n"
}

// HarnessMessage is TextMessage with the instruction first and tools ruled
// out, for CLI agents that have a system prompt of their own.
func (s StepRequest) HarnessMessage() string {
	return "이 작업에는 도구를 쓰지 않습니다. 파일·명령·웹 도구를 부르지 말고 바로 답하세요.\n\n지시:\n" + s.Prompt + "\n\n" + s.TextMessage()
}

// StepInputEstimate is a step's input in the byte-based estimate (a token
// covers at least one byte, so it is never below the real count).
func StepInputEstimate(s StepRequest) int {
	return len(s.Prompt) + len(Encode(s.Schema)) + len(s.InputJSON()) + framingTokens
}
