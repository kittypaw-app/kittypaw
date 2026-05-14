package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestSkillsAPIIncludesScheduleStatus(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
	baseDir := srv.defaultRuntime().BaseDir

	runAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	if err := core.SaveSkillTo(baseDir, &core.SkillManifest{
		Name:        "remind",
		Version:     1,
		Description: "one-shot reminder",
		Enabled:     true,
		Trigger:     core.SkillTrigger{Type: "once", RunAt: runAt},
	}, `return "ok";`); err != nil {
		t.Fatalf("save once skill: %v", err)
	}
	if err := core.SaveSkillTo(baseDir, &core.SkillManifest{
		Name:        "poll",
		Version:     1,
		Description: "scheduled poll",
		Enabled:     true,
		Trigger:     core.SkillTrigger{Type: "schedule", Cron: "every 10m"},
	}, `return "ok";`); err != nil {
		t.Fatalf("save scheduled skill: %v", err)
	}
	lastRun := time.Now().UTC().Add(-3 * time.Minute)
	if err := srv.store.SetLastRun("poll", lastRun); err != nil {
		t.Fatalf("set last run: %v", err)
	}
	if err := srv.store.IncrementFailureCount("poll"); err != nil {
		t.Fatalf("increment failure count: %v", err)
	}
	if err := srv.store.IncrementFailureCount("poll"); err != nil {
		t.Fatalf("increment failure count: %v", err)
	}

	var body struct {
		Skills []struct {
			Name         string `json:"name"`
			Trigger      string `json:"trigger"`
			Cron         string `json:"cron"`
			RunAt        string `json:"run_at"`
			LastRun      string `json:"last_run"`
			FailureCount int    `json:"failure_count"`
			NextRun      string `json:"next_run"`
		} `json:"skills"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/skills", nil, http.StatusOK, &body)

	byName := make(map[string]struct {
		Name         string
		Trigger      string
		Cron         string
		RunAt        string
		LastRun      string
		FailureCount int
		NextRun      string
	}, len(body.Skills))
	for _, skill := range body.Skills {
		byName[skill.Name] = struct {
			Name         string
			Trigger      string
			Cron         string
			RunAt        string
			LastRun      string
			FailureCount int
			NextRun      string
		}(skill)
	}

	remind, ok := byName["remind"]
	if !ok {
		t.Fatalf("skills = %+v, want remind", body.Skills)
	}
	if remind.Trigger != "once" || remind.RunAt != runAt || remind.NextRun != runAt {
		t.Fatalf("remind schedule fields = %+v, want once run_at/next_run %q", remind, runAt)
	}

	poll, ok := byName["poll"]
	if !ok {
		t.Fatalf("skills = %+v, want poll", body.Skills)
	}
	if poll.Trigger != "schedule" || poll.Cron != "every 10m" {
		t.Fatalf("poll trigger fields = %+v, want schedule/every 10m", poll)
	}
	if poll.LastRun == "" || poll.NextRun == "" {
		t.Fatalf("poll schedule status = %+v, want last_run and next_run", poll)
	}
	if poll.FailureCount != 2 {
		t.Fatalf("poll failure_count = %d, want 2", poll.FailureCount)
	}
}
