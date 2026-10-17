package ai

// Price is a model's list price in US dollars per million tokens.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	BatchShare float64 `json:"batch_share"` // share of the price paid in a batch (0.5); 0 when not offered
	AsOf       string  `json:"as_of"`       // date of the price list the numbers come from
}

// Cost is what usage costs at p, in US dollars.
func (p Price) Cost(u Usage, batch bool) float64 {
	c := (float64(u.InputTokens)*p.Input + float64(u.CacheReadTokens)*p.CacheRead +
		float64(u.CacheWriteTokens)*p.CacheWrite + float64(u.OutputTokens)*p.Output) / 1e6
	if batch && p.BatchShare > 0 {
		c *= p.BatchShare
	}
	return c
}

// UpperCost is the most one request can cost: every input token at the
// dearer of the plain and cache-write prices, and the whole answer limit.
func (p Price) UpperCost(inputTokens, outputTokens int, batch bool) float64 {
	in := Usage{InputTokens: inputTokens, OutputTokens: outputTokens}
	if p.CacheWrite > p.Input {
		in = Usage{CacheWriteTokens: inputTokens, OutputTokens: outputTokens}
	}
	return p.Cost(in, batch)
}
