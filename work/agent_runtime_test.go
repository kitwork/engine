package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "agent_runtime_test.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	return db, dbPath
}

func setupNewsroomManifest() AgentManifest {
	return AgentManifest{
		ID:          "agent_withai_newsroom",
		Name:        "WithAI Newsroom Agent",
		Version:     "1.0.0",
		Description: "Automated AI news gathering and verification agent",
		Steps: []AgentStepDef{
			{Name: "Fetch RSS Source", SkillName: "newsroom.fetch_source", RequiresApproval: false},
			{Name: "Draft Article HTML", SkillName: "newsroom.draft_article", RequiresApproval: false},
			{Name: "Human Approval Checkpoint", SkillName: "newsroom.human_checkpoint", RequiresApproval: true},
			{Name: "Publish to WithAI.vn", SkillName: "newsroom.publish_article", RequiresApproval: false},
		},
	}
}

func setupStepHandlers() map[string]StepHandler {
	return map[string]StepHandler{
		"newsroom.fetch_source": func(ctx context.Context, run *DBAgentRun, input map[string]any) (map[string]any, error) {
			return map[string]any{
				"source_title": "Breakthrough in AI Agent Architectures",
				"source_url":   "https://example.com/ai-news",
			}, nil
		},
		"newsroom.draft_article": func(ctx context.Context, run *DBAgentRun, input map[string]any) (map[string]any, error) {
			return map[string]any{
				"draft_html": "<article><h1>AI Agent Breakthrough</h1><p>Details here...</p></article>",
				"x_summary":  "New breakthrough in AI Agent architectures released today!",
			}, nil
		},
		"newsroom.human_checkpoint": func(ctx context.Context, run *DBAgentRun, input map[string]any) (map[string]any, error) {
			return map[string]any{"checkpoint": "passed"}, nil
		},
		"newsroom.publish_article": func(ctx context.Context, run *DBAgentRun, input map[string]any) (map[string]any, error) {
			return map[string]any{
				"published_url": "https://withai.vn/news/ai-agent-breakthrough",
				"publish_id":    "pub_998877",
			}, nil
		},
	}
}

func TestAgentRuntimeSlice1HappyPath(t *testing.T) {
	db, _ := openTestDB(t)
	defer db.Close()

	store, err := NewAgentStore(db)
	if err != nil {
		t.Fatalf("NewAgentStore error: %v", err)
	}

	runtime := NewAgentRuntime(store, "tenant_withai")
	manifest := setupNewsroomManifest()
	handlers := setupStepHandlers()
	runtime.RegisterAgent(manifest, handlers)

	// 1. Create Run
	run, err := runtime.CreateRun("agent_withai_newsroom", map[string]any{"rss_feed": "https://example.com/rss"})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}
	if run.Status != "Pending" {
		t.Fatalf("initial run status = %s, want Pending", run.Status)
	}

	// 2. Execute Run until Human Approval Checkpoint
	execErr := runtime.ExecuteRun(context.Background(), run.ID)
	if !errors.Is(execErr, ErrApprovalRequired) {
		t.Fatalf("ExecuteRun error = %v, want ErrApprovalRequired", execErr)
	}

	// Verify status is WaitingForApproval
	pausedRun, err := store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun error: %v", err)
	}
	if pausedRun.Status != "WaitingForApproval" {
		t.Fatalf("paused run status = %s, want WaitingForApproval", pausedRun.Status)
	}

	// Obtain approval token
	token := fmt.Sprintf("appr_%s_2", run.ID)

	// 3. Submit Approval
	completedRun, err := runtime.SubmitApproval(context.Background(), token, true, "Article content approved by editor")
	if err != nil {
		t.Fatalf("SubmitApproval error: %v", err)
	}
	if completedRun.Status != "Completed" {
		t.Fatalf("completed run status = %s, want Completed", completedRun.Status)
	}

	// 4. Verify Timeline
	timeline, err := runtime.GetRunTimeline(run.ID)
	if err != nil {
		t.Fatalf("GetRunTimeline error: %v", err)
	}
	if len(timeline) != 4 {
		t.Fatalf("timeline count = %d, want 4 steps", len(timeline))
	}
}

func TestAgentRuntimeSlice1Rejection(t *testing.T) {
	db, _ := openTestDB(t)
	defer db.Close()

	store, _ := NewAgentStore(db)
	runtime := NewAgentRuntime(store, "tenant_withai")
	manifest := setupNewsroomManifest()
	handlers := setupStepHandlers()
	runtime.RegisterAgent(manifest, handlers)

	run, _ := runtime.CreateRun("agent_withai_newsroom", map[string]any{"rss_feed": "https://example.com/rss"})
	_ = runtime.ExecuteRun(context.Background(), run.ID)

	token := fmt.Sprintf("appr_%s_2", run.ID)

	// Reject Approval
	rejectedRun, err := runtime.SubmitApproval(context.Background(), token, false, "Fact check unverified")
	if err != nil {
		t.Fatalf("SubmitApproval rejection error: %v", err)
	}
	if rejectedRun.Status != "Cancelled" {
		t.Fatalf("rejected run status = %s, want Cancelled", rejectedRun.Status)
	}
}

func TestAgentRuntimeSlice1ProcessRestart(t *testing.T) {
	db, dbPath := openTestDB(t)

	// 1. Initial Host Process Session
	store1, _ := NewAgentStore(db)
	runtime1 := NewAgentRuntime(store1, "tenant_withai")
	manifest := setupNewsroomManifest()
	handlers := setupStepHandlers()
	runtime1.RegisterAgent(manifest, handlers)

	run, err := runtime1.CreateRun("agent_withai_newsroom", map[string]any{"topic": "quantum AI"})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	_ = runtime1.ExecuteRun(context.Background(), run.ID)
	db.Close() // SIMULATE PROCESS SIGKILL / SHUTDOWN

	// 2. Re-open DB in new Host Process Session
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("re-opening sqlite db error: %v", err)
	}
	defer db2.Close()

	store2, err := NewAgentStore(db2)
	if err != nil {
		t.Fatalf("NewAgentStore on restarted host error: %v", err)
	}

	runtime2 := NewAgentRuntime(store2, "tenant_withai")
	runtime2.RegisterAgent(manifest, handlers)

	// Verify run state in new process is WaitingForApproval
	restartedRun, err := store2.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun on restarted host error: %v", err)
	}
	if restartedRun.Status != "WaitingForApproval" {
		t.Fatalf("restarted run status = %s, want WaitingForApproval", restartedRun.Status)
	}

	// Submit approval on restarted process
	token := fmt.Sprintf("appr_%s_2", run.ID)
	resumedRun, err := runtime2.SubmitApproval(context.Background(), token, true, "Approved after host restart")
	if err != nil {
		t.Fatalf("SubmitApproval on restarted host error: %v", err)
	}

	if resumedRun.Status != "Completed" {
		t.Fatalf("resumed run status = %s, want Completed", resumedRun.Status)
	}

	// Cleanup test DB file
	_ = os.Remove(dbPath)
}
