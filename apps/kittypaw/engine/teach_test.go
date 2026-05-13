package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

func TestStripFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fences",
			input: `const x = 1;`,
			want:  `const x = 1;`,
		},
		{
			name:  "js fenced block",
			input: "```js\nconst x = 1;\n```",
			want:  "const x = 1;",
		},
		{
			name:  "javascript fenced block",
			input: "```javascript\nconst x = 1;\n```",
			want:  "const x = 1;",
		},
		{
			name:  "bare fences no language",
			input: "```\nconst x = 1;\n```",
			want:  "const x = 1;",
		},
		{
			name:  "nested backticks in string",
			input: "```js\nconst s = \"use `backticks` here\";\n```",
			want:  "const s = \"use `backticks` here\";",
		},
		{
			name:  "whitespace around fences",
			input: "  ```js\n  const x = 1;\n  ```  ",
			want:  "  const x = 1;",
		},
		{
			name:  "multiple fenced blocks takes first",
			input: "```js\nfirst();\n```\nsome text\n```js\nsecond();\n```",
			want:  "first();",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \n  \n  ",
			want:  "",
		},
		{
			name:  "preamble before fence",
			input: "Here is the code:\n```js\nconst x = 1;\n```",
			want:  "const x = 1;",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripFences(tt.input)
			if got != tt.want {
				t.Errorf("stripFences() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // empty means "just check it's valid and non-empty"
	}{
		{
			name:  "english lowercase",
			input: "send daily weather",
			want:  "send-daily-weather",
		},
		{
			name:  "english mixed case",
			input: "Send Daily Weather Report",
			want:  "send-daily-weather-report",
		},
		{
			name:  "special characters stripped",
			input: "check $tock price!",
			want:  "check-tock-price",
		},
		{
			name:  "numbers preserved",
			input: "run task 42 daily",
			want:  "run-task-42-daily",
		},
		{
			name:  "korean only produces valid slug",
			input: "매일 아침 날씨 알려줘",
		},
		{
			name:  "mixed korean english",
			input: "매일 아침 weather 알려줘",
			want:  "weather",
		},
		{
			name:  "extra whitespace collapsed",
			input: "  send   daily   weather  ",
			want:  "send-daily-weather",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugify(tt.input)
			if got == "" {
				t.Fatal("slugify() returned empty string")
			}
			// Must pass ValidateSkillName
			if err := validateSlug(got); err != nil {
				t.Errorf("slugify() produced invalid slug %q: %v", got, err)
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("slugify() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectPermissions(t *testing.T) {
	tests := []struct {
		name string
		code string
		want []string
	}{
		{
			name: "http and telegram",
			code: `Http.get("https://api.example.com"); Telegram.send("hello");`,
			want: []string{"Http", "Telegram"},
		},
		{
			name: "no permissions",
			code: `const x = 1 + 2; console.log(x);`,
			want: nil,
		},
		{
			name: "all common globals",
			code: `Http.get(u); File.read(p); Storage.get(k); Shell.exec(c); Llm.generate(q);`,
			want: []string{"File", "Http", "Llm", "Shell", "Storage"},
		},
		{
			name: "browser global",
			code: `Browser.open("https://example.com"); Browser.click("e1");`,
			want: []string{"Browser"},
		},
		{
			name: "duplicates removed",
			code: `Http.get(a); Http.post(b); Http.get(c);`,
			want: []string{"Http"},
		},
		{
			name: "string literal containing dot pattern still detected",
			code: `const s = "Http.get is a function";`,
			want: []string{"Http"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectPermissions(tt.code)
			if !slicesEqual(got, tt.want) {
				t.Errorf("DetectPermissions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInferTrigger(t *testing.T) {
	tests := []struct {
		name     string
		desc     string
		wantType string
		wantCron string
		wantKw   string
	}{
		{
			name:     "every 10 minutes",
			desc:     "check stock price every 10m",
			wantType: "schedule",
			wantCron: "every 10m",
		},
		{
			name:     "every 2 hours",
			desc:     "send report every 2h",
			wantType: "schedule",
			wantCron: "every 2h",
		},
		{
			name:     "every day korean",
			desc:     "매일 아침 날씨 알려줘",
			wantType: "schedule",
			wantCron: "every 24h",
		},
		{
			name:     "keyword trigger english",
			desc:     "when someone says hello respond with greeting",
			wantType: "keyword",
			wantKw:   "hello",
		},
		{
			name:     "default to manual",
			desc:     "calculate fibonacci sequence",
			wantType: "manual",
		},
		{
			name:     "every week korean",
			desc:     "주마다 보고서 생성",
			wantType: "schedule",
			wantCron: "every 168h",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferTrigger(tt.desc)
			if got.Type != tt.wantType {
				t.Errorf("inferTrigger().Type = %q, want %q", got.Type, tt.wantType)
			}
			if tt.wantCron != "" && got.Cron != tt.wantCron {
				t.Errorf("inferTrigger().Cron = %q, want %q", got.Cron, tt.wantCron)
			}
			if tt.wantKw != "" && got.Keyword != tt.wantKw {
				t.Errorf("inferTrigger().Keyword = %q, want %q", got.Keyword, tt.wantKw)
			}
		})
	}
}

func TestSyntaxCheck(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		code    string
		wantOK  bool
		wantErr string // substring
	}{
		{
			name:   "valid simple expression",
			code:   `const x = 1 + 2; console.log(x);`,
			wantOK: true,
		},
		{
			name:   "valid ES2020 with arrow function",
			code:   `const fn = (x) => x * 2; fn(21);`,
			wantOK: true,
		},
		{
			name:   "valid skill call pattern",
			code:   `const data = Http.get("https://example.com"); Telegram.send(data);`,
			wantOK: true,
		},
		{
			name:    "syntax error missing paren",
			code:    `function foo( { return 1; }`,
			wantOK:  false,
			wantErr: "SyntaxError",
		},
		{
			name:    "syntax error unexpected token",
			code:    `const x = ;`,
			wantOK:  false,
			wantErr: "SyntaxError",
		},
		{
			name:    "empty code",
			code:    "",
			wantOK:  false,
			wantErr: "empty",
		},
		{
			name:    "exceeds size limit",
			code:    strings.Repeat("x", maxCodeSize+1),
			wantOK:  false,
			wantErr: "exceeds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, errMsg := SyntaxCheck(ctx, tt.code, nil)
			if ok != tt.wantOK {
				t.Errorf("syntaxCheck() ok = %v, want %v (err: %s)", ok, tt.wantOK, errMsg)
			}
			if tt.wantErr != "" && !strings.Contains(errMsg, tt.wantErr) {
				t.Errorf("syntaxCheck() error = %q, want substring %q", errMsg, tt.wantErr)
			}
		})
	}
}

func TestGenerateCode(t *testing.T) {
	t.Run("returns code from LLM", func(t *testing.T) {
		mock := &mockProvider{responses: []*llm.Response{
			{Content: `const data = Http.get("https://api.weather.com"); Telegram.send(data);`},
		}}
		code, err := generateCode(context.Background(), "send weather update", "test-chat", mock)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(code, "Http.get") {
			t.Errorf("generated code = %q, want to contain Http.get", code)
		}
	})

	t.Run("empty LLM response returns error", func(t *testing.T) {
		mock := &mockProvider{responses: []*llm.Response{
			{Content: ""},
		}}
		_, err := generateCode(context.Background(), "do something", "test-chat", mock)
		if err == nil {
			t.Fatal("expected error for empty LLM response")
		}
	})

	t.Run("LLM error propagates", func(t *testing.T) {
		mock := &mockProvider{responses: nil} // no responses → DeadlineExceeded
		_, err := generateCode(context.Background(), "do something", "test-chat", mock)
		if err == nil {
			t.Fatal("expected error when LLM fails")
		}
	})

	t.Run("system prompt references SkillRegistry globals", func(t *testing.T) {
		prompt := buildTeachPrompt()
		for _, skill := range core.SkillRegistry {
			if !strings.Contains(prompt, skill.Name) {
				t.Errorf("TEACH_PROMPT missing SkillRegistry global %q", skill.Name)
			}
		}
	})
}

func TestHandleTeach(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		mock := &mockProvider{responses: []*llm.Response{
			{Content: `const data = Http.get("https://api.weather.com"); Telegram.send(data); return "done";`},
		}}
		sess := &AccountRuntime{Provider: mock}

		result, err := HandleTeach(context.Background(), "send weather every morning", "test-chat", sess)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.SyntaxOK {
			t.Errorf("expected SyntaxOK=true, got error: %s", result.SyntaxError)
		}
		if result.SkillName == "" {
			t.Error("expected non-empty SkillName")
		}
		if !strings.Contains(result.Code, "Http.get") {
			t.Error("expected code to contain Http.get")
		}
		if !slicesEqual(result.Permissions, []string{"Http", "Telegram"}) {
			t.Errorf("Permissions = %v, want [Http, Telegram]", result.Permissions)
		}
	})

	t.Run("empty description returns error", func(t *testing.T) {
		sess := &AccountRuntime{Provider: &mockProvider{}}
		_, err := HandleTeach(context.Background(), "", "test-chat", sess)
		if err == nil {
			t.Fatal("expected error for empty description")
		}
	})

	t.Run("whitespace description returns error", func(t *testing.T) {
		sess := &AccountRuntime{Provider: &mockProvider{}}
		_, err := HandleTeach(context.Background(), "   \n  ", "test-chat", sess)
		if err == nil {
			t.Fatal("expected error for whitespace description")
		}
	})

	t.Run("LLM returns code with fences", func(t *testing.T) {
		mock := &mockProvider{responses: []*llm.Response{
			{Content: "```javascript\nreturn 42;\n```"},
		}}
		sess := &AccountRuntime{Provider: mock}

		result, err := HandleTeach(context.Background(), "return forty two", "test-chat", sess)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.SyntaxOK {
			t.Errorf("expected SyntaxOK=true, got error: %s", result.SyntaxError)
		}
		if strings.Contains(result.Code, "```") {
			t.Error("code should not contain markdown fences after stripping")
		}
	})

	t.Run("LLM returns invalid JS", func(t *testing.T) {
		mock := &mockProvider{responses: []*llm.Response{
			{Content: `function foo( { return; }`},
		}}
		sess := &AccountRuntime{Provider: mock}

		result, err := HandleTeach(context.Background(), "broken code", "test-chat", sess)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.SyntaxOK {
			t.Error("expected SyntaxOK=false for invalid JS")
		}
		if result.SyntaxError == "" {
			t.Error("expected non-empty SyntaxError")
		}
	})
}

func TestApproveSkill(t *testing.T) {
	t.Run("manual trigger saves and loads", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)

		result := &TeachResult{
			SkillName:   "test-skill",
			Code:        `return "hello";`,
			SyntaxOK:    true,
			Description: "a test skill",
			Trigger:     core.SkillTrigger{Type: "manual"},
			Permissions: []string{"Http"},
		}

		if err := ApproveSkill("", result); err != nil {
			t.Fatalf("ApproveSkill failed: %v", err)
		}

		skill, code, err := core.LoadSkill("test-skill")
		if err != nil {
			t.Fatalf("LoadSkill failed: %v", err)
		}
		if skill == nil {
			t.Fatal("LoadSkill returned nil")
		}
		if skill.Name != "test-skill" {
			t.Errorf("Name = %q, want %q", skill.Name, "test-skill")
		}
		if code != `return "hello";` {
			t.Errorf("Code = %q, want %q", code, `return "hello";`)
		}
		if skill.Trigger.Type != "manual" {
			t.Errorf("Trigger.Type = %q, want %q", skill.Trigger.Type, "manual")
		}
	})

	t.Run("schedule trigger with valid cron", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)

		result := &TeachResult{
			SkillName:   "weather-check",
			Code:        `Http.get("https://weather.com"); return "ok";`,
			SyntaxOK:    true,
			Description: "check weather every 10m",
			Trigger:     core.SkillTrigger{Type: "schedule", Cron: "every 10m"},
			Permissions: []string{"Http"},
		}

		if err := ApproveSkill("", result); err != nil {
			t.Fatalf("ApproveSkill failed: %v", err)
		}

		skill, _, err := core.LoadSkill("weather-check")
		if err != nil {
			t.Fatalf("LoadSkill failed: %v", err)
		}
		if skill.Trigger.Cron != "every 10m" {
			t.Errorf("Cron = %q, want %q", skill.Trigger.Cron, "every 10m")
		}
	})

	t.Run("schedule trigger with invalid cron returns error", func(t *testing.T) {
		result := &TeachResult{
			SkillName: "bad-schedule",
			Code:      `return 1;`,
			SyntaxOK:  true,
			Trigger:   core.SkillTrigger{Type: "schedule", Cron: "invalid"},
		}

		err := ApproveSkill("", result)
		if err == nil {
			t.Fatal("expected error for invalid cron")
		}
		if !strings.Contains(err.Error(), "invalid schedule") {
			t.Errorf("error = %q, want substring 'invalid schedule'", err.Error())
		}
	})

	t.Run("syntax not OK returns error", func(t *testing.T) {
		result := &TeachResult{
			SkillName:   "bad-syntax",
			Code:        `function foo( {`,
			SyntaxOK:    false,
			SyntaxError: "SyntaxError: unexpected token",
		}

		err := ApproveSkill("", result)
		if err == nil {
			t.Fatal("expected error for failed syntax check")
		}
	})
}

// slicesEqual compares two string slices for equality.
func slicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validateSlug delegates to the real core.ValidateSkillName.
func validateSlug(s string) error {
	return core.ValidateSkillName(s)
}
