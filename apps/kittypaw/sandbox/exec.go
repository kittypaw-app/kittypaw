package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/jinto/kittypaw/core"
)

// execOpts controls per-execution behavior.
type execOpts struct {
	// rawResolverResults makes skill stubs return raw JSON strings instead of
	// auto-parsed objects. Package code expects raw strings (it calls JSON.parse
	// itself), while LLM-generated code expects parsed objects.
	rawResolverResults bool

	// exposeFanout registers the Fanout global. Off by default: only team-space
	// accounts (Session.Fanout != nil) may push to peers, and we want personal
	// accounts to see `typeof Fanout === "undefined"` at the JS layer — not a
	// bound object that happens to error on call. Defense in depth against a
	// skill that probes the API surface.
	exposeFanout bool

	// exposeShare registers the Share global. Off by default: only personal
	// accounts read from team space (team space is the authoritative source and
	// has no peer to read from), so team-space sessions must see `typeof Share ===
	// "undefined"`. Mirror of exposeFanout — same defense-in-depth intent.
	exposeShare bool
}

// run executes JS code in an in-process goja VM.
// Skill calls are resolved synchronously: the JS stub calls a Go function
// that invokes the resolver and returns the result directly.
func run(ctx context.Context, cfg core.SandboxConfig, code string, jsContext map[string]any, resolver SkillResolver, opts execOpts) (*core.ExecutionResult, error) {
	if jsContext == nil {
		jsContext = map[string]any{}
	}

	vm := goja.New()

	// --- timeout ---
	timeout := time.Duration(cfg.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt("execution timed out")
		case <-done:
		}
	}()
	defer close(done)

	result := &core.ExecutionResult{Success: true}

	// observeSignal is the typed sentinel for Runner.observe() interrupts.
	// Using a struct type (not a string) prevents confusion with timeout interrupts.
	type observeSignal struct{}
	var observations []core.Observation

	// --- console.log capture ---
	var consoleLogs []string
	console := vm.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			parts[i] = arg.String()
		}
		consoleLogs = append(consoleLogs, strings.Join(parts, " "))
		return goja.Undefined()
	})
	vm.Set("console", console)

	// --- skill stubs ---
	for _, skill := range core.SkillRegistry {
		if skill.Name == "Fanout" && !opts.exposeFanout {
			// Personal accounts never even see the global; see execOpts doc.
			continue
		}
		if skill.Name == "Share" && !opts.exposeShare {
			// Team-space accounts never even see the global; see execOpts doc.
			continue
		}
		obj := vm.NewObject()
		skillName := skill.Name
		for _, method := range skill.Methods {
			methodName := method.Name
			obj.Set(methodName, func(call goja.FunctionCall) goja.Value {
				rawArgs := make([]json.RawMessage, len(call.Arguments))
				for i, arg := range call.Arguments {
					exported := arg.Export()
					b, err := json.Marshal(exported)
					if err != nil {
						rawArgs[i] = json.RawMessage("null")
					} else {
						rawArgs[i] = b
					}
				}
				sc := core.SkillCall{
					ID:        fmt.Sprintf("skill_call_%d", len(result.SkillCalls)+1),
					SkillName: skillName,
					Method:    methodName,
					Args:      rawArgs,
				}
				result.SkillCalls = append(result.SkillCalls, sc)
				trace := core.ToolTrace{
					ID:        sc.ID,
					SkillName: sc.SkillName,
					Method:    sc.Method,
					Args:      rawArgs,
				}

				if resolver != nil {
					resp, err := resolver(ctx, sc)
					if err != nil {
						trace.Error = err.Error()
						result.ToolTraces = append(result.ToolTraces, trace)
						panic(vm.NewGoError(err))
					}
					trace.Success = true
					trace.Result = rawToolResult(resp)
					result.ToolTraces = append(result.ToolTraces, trace)
					if resp != "" {
						if opts.rawResolverResults {
							// Package mode: return raw JSON string so
							// package code can JSON.parse() it directly.
							return vm.ToValue(resp)
						}
						var parsed any
						if json.Unmarshal([]byte(resp), &parsed) == nil {
							return vm.ToValue(parsed)
						}
					}
				} else {
					trace.Success = true
					trace.Result = json.RawMessage("null")
					result.ToolTraces = append(result.ToolTraces, trace)
				}
				return goja.Null()
			})
		}
		vm.Set(skillName, obj)
	}

	// --- Runner.observe (VM control flow, not a skill call) ---
	// Registered after SkillRegistry loop so it's added to the existing Runner object.
	if runnerVal := vm.Get("Runner"); runnerVal != nil && runnerVal != goja.Undefined() {
		runnerObj := runnerVal.ToObject(vm)
		runnerObj.Set("observe", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.ToValue("Runner.observe requires an argument"))
			}
			obs := core.Observation{}
			exported := call.Arguments[0].Export()
			switch v := exported.(type) {
			case map[string]any:
				if d, ok := v["data"].(string); ok {
					obs.Data = d
				} else {
					// Marshal non-string data
					if b, err := json.Marshal(v["data"]); err == nil {
						obs.Data = string(b)
					}
				}
				if l, ok := v["label"].(string); ok {
					obs.Label = l
				}
			default:
				obs.Data = fmt.Sprintf("%v", exported)
			}
			// Truncate to 5000 runes
			const maxObsLen = 5000
			if runes := []rune(obs.Data); len(runes) > maxObsLen {
				obs.Data = string(runes[:maxObsLen])
			}
			observations = append(observations, obs)
			vm.Interrupt(observeSignal{})
			return goja.Undefined()
		})
	}

	// --- inject context ---
	vm.Set("context", jsContext)

	// --- execute ---
	wrapped := autoReturn(code)
	script := fmt.Sprintf("(function(){\n%s\n})()", wrapped)

	val, err := vm.RunString(script)

	if err != nil {
		// Check for Runner.observe() interrupt (typed sentinel).
		if ie, ok := err.(*goja.InterruptedError); ok {
			if _, isObserve := ie.Value().(observeSignal); isObserve {
				result.Observe = true
				result.Observations = observations
				if len(consoleLogs) > 0 {
					result.Output = strings.Join(consoleLogs, "\n")
				}
				return result, nil
			}
			// Other interrupts (timeout)
			result.Success = false
			result.Error = "execution timed out"
			if len(consoleLogs) > 0 {
				result.Output = strings.Join(consoleLogs, "\n")
			}
			return result, nil
		}
		// JS exceptions
		if ex, ok := err.(*goja.Exception); ok {
			result.Success = false
			result.Error = ex.Value().String()
		} else if ctx.Err() != nil {
			result.Success = false
			result.Error = "execution timed out"
		} else {
			result.Success = false
			result.Error = err.Error()
		}
		if len(consoleLogs) > 0 {
			result.Output = strings.Join(consoleLogs, "\n")
		}
		return result, nil
	}

	// --- build output ---
	var jsonResult string
	if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
		exported := val.Export()
		switch v := exported.(type) {
		case string:
			jsonResult = v
		default:
			b, marshalErr := json.Marshal(exported)
			if marshalErr == nil {
				jsonResult = string(b)
			}
		}
	}

	if len(consoleLogs) > 0 && jsonResult != "" {
		result.Output = strings.Join(consoleLogs, "\n") + "\n" + jsonResult
	} else if len(consoleLogs) > 0 {
		result.Output = strings.Join(consoleLogs, "\n")
	} else {
		result.Output = jsonResult
	}

	return result, nil
}

func rawToolResult(resp string) json.RawMessage {
	if resp == "" {
		return json.RawMessage("null")
	}
	if json.Valid([]byte(resp)) {
		return json.RawMessage(resp)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}
