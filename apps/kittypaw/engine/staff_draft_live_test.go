package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

func TestE2ELiveStaffDraftReproducesContextualPMRequest(t *testing.T) {
	runE2ELiveStaffDraftReproducesContextualPMRequest(t)
}

func TestE2ELiveProjectsStaffDraftReproducesContextualPMRequest(t *testing.T) {
	runE2ELiveStaffDraftReproducesContextualPMRequest(t)
}

func runE2ELiveStaffDraftReproducesContextualPMRequest(t *testing.T) {
	t.Helper()
	if os.Getenv("KITTYPAW_E2E_LIVE") != "1" && os.Getenv("KITTYPAW_LIVE_LLM") != "1" {
		t.Skip("set KITTYPAW_E2E_LIVE=1 to call the configured live LLM")
	}
	accountID := os.Getenv("KITTYPAW_E2E_ACCOUNT")
	if accountID == "" {
		accountID = os.Getenv("KITTYPAW_LIVE_STAFF_DRAFT_ACCOUNT")
	}
	if accountID == "" {
		accountID = "jinto"
	}
	cfgPath, err := core.ConfigPathForAccount(accountID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := core.LoadAccountSecrets(accountID)
	if err != nil {
		t.Fatal(err)
	}
	model, ok := cfg.RuntimeDefaultModel(secrets)
	if !ok {
		t.Fatal("no runtime default model configured")
	}
	provider, err := llm.NewProviderFromModelConfig(model)
	if err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedStaffDraftTranscript(t, st)

	sess := &AccountRuntime{
		Provider:  provider,
		Store:     st,
		Config:    cfg,
		BaseDir:   t.TempDir(),
		AccountID: accountID,
		Pipeline:  NewPipelineState(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	out, err := sess.Run(ctx, webChatEvent("우리 대화내용을 보고 pm 을 한사람 채용해주세요."), nil)
	if err != nil {
		t.Fatalf("Run request error: %v", err)
	}
	if !strings.Contains(out, "Staff 기능") {
		t.Fatalf("first response = %q, want Staff opt-in question", out)
	}

	out, err = sess.Run(ctx, webChatEvent("네네"), nil)
	if err != nil {
		t.Fatalf("Run opt-in error: %v", err)
	}
	t.Logf("live draft response:\n%s", out)
	if strings.Contains(out, "우리 대화내용") || strings.Contains(out, "한사람") {
		t.Fatalf("draft response copied request preamble: %q", out)
	}

	draft, ok, err := loadPendingStaffDraft(sess.BaseDir, accountID)
	if err != nil || !ok {
		t.Fatalf("load pending draft ok=%v err=%v, want ok true nil", ok, err)
	}
	t.Logf("live draft id=%q display=%q description=%q aliases=%v", draft.ID, draft.DisplayName, draft.Description, draft.Aliases)
	if draft.ID == "" || strings.Contains(draft.Description, "우리 대화내용") || strings.Contains(draft.DisplayName, "우리 대화내용") {
		t.Fatalf("bad draft: %+v", draft)
	}
	if _, err := os.Stat(filepath.Join(sess.BaseDir, "staff", draft.ID, "SOUL.draft.md")); err != nil {
		t.Fatalf("SOUL.draft.md missing: %v", err)
	}
}

func seedStaffDraftTranscript(t *testing.T, st *store.Store) {
	t.Helper()
	turns := []core.ConversationTurn{
		{Role: core.RoleUser, Content: "개발PM 을 한명 만들어줘.(혹은 채용해줘)"},
		{Role: core.RoleAssistant, Content: "개발 PM을 만들었어요.\n\n- ID: dev-pm\n- 역할: 요구사항 정리, 일정 관리, 우선순위 조율, 진행상황 추적, 블로커 관리, 릴리즈 체크리스트 관리"},
		{Role: core.RoleUser, Content: "개발PM 을 한명 만들어줘.(혹은 채용해줘)"},
		{Role: core.RoleAssistant, Content: "개발 PM을 준비했어요.\n\n- ID: dev-pm\n- 역할: 요구사항 정리, 일정 관리, 우선순위 조율, 진행상황 추적, 블로커 관리, 릴리즈 체크리스트 관리"},
		{Role: core.RoleUser, Content: "개발PM 을 한명 채용해주세요."},
		{Role: core.RoleAssistant, Content: "개발 PM을 채용했어요.\n\n- ID: dev-pm\n- 역할: 요구사항 정리, 일정 관리, 우선순위 조율, 진행상황 추적, 블로커 관리, 릴리즈 체크리스트 관리"},
		{Role: core.RoleUser, Content: "개발PM 을 한명 채용해주세요."},
		{Role: core.RoleAssistant, Content: "KittyPaw Staff 기능으로 새 역할을 만들까요?"},
		{Role: core.RoleUser, Content: "오.. 좋아요."},
		{Role: core.RoleAssistant, Content: "도움이 됐다니 좋아요! 또 필요하면 말씀해 주세요."},
		{Role: core.RoleUser, Content: "개발PM 을 한명 채용해주세요."},
		{Role: core.RoleAssistant, Content: "staff \"dev-pm\"는 이미 존재합니다."},
		{Role: core.RoleUser, Content: "오호.. 그래요?"},
		{Role: core.RoleAssistant, Content: "방금 확인해보니 목록에서 `dev-pm`을 찾지는 못했어요. 다시 생성해드릴까요?"},
	}
	for i := range turns {
		if err := st.AddConversationTurn(&turns[i]); err != nil {
			t.Fatalf("seed turn %d: %v", i, err)
		}
	}
}
