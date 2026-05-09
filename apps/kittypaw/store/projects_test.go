package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectsMigrationCreatesCoreTables(t *testing.T) {
	st := openTestStore(t)
	for _, table := range []string{
		"projects",
		"project_staff_assignments",
		"project_driver_settings",
		"tickets",
		"ticket_dependencies",
		"ticket_actions",
		"ticket_messages",
		"ticket_staff_assignments",
		"project_brief_drafts",
		"jobs",
		"job_events",
		"driver_definitions",
		"conversation_scope",
	} {
		var name string
		err := st.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestCreateProjectSetsConversationScopeAndDefaults(t *testing.T) {
	st := openTestStore(t)
	root := t.TempDir()

	project, err := st.CreateProject(CreateProjectRequest{
		Key:       "kitty",
		Name:      "KittyPaw",
		RootPath:  root,
		CreatedBy: "alice",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project.ID == "" || project.Key != "KITTY" || project.Name != "KittyPaw" {
		t.Fatalf("project identity = %+v, want ID, KITTY, KittyPaw", project)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		t.Fatalf("canonical root: %v", err)
	}
	if project.RootPath != canonical {
		t.Fatalf("RootPath = %q, want %q", project.RootPath, canonical)
	}
	if project.ProjectConversationID == "" {
		t.Fatalf("ProjectConversationID is empty: %+v", project)
	}
	scope, ok, err := st.ConversationScope(project.ProjectConversationID)
	if err != nil || !ok {
		t.Fatalf("ConversationScope() ok=%v err=%v", ok, err)
	}
	if scope.ScopeType != "project" || scope.ScopeID != project.ID {
		t.Fatalf("scope = %+v, want project scope for %s", scope, project.ID)
	}

	var defaultDriverID, defaultMode, worktreePolicy, autonomyPolicy string
	if err := st.db.QueryRow(`
		SELECT default_driver_id, default_mode, default_worktree_policy, autonomy_policy
		FROM project_driver_settings
		WHERE project_id = ?`, project.ID).Scan(&defaultDriverID, &defaultMode, &worktreePolicy, &autonomyPolicy); err != nil {
		t.Fatalf("read driver settings: %v", err)
	}
	if defaultDriverID != "codex" || defaultMode != "one_shot" || worktreePolicy != "preserve" || autonomyPolicy != "edit_and_test" {
		t.Fatalf("driver settings = %q %q %q %q", defaultDriverID, defaultMode, worktreePolicy, autonomyPolicy)
	}
}

func TestProjectKeyLocksAfterFirstTicket(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateProject(CreateProjectRequest{
		Key:      "kitty",
		Name:     "KittyPaw",
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	renamed, err := st.UpdateProjectKey(project.ID, "paw")
	if err != nil {
		t.Fatalf("UpdateProjectKey(before ticket) error = %v", err)
	}
	if renamed.Key != "PAW" {
		t.Fatalf("renamed key = %q, want PAW", renamed.Key)
	}
	if _, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "First ticket"}); err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if _, err := st.UpdateProjectKey(project.ID, "new"); err == nil {
		t.Fatal("UpdateProjectKey(after ticket) succeeded, want error")
	}
}

func TestClassifyProjectFolderEmptyishAndNonEmpty(t *testing.T) {
	emptyish := t.TempDir()
	if err := os.Mkdir(filepath.Join(emptyish, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyish, ".DS_Store"), nil, 0o644); err != nil {
		t.Fatalf("write .DS_Store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyish, ".gitignore"), []byte("tmp\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyish, "README.md"), []byte("# Emptyish\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	class, err := ClassifyProjectFolder(emptyish)
	if err != nil {
		t.Fatalf("ClassifyProjectFolder(emptyish) error = %v", err)
	}
	if class != ProjectFolderEmptyish {
		t.Fatalf("emptyish class = %q, want %q", class, ProjectFolderEmptyish)
	}

	nonEmpty := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonEmpty, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	class, err = ClassifyProjectFolder(nonEmpty)
	if err != nil {
		t.Fatalf("ClassifyProjectFolder(nonEmpty) error = %v", err)
	}
	if class != ProjectFolderNonEmpty {
		t.Fatalf("nonEmpty class = %q, want %q", class, ProjectFolderNonEmpty)
	}
}

func TestSelectProjectPMUsesMetadataAliasThenDefault(t *testing.T) {
	st := openTestStore(t)

	pm, err := st.SelectProjectPM()
	if err != nil {
		t.Fatalf("SelectProjectPM(empty) error = %v", err)
	}
	if pm != "default" {
		t.Fatalf("empty PM = %q, want default", pm)
	}

	if err := st.UpsertStaffMetaWithDisplayName("dev-pm", "개발 PM", "요구사항 정리", "[]", "test"); err != nil {
		t.Fatalf("UpsertStaffMetaWithDisplayName(dev-pm): %v", err)
	}
	if err := st.ReplaceStaffAliases("dev-pm", []string{"개발PM", "PM"}); err != nil {
		t.Fatalf("ReplaceStaffAliases(dev-pm): %v", err)
	}
	pm, err = st.SelectProjectPM()
	if err != nil {
		t.Fatalf("SelectProjectPM(alias) error = %v", err)
	}
	if pm != "dev-pm" {
		t.Fatalf("alias PM = %q, want dev-pm", pm)
	}

	if err := st.UpsertStaffMetaWithDisplayName("lead", "Lead", "project planning", `["project-manager"]`, "test"); err != nil {
		t.Fatalf("UpsertStaffMetaWithDisplayName(lead): %v", err)
	}
	pm, err = st.SelectProjectPM()
	if err != nil {
		t.Fatalf("SelectProjectPM(metadata) error = %v", err)
	}
	if pm != "lead" {
		t.Fatalf("metadata PM = %q, want lead", pm)
	}
}
