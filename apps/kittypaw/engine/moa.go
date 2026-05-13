package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

const (
	moaMaxModels           = 5
	moaDefaultTimeout      = 30 * time.Second
	moaCandidateCharLimit  = 8000 // ≈ 2000 tokens, protects synthesizer context window
	moaSynthesisSystemText = "You are synthesizing responses from multiple models into a single best answer. Preserve accurate content, cite agreements, resolve disagreements with reasoning."
)

// ProviderResolver maps a named model (from config [[models]]) to a Provider.
// QueryMoA takes this as an injected dependency so tests can substitute mock
// providers without constructing a full AccountRuntime.
type ProviderResolver func(model string) llm.Provider

// MoACandidate is one model's response within a MoA run. Error is non-empty
// iff the model call failed; text+usage are zero in that case.
type MoACandidate struct {
	Model     string          `json:"model"`
	Text      string          `json:"text,omitempty"`
	Usage     *llm.TokenUsage `json:"usage,omitempty"`
	Error     string          `json:"error,omitempty"`
	LatencyMs int64           `json:"latency_ms"`
}

// MoAResult is the aggregated outcome of a MoA fan-out + optional synthesis.
// Synthesized=false means the synthesizer was skipped (≤1 candidate succeeded)
// and Text/Model reflect the sole successful candidate.
type MoAResult struct {
	Text        string          `json:"text"`
	Model       string          `json:"model"`
	Usage       *llm.TokenUsage `json:"usage,omitempty"`
	Candidates  []MoACandidate  `json:"candidates"`
	Synthesized bool            `json:"synthesized"`
}

// MoARequest bundles the inputs to QueryMoA. Models must be non-empty and
// ≤ moaMaxModels; empty SynthesizerModel defaults to the first candidate's
// provider during synthesis. Zero PerModelTimeout uses moaDefaultTimeout.
type MoARequest struct {
	Prompt           string
	Models           []string
	SynthesizerModel string
	PerModelTimeout  time.Duration
}

// QueryMoA fans out Prompt across Models in parallel, then synthesizes the
// successful responses via SynthesizerModel. Returns an error only for
// guard-rail violations (zero models, too many models, all candidates failed
// or resolver missing). Per-candidate failures are captured in
// result.Candidates[i].Error — a single slow/flaky model does not fail the
// whole call.
func QueryMoA(ctx context.Context, req MoARequest, resolver ProviderResolver, budget *SharedTokenBudget) (*MoAResult, error) {
	if len(req.Models) == 0 {
		return nil, fmt.Errorf("no [[models]] configured for MoA")
	}
	if len(req.Models) > moaMaxModels {
		return nil, fmt.Errorf("too many models: %d (max %d)", len(req.Models), moaMaxModels)
	}
	if resolver == nil {
		return nil, fmt.Errorf("provider resolver unavailable")
	}

	perModelTimeout := req.PerModelTimeout
	if perModelTimeout <= 0 {
		perModelTimeout = moaDefaultTimeout
	}

	// Wrap ctx so budget exhaustion can cut pending siblings.
	runCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()

	candidates := make([]MoACandidate, len(req.Models))
	var wg sync.WaitGroup
	for i, modelName := range req.Models {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidates[i] = runCandidate(runCtx, req.Prompt, modelName, resolver, budget, perModelTimeout, cancelAll)
		}()
	}
	wg.Wait()

	// Count successes.
	var firstSuccess *MoACandidate
	successCount := 0
	for i := range candidates {
		if candidates[i].Error == "" {
			successCount++
			if firstSuccess == nil {
				firstSuccess = &candidates[i]
			}
		}
	}

	if successCount == 0 {
		return nil, fmt.Errorf("all %d candidate models failed", len(candidates))
	}

	// 1 candidate — synthesis would just paraphrase a single answer; skip to
	// save tokens and preserve the original model's phrasing.
	if successCount == 1 {
		slog.Info("moa: single-success, synthesis skipped", "moa_model", firstSuccess.Model)
		return unsynthesizedResult(firstSuccess, candidates), nil
	}

	// Synthesize via resolver(SynthesizerModel). Fallback: first successful
	// candidate's provider (keeps the call alive when config is misconfigured).
	synthName := req.SynthesizerModel
	if synthName == "" {
		synthName = firstSuccess.Model
	}
	synthProvider := resolver(synthName)
	if synthProvider == nil {
		slog.Warn("moa: synthesizer model not resolvable, falling back",
			"moa_model", synthName, "fallback", firstSuccess.Model)
		synthName = firstSuccess.Model
		synthProvider = resolver(synthName)
	}
	if synthProvider == nil {
		// Final fallback: candidates are already valuable content, don't error.
		slog.Warn("moa: no resolvable synthesizer, returning first success unsynthesized")
		return unsynthesizedResult(firstSuccess, candidates), nil
	}

	synthMsg := buildSynthesisMessages(req.Prompt, candidates)
	synthResp, err := synthProvider.Generate(WithLLMCallKind(ctx, "moa.synthesis"), synthMsg)
	if err != nil {
		slog.Warn("moa: synthesis failed, returning first success", "moa_model", synthName, "error", err)
		return unsynthesizedResult(firstSuccess, candidates), nil
	}
	if budget != nil {
		if !budget.TrySpendFromUsage(synthResp.Usage) {
			slog.Warn("moa: synthesis usage exceeds budget (post-hoc)", "moa_model", synthName)
		}
	}

	return &MoAResult{
		Text:        synthResp.Content,
		Model:       synthName,
		Usage:       synthResp.Usage,
		Candidates:  candidates,
		Synthesized: true,
	}, nil
}

// unsynthesizedResult projects the lone successful candidate into a MoAResult
// with Synthesized=false. Used by the three QueryMoA paths that skip/fail
// synthesis but still want to surface the candidate's text as the final answer.
func unsynthesizedResult(firstSuccess *MoACandidate, candidates []MoACandidate) *MoAResult {
	return &MoAResult{
		Text:        firstSuccess.Text,
		Model:       firstSuccess.Model,
		Usage:       firstSuccess.Usage,
		Candidates:  candidates,
		Synthesized: false,
	}
}

// runCandidate wraps a single per-model call with its own timeout + budget
// accounting + latency capture. Never panics; never returns an error — all
// failure modes land in MoACandidate.Error.
func runCandidate(ctx context.Context, prompt, model string, resolver ProviderResolver, budget *SharedTokenBudget, timeout time.Duration, cancelAll context.CancelFunc) MoACandidate {
	start := time.Now()
	cand := MoACandidate{Model: model}

	provider := resolver(model)
	if provider == nil {
		cand.Error = fmt.Sprintf("provider not found for model %q", model)
		cand.LatencyMs = time.Since(start).Milliseconds()
		return cand
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msgs := []core.LlmMessage{{Role: core.RoleUser, Content: prompt}}
	resp, err := provider.Generate(WithLLMCallKind(callCtx, "moa.candidate"), msgs)
	cand.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		cand.Error = err.Error()
		slog.Warn("moa: candidate failed",
			"moa_model", model, "latency_ms", cand.LatencyMs, "err", err)
		return cand
	}

	cand.Text = resp.Content
	cand.Usage = resp.Usage

	// Budget accounting — if exhausted, cancel siblings but keep our own
	// result (the API call already happened).
	if budget != nil {
		if !budget.TrySpendFromUsage(resp.Usage) {
			slog.Warn("moa: budget exhausted, canceling remaining candidates",
				"moa_model", model)
			cancelAll()
		}
	}

	var inputTok, outputTok int64
	if resp.Usage != nil {
		inputTok = resp.Usage.InputTokens
		outputTok = resp.Usage.OutputTokens
	}
	slog.Info("moa: candidate ok",
		"moa_model", model,
		"latency_ms", cand.LatencyMs,
		"input_tokens", inputTok,
		"output_tokens", outputTok)
	return cand
}

// buildSynthesisMessages constructs the synthesizer prompt with per-candidate
// text truncated to moaCandidateCharLimit to protect the synthesizer's
// context window. Failed candidates are excluded.
func buildSynthesisMessages(prompt string, candidates []MoACandidate) []core.LlmMessage {
	body := "Question:\n" + prompt + "\n\n"
	for _, c := range candidates {
		if c.Error != "" {
			continue
		}
		body += fmt.Sprintf("Response from %s:\n%s\n\n", c.Model, moaTruncate(c.Text, moaCandidateCharLimit))
	}
	body += "Produce a single best answer synthesizing the above."

	return []core.LlmMessage{
		{Role: core.RoleSystem, Content: moaSynthesisSystemText},
		{Role: core.RoleUser, Content: body},
	}
}

func moaTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]"
}

// executeMoA is the JS sandbox adapter. Moa.query(prompt, options?) maps to
// QueryMoA with resolver=s.resolveProvider and budget=s.Budget. Models default
// to s.Config.Models (all named models) when options.models is omitted;
// synthesizer defaults to the [[models]] entry with Default=true.
func executeMoA(ctx context.Context, call core.SkillCall, s *AccountRuntime) (string, error) {
	if call.Method != "query" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Moa method: %s", call.Method)})
	}
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "prompt required"})
	}
	var prompt string
	if err := json.Unmarshal(call.Args[0], &prompt); err != nil {
		return jsonResult(map[string]any{"error": "invalid prompt argument"})
	}

	var opts struct {
		Models            []string `json:"models"`
		Synthesizer       string   `json:"synthesizer"`
		PerModelTimeoutMs int64    `json:"per_model_timeout_ms"`
	}
	if len(call.Args) > 1 {
		// Invalid options JSON → silently degrade to defaults; strict rejection
		// would surprise JS callers building options dynamically.
		_ = json.Unmarshal(call.Args[1], &opts)
	}

	req := MoARequest{
		Prompt:           prompt,
		Models:           opts.Models,
		SynthesizerModel: opts.Synthesizer,
	}
	// Clamp per-model timeout to prevent int64*time.Millisecond overflow from a
	// hostile/misconfigured JS caller. 10 minutes is already absurd for a
	// single LLM call; anything beyond that is a bug.
	if opts.PerModelTimeoutMs > 0 {
		ms := opts.PerModelTimeoutMs
		if ms > 600_000 {
			ms = 600_000
		}
		req.PerModelTimeout = time.Duration(ms) * time.Millisecond
	}

	// Default model list from config.
	if len(req.Models) == 0 && s.Config != nil {
		for _, m := range s.Config.Models {
			if m.Name != "" {
				req.Models = append(req.Models, m.Name)
			}
		}
	}
	// Default synthesizer = config model with Default=true.
	if req.SynthesizerModel == "" && s.Config != nil {
		for _, m := range s.Config.Models {
			if m.Default {
				req.SynthesizerModel = m.Name
				break
			}
		}
	}

	// Single-model config is handled inside QueryMoA via the successCount==1
	// skip-synthesis branch — no executor-level short-circuit needed. The
	// "synthesis skipped" event is logged once there, not twice from both sides.

	result, err := QueryMoA(ctx, req, s.resolveProvider, s.Budget)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{
		"text":        result.Text,
		"model":       result.Model,
		"usage":       result.Usage,
		"candidates":  result.Candidates,
		"synthesized": result.Synthesized,
	})
}
