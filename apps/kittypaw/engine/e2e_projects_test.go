package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestE2EProjectsStaffDraftApprovalDoesNotLoseContext(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	baseDir := t.TempDir()
	project, err := st.CreateProject(store.CreateProjectRequest{
		Key:      "kitty",
		Name:     "KittyPaw",
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	provider := &promptCaptureProvider{response: `{
		"id": "pm",
		"display_name": "PM",
		"description": "요구사항 정리, 우선순위 조율, 진행상황 추적, 블로커 관리",
		"aliases": ["pm", "피엠"],
		"soul": "You are PM, a KittyPaw staff member.\n\n## Role\n요구사항 정리, 우선순위 조율, 진행상황 추적, 블로커 관리\n\n## Working Style\n- Keep plans practical.\n- Respond in Korean."
	}`}
	sess := &Session{
		Provider:  provider,
		Store:     st,
		Config:    &cfg,
		BaseDir:   baseDir,
		AccountID: project.ProjectConversationID,
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("우리 대화내용을 보고 pm 을 한사람 채용해주세요."), nil)
	if err != nil {
		t.Fatalf("Run request error: %v", err)
	}
	if !strings.Contains(out, "Staff 기능") || strings.Contains(out, "KittyPaw Staff") {
		t.Fatalf("first response = %q, want non-branded Staff opt-in question", out)
	}

	out, err = sess.Run(context.Background(), webChatEvent("네네"), nil)
	if err != nil {
		t.Fatalf("Run opt-in error: %v", err)
	}
	if !strings.Contains(out, "초안") || !strings.Contains(out, "이대로 생성할까요") {
		t.Fatalf("opt-in response = %q, want draft preview confirmation", out)
	}
	if strings.Contains(out, "도움이 됐다니") || strings.Contains(out, "또 필요하면") {
		t.Fatalf("opt-in response lost staff context: %q", out)
	}

	out, err = sess.Run(context.Background(), webChatEvent("네"), nil)
	if err != nil {
		t.Fatalf("Run approval error: %v", err)
	}
	if !strings.Contains(out, "staff를 만들었어요") || !strings.Contains(out, "지금 이 대화") {
		t.Fatalf("approval response = %q, want staff creation plus switch prompt", out)
	}
	if strings.Contains(out, "도움이 됐다니") || strings.Contains(out, "또 필요하면") {
		t.Fatalf("approval response lost staff context: %q", out)
	}
	if _, err := core.ReadStaffMetaFile(baseDir, "pm"); err != nil {
		t.Fatalf("ReadStaffMetaFile(pm) error = %v", err)
	}
	scope, ok, err := st.ConversationScope(project.ProjectConversationID)
	if err != nil || !ok {
		t.Fatalf("ConversationScope(project chat) ok=%v err=%v, want ok true nil", ok, err)
	}
	if scope.ScopeType != "project" || scope.ScopeID != project.ID {
		t.Fatalf("project conversation scope = %+v, want project %s", scope, project.ID)
	}
}
