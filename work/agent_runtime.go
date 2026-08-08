package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrApprovalRequired = errors.New("execution paused: human approval required")
	ErrRunCompleted     = errors.New("run is already completed")
	ErrRunCancelled     = errors.New("run has been cancelled")
	ErrAgentNotFound    = errors.New("agent manifest not found")
)

type AgentStepDef struct {
	Name             string `json:"name"`
	SkillName        string `json:"skill_name"`
	RequiresApproval bool   `json:"requires_approval"`
}

type AgentManifest struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Steps       []AgentStepDef `json:"steps"`
}

type StepHandler func(ctx context.Context, run *DBAgentRun, input map[string]any) (map[string]any, error)

type AgentRuntime struct {
	store    *AgentStore
	tenantID string
	mu       sync.RWMutex
	agents   map[string]AgentManifest
	handlers map[string]StepHandler
}

func NewAgentRuntime(store *AgentStore, tenantID string) *AgentRuntime {
	return &AgentRuntime{
		store:    store,
		tenantID: tenantID,
		agents:   make(map[string]AgentManifest),
		handlers: make(map[string]StepHandler),
	}
}

func (ar *AgentRuntime) RegisterAgent(manifest AgentManifest, stepHandlers map[string]StepHandler) {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	ar.agents[manifest.ID] = manifest
	for k, v := range stepHandlers {
		ar.handlers[k] = v
	}
}

func (ar *AgentRuntime) CreateRun(agentID string, input map[string]any) (*DBAgentRun, error) {
	ar.mu.RLock()
	_, exists := ar.agents[agentID]
	ar.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	runID := fmt.Sprintf("run_%d", time.Now().UnixNano())
	inputBytes, _ := json.Marshal(input)

	err := ar.store.SaveRun(runID, ar.tenantID, agentID, "Pending", 0, string(inputBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to save new run: %w", err)
	}

	return ar.store.GetRun(runID)
}

func (ar *AgentRuntime) ExecuteRun(ctx context.Context, runID string) error {
	run, err := ar.store.GetRun(runID)
	if err != nil {
		return fmt.Errorf("failed to load run %s: %w", runID, err)
	}

	if run.Status == "Completed" {
		return ErrRunCompleted
	}
	if run.Status == "Cancelled" {
		return ErrRunCancelled
	}

	ar.mu.RLock()
	agent, exists := ar.agents[run.AgentID]
	ar.mu.RUnlock()
	if !exists {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, run.AgentID)
	}

	// Update status to Running
	_ = ar.store.UpdateRunStatus(run.ID, "Running", run.OutputJSON, "", run.CurrentStepIndex)

	currentInput := make(map[string]any)
	if run.InputJSON != "" {
		_ = json.Unmarshal([]byte(run.InputJSON), &currentInput)
	}

	// If resuming from checkpoint, re-hydrate output from last step
	if run.CurrentStepIndex > 0 {
		_, snapshotJSON, err := ar.store.GetLastCheckpoint(run.ID)
		if err == nil && snapshotJSON != "" {
			_ = json.Unmarshal([]byte(snapshotJSON), &currentInput)
		}
	}

	for i := run.CurrentStepIndex; i < len(agent.Steps); i++ {
		stepDef := agent.Steps[i]
		stepID := fmt.Sprintf("%s_step_%d", run.ID, i)
		idempotencyKey := fmt.Sprintf("%s:%d:%s", run.ID, i, stepDef.Name)

		// Check if step requires human approval
		if stepDef.RequiresApproval {
			// Check if approval was already granted
			approved, token, err := ar.checkApprovalStatus(run.ID, i)
			if err != nil {
				return err
			}
			if !approved {
				// Pause execution, emit token and save checkpoint
				snapshotBytes, _ := json.Marshal(currentInput)
				checkpointID := fmt.Sprintf("chk_%s_%d", run.ID, i)
				_ = ar.store.SaveCheckpoint(checkpointID, run.ID, i, string(snapshotBytes))

				if token == "" {
					token = fmt.Sprintf("appr_%s_%d", run.ID, i)
					approvalID := fmt.Sprintf("appr_id_%s_%d", run.ID, i)
					_ = ar.store.SaveApproval(approvalID, run.ID, i, token, "pending")
				}

				_ = ar.store.UpdateRunStatus(run.ID, "WaitingForApproval", string(snapshotBytes), "", i)
				return fmt.Errorf("%w (token: %s)", ErrApprovalRequired, token)
			}
		}

		// Execute step handler
		ar.mu.RLock()
		handler, hasHandler := ar.handlers[stepDef.SkillName]
		ar.mu.RUnlock()

		var stepOutput map[string]any
		var handlerErr error

		inputBytes, _ := json.Marshal(currentInput)
		if hasHandler {
			stepOutput, handlerErr = handler(ctx, run, currentInput)
		} else {
			stepOutput = map[string]any{"status": "simulated_success", "step": stepDef.Name}
		}

		if handlerErr != nil {
			errStr := handlerErr.Error()
			_ = ar.store.SaveStep(stepID, run.ID, i, stepDef.Name, "Failed", string(inputBytes), "", idempotencyKey)
			_ = ar.store.UpdateRunStatus(run.ID, "Failed", "", errStr, i)
			return fmt.Errorf("step %s failed: %w", stepDef.Name, handlerErr)
		}

		outputBytes, _ := json.Marshal(stepOutput)
		_ = ar.store.SaveStep(stepID, run.ID, i, stepDef.Name, "Completed", string(inputBytes), string(outputBytes), idempotencyKey)

		// Save checkpoint after step
		checkpointID := fmt.Sprintf("chk_%s_%d", run.ID, i)
		_ = ar.store.SaveCheckpoint(checkpointID, run.ID, i+1, string(outputBytes))

		// Merge step output into input for next step
		for k, v := range stepOutput {
			currentInput[k] = v
		}

		_ = ar.store.UpdateRunStatus(run.ID, "Running", string(outputBytes), "", i+1)
	}

	finalOutputBytes, _ := json.Marshal(currentInput)
	_ = ar.store.UpdateRunStatus(run.ID, "Completed", string(finalOutputBytes), "", len(agent.Steps))
	return nil
}

func (ar *AgentRuntime) SubmitApproval(ctx context.Context, token string, approve bool, reviewerNote string) (*DBAgentRun, error) {
	status := "rejected"
	if approve {
		status = "approved"
	}

	runID, stepIndex, err := ar.store.UpdateApproval(token, status, reviewerNote)
	if err != nil {
		return nil, fmt.Errorf("failed to update approval token: %w", err)
	}

	run, err := ar.store.GetRun(runID)
	if err != nil {
		return nil, fmt.Errorf("failed to load run: %w", err)
	}

	if !approve {
		_ = ar.store.UpdateRunStatus(runID, "Cancelled", run.OutputJSON, "Rejected by human reviewer: "+reviewerNote, stepIndex)
		return ar.store.GetRun(runID)
	}

	// If approved, update status to Running and resume execution from checkpoint
	_ = ar.store.UpdateRunStatus(runID, "Running", run.OutputJSON, "", stepIndex)
	execErr := ar.ExecuteRun(ctx, runID)
	if execErr != nil && !errors.Is(execErr, ErrApprovalRequired) {
		return nil, execErr
	}

	return ar.store.GetRun(runID)
}

func (ar *AgentRuntime) GetRunTimeline(runID string) ([]DBAgentStep, error) {
	return ar.store.GetRunSteps(runID)
}

func (ar *AgentRuntime) checkApprovalStatus(runID string, stepIndex int) (bool, string, error) {
	query := `SELECT approval_token, status FROM agent_approvals WHERE run_id = ? AND step_index = ?;`
	var token string
	var status string
	err := ar.store.db.QueryRow(query, runID, stepIndex).Scan(&token, &status)
	if err != nil {
		return false, "", nil
	}
	return status == "approved", token, nil
}
