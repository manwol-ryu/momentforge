package cut

// JSON schemas of the step answers (strict: every property required, no
// extras). Helpers keep them short.

func str() map[string]any             { return map[string]any{"type": "string"} }
func boolean() map[string]any         { return map[string]any{"type": "boolean"} }
func integer() map[string]any         { return map[string]any{"type": "integer"} }
func strs() map[string]any            { return map[string]any{"type": "array", "items": str()} }
func enum(v ...string) map[string]any { return map[string]any{"type": "string", "enum": v} }
func arr(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

// object is a strict object: every listed property is required.
func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	sortStrings(required)
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func causeSchema() map[string]any {
	return object(map[string]any{
		"kind":      enum(CauseBroadcastEvent, CauseCallback, CauseViewerJoke, CauseDonation, CauseUnknown),
		"summary":   str(),
		"support":   strs(),
		"against":   strs(),
		"direction": enum("broadcast_to_viewers", "viewer_to_broadcaster_to_viewers", "unknown"),
	})
}

func searchSchema() map[string]any {
	return object(map[string]any{
		"tool":      enum("search", "read_around", "read_range", "events", "activity"),
		"query":     str(),
		"in":        enum("utterances", "chats", "both"),
		"before_ms": integer(),
		"at_ms":     integer(),
		"from_ms":   integer(),
		"to_ms":     integer(),
		"window_s":  integer(),
	})
}

// AnalyzeSchema is the analyze step's answer.
func AnalyzeSchema() map[string]any {
	return object(map[string]any{
		"target":          str(),
		"causes":          arr(causeSchema()),
		"cause_status":    enum("confirmed", "likely", "undetermined"),
		"questions":       strs(),
		"search_requests": arr(searchSchema()),
	})
}

func spanSchema() map[string]any {
	return object(map[string]any{"start_ref": str(), "end_ref": str(), "role": enum("setup", "main", "payoff")})
}

// PlanSchema is the plan step's answer.
func PlanSchema() map[string]any {
	return object(map[string]any{
		"story_origin_ref":       str(),
		"origin_confidence":      enum("found_in_search_range", "not_found"),
		"event_start_ref":        str(),
		"analysis_evidence_refs": strs(),
		"required_context_refs":  strs(),
		"optional_context_refs":  strs(),
		"boundary_candidates":    arr(object(map[string]any{"name": enum("short", "context", "full_event"), "spans": arr(spanSchema())})),
		"chosen":                 enum("short", "context", "full_event"),
		"start_reason":           str(),
		"end_reason":             str(),
		"ending": object(map[string]any{
			"state":      enum("confirmed", "pending", "cut_off", "insufficient", "unresolved"),
			"event_kind": enum("question_answer", "success_failure", "joke_mistake", "claim_argument", "promise_penalty", "other"),
			"checks":     strs(),
		}),
		"unresolved_questions": strs(),
	})
}

// VerifyClipSchema is the clip-only check's answer.
func VerifyClipSchema() map[string]any {
	return object(map[string]any{
		"pass": boolean(),
		"answers": object(map[string]any{
			"what": str(), "condition": str(), "result": str(), "references": str(), "why_reaction": str(),
		}),
		"gaps":   strs(),
		"asides": strs(),
	})
}

// VerifySourceSchema is the against-source check's answer.
func VerifySourceSchema() map[string]any {
	return object(map[string]any{
		"pass":              boolean(),
		"mixed_attempt":     boolean(),
		"cut_condition":     boolean(),
		"missed_correction": boolean(),
		"ended_early":       boolean(),
		"notes":             strs(),
	})
}
