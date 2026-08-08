package work

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

type AgentStore struct {
	db *sql.DB
	mu sync.Mutex
}

func NewAgentStore(db *sql.DB) (*AgentStore, error) {
	s := &AgentStore{db: db}
	if err := s.initTables(); err != nil {
		return nil, fmt.Errorf("failed to init agent store tables: %w", err)
	}
	return s, nil
}

func (s *AgentStore) initTables() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queries := []string{
		`CREATE TABLE IF NOT EXISTS agent_manifests (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			version TEXT NOT NULL,
			manifest_json TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS agent_runs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			status TEXT NOT NULL,
			current_step_index INTEGER NOT NULL DEFAULT 0,
			input_json TEXT,
			output_json TEXT,
			error_detail TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS agent_steps (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			input_data TEXT,
			output_data TEXT,
			idempotency_key TEXT UNIQUE,
			started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS agent_checkpoints (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			snapshot_json TEXT NOT NULL,
			saved_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS agent_approvals (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			approval_token TEXT UNIQUE NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			requested_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			responded_at TIMESTAMP,
			reviewer_note TEXT
		);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentStore) SaveRun(id, tenantID, agentID, status string, currentStepIndex int, inputJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO agent_runs (id, tenant_id, agent_id, status, current_step_index, input_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			current_step_index = excluded.current_step_index,
			updated_at = CURRENT_TIMESTAMP;`
	_, err := s.db.Exec(query, id, tenantID, agentID, status, currentStepIndex, inputJSON)
	return err
}

func (s *AgentStore) UpdateRunStatus(id, status, outputJSON, errorDetail string, currentStepIndex int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE agent_runs SET status = ?, output_json = ?, error_detail = ?, current_step_index = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;`
	_, err := s.db.Exec(query, status, outputJSON, errorDetail, currentStepIndex, id)
	return err
}

func (s *AgentStore) SaveStep(id, runID string, stepIndex int, name, status, inputData, outputData, idempotencyKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO agent_steps (id, run_id, step_index, name, status, input_data, output_data, idempotency_key, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(idempotency_key) DO UPDATE SET
			status = excluded.status,
			output_data = excluded.output_data,
			completed_at = CURRENT_TIMESTAMP;`
	_, err := s.db.Exec(query, id, runID, stepIndex, name, status, inputData, outputData, idempotencyKey)
	return err
}

func (s *AgentStore) SaveCheckpoint(id, runID string, stepIndex int, snapshotJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO agent_checkpoints (id, run_id, step_index, snapshot_json, saved_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP);`
	_, err := s.db.Exec(query, id, runID, stepIndex, snapshotJSON)
	return err
}

func (s *AgentStore) GetLastCheckpoint(runID string) (int, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `SELECT step_index, snapshot_json FROM agent_checkpoints WHERE run_id = ? ORDER BY step_index DESC LIMIT 1;`
	var stepIndex int
	var snapshotJSON string
	err := s.db.QueryRow(query, runID).Scan(&stepIndex, &snapshotJSON)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	return stepIndex, snapshotJSON, err
}

func (s *AgentStore) SaveApproval(id, runID string, stepIndex int, token, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO agent_approvals (id, run_id, step_index, approval_token, status, requested_at) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP);`
	_, err := s.db.Exec(query, id, runID, stepIndex, token, status)
	return err
}

func (s *AgentStore) UpdateApproval(token, status, reviewerNote string) (string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE agent_approvals SET status = ?, reviewer_note = ?, responded_at = CURRENT_TIMESTAMP WHERE approval_token = ?;`
	res, err := s.db.Exec(query, status, reviewerNote, token)
	if err != nil {
		return "", 0, err
	}
	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		return "", 0, fmt.Errorf("approval token %q not found", token)
	}

	var runID string
	var stepIndex int
	q2 := `SELECT run_id, step_index FROM agent_approvals WHERE approval_token = ?;`
	err = s.db.QueryRow(q2, token).Scan(&runID, &stepIndex)
	return runID, stepIndex, err
}

func (s *AgentStore) GetRun(id string) (*DBAgentRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `SELECT id, tenant_id, agent_id, status, current_step_index, COALESCE(input_json, ''), COALESCE(output_json, ''), COALESCE(error_detail, '') FROM agent_runs WHERE id = ?;`
	run := &DBAgentRun{}
	err := s.db.QueryRow(query, id).Scan(&run.ID, &run.TenantID, &run.AgentID, &run.Status, &run.CurrentStepIndex, &run.InputJSON, &run.OutputJSON, &run.ErrorDetail)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (s *AgentStore) GetRunSteps(runID string) ([]DBAgentStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `SELECT id, run_id, step_index, name, status, COALESCE(input_data, ''), COALESCE(output_data, '') FROM agent_steps WHERE run_id = ? ORDER BY step_index ASC;`
	rows, err := s.db.Query(query, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []DBAgentStep
	for rows.Next() {
		var st DBAgentStep
		if err := rows.Scan(&st.ID, &st.RunID, &st.StepIndex, &st.Name, &st.Status, &st.InputData, &st.OutputData); err != nil {
			return nil, err
		}
		steps = append(steps, st)
	}
	return steps, nil
}

type DBAgentRun struct {
	ID               string
	TenantID         string
	AgentID          string
	Status           string
	CurrentStepIndex int
	InputJSON        string
	OutputJSON       string
	ErrorDetail      string
}

type DBAgentStep struct {
	ID         string
	RunID      string
	StepIndex  int
	Name       string
	Status     string
	InputData  string
	OutputData string
}

type DBAgentApproval struct {
	ID            string
	RunID         string
	StepIndex     int
	ApprovalToken string
	Status        string
	RequestedAt   time.Time
	RespondedAt   sql.NullTime
	ReviewerNote  string
}
