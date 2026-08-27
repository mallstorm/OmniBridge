package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const OrchestrationSchemaVersion = 1

type OrchestrationExecutionRecord struct {
	ID              string    `json:"id"`
	WorkflowID      string    `json:"workflow_id"`
	WorkflowVersion int       `json:"workflow_version"`
	Status          string    `json:"status"`
	Iteration       int       `json:"iteration"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	FinalResult     string    `json:"final_result,omitempty"`
	Error           string    `json:"error,omitempty"`
	ReviewJSON      string    `json:"review_json,omitempty"`
}

type OrchestrationStepRecord struct {
	ExecutionID  string    `json:"execution_id"`
	Ordinal      int       `json:"ordinal"`
	StepID       string    `json:"step_id"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	Model        string    `json:"model,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	TotalTokens  int64     `json:"total_tokens"`
	Output       string    `json:"output,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type OrchestrationExecutionDetail struct {
	Execution OrchestrationExecutionRecord `json:"execution"`
	Steps     []OrchestrationStepRecord     `json:"steps"`
}

// EnsureOrchestrationSchema performs an additive, idempotent subsystem
// migration inside the canonical omnibridge.db. It intentionally does not bump
// Persistence 2.0's global schema version, so stable core migrations remain
// untouched while the orchestration subsystem is still feature-gated.
func (d *DB) EnsureOrchestrationSchema() error {
	if d == nil {
		return fmt.Errorf("orchestration storage is nil")
	}
	return d.Transaction(
		`CREATE TABLE IF NOT EXISTS orchestration_schema(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS orchestration_workflows(id TEXT NOT NULL, version INTEGER NOT NULL, name TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, payload TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(id,version));`,
		`CREATE TABLE IF NOT EXISTS orchestration_executions(id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, workflow_version INTEGER NOT NULL, status TEXT NOT NULL, iteration INTEGER NOT NULL DEFAULT 0, started_at TEXT NOT NULL, finished_at TEXT, final_result TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', review_json TEXT NOT NULL DEFAULT '');`,
		`CREATE INDEX IF NOT EXISTS idx_orchestration_exec_started ON orchestration_executions(started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_orchestration_exec_workflow ON orchestration_executions(workflow_id,started_at);`,
		`CREATE TABLE IF NOT EXISTS orchestration_steps(execution_id TEXT NOT NULL, ordinal INTEGER NOT NULL, step_id TEXT NOT NULL, role TEXT NOT NULL, status TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL, finished_at TEXT, input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0, output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', PRIMARY KEY(execution_id,ordinal), FOREIGN KEY(execution_id) REFERENCES orchestration_executions(id) ON DELETE CASCADE);`,
		`CREATE INDEX IF NOT EXISTS idx_orchestration_steps_exec ON orchestration_steps(execution_id,ordinal);`,
		fmt.Sprintf(`INSERT OR IGNORE INTO orchestration_schema(version,applied_at) VALUES(%d,%s);`, OrchestrationSchemaVersion, quoteTime(time.Now())),
	)
}

func (d *DB) SaveOrchestrationWorkflow(id string, version int, name string, enabled bool, payload any) error {
	if strings.TrimSpace(id) == "" || version < 1 {
		return fmt.Errorf("invalid orchestration workflow identity")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	stmt := fmt.Sprintf(`INSERT INTO orchestration_workflows(id,version,name,enabled,payload,updated_at) VALUES(%s,%d,%s,%d,%s,%s) ON CONFLICT(id,version) DO UPDATE SET name=excluded.name,enabled=excluded.enabled,payload=excluded.payload,updated_at=excluded.updated_at;`, quoteText(id), version, quoteText(name), boolInt(enabled), quoteText(string(raw)), quoteTime(time.Now()))
	return d.Exec(stmt)
}

func (d *DB) SaveOrchestrationExecution(ex OrchestrationExecutionRecord, steps []OrchestrationStepRecord) error {
	if strings.TrimSpace(ex.ID) == "" || strings.TrimSpace(ex.WorkflowID) == "" {
		return fmt.Errorf("invalid orchestration execution identity")
	}
	finished := "NULL"
	if !ex.FinishedAt.IsZero() {
		finished = quoteTime(ex.FinishedAt)
	}
	statements := []string{
		fmt.Sprintf(`INSERT INTO orchestration_executions(id,workflow_id,workflow_version,status,iteration,started_at,finished_at,final_result,error,review_json) VALUES(%s,%s,%d,%s,%d,%s,%s,%s,%s,%s) ON CONFLICT(id) DO UPDATE SET status=excluded.status,iteration=excluded.iteration,finished_at=excluded.finished_at,final_result=excluded.final_result,error=excluded.error,review_json=excluded.review_json;`, quoteText(ex.ID), quoteText(ex.WorkflowID), ex.WorkflowVersion, quoteText(ex.Status), ex.Iteration, quoteTime(ex.StartedAt), finished, quoteText(ex.FinalResult), quoteText(ex.Error), quoteText(ex.ReviewJSON)),
		fmt.Sprintf(`DELETE FROM orchestration_steps WHERE execution_id=%s;`, quoteText(ex.ID)),
	}
	for i, step := range steps {
		ordinal := step.Ordinal
		if ordinal < 0 {
			ordinal = i
		}
		stepFinished := "NULL"
		if !step.FinishedAt.IsZero() {
			stepFinished = quoteTime(step.FinishedAt)
		}
		statements = append(statements, fmt.Sprintf(`INSERT INTO orchestration_steps(execution_id,ordinal,step_id,role,status,model,provider,started_at,finished_at,input_tokens,output_tokens,total_tokens,output,error) VALUES(%s,%d,%s,%s,%s,%s,%s,%s,%s,%d,%d,%d,%s,%s);`, quoteText(ex.ID), ordinal, quoteText(step.StepID), quoteText(step.Role), quoteText(step.Status), quoteText(step.Model), quoteText(step.Provider), quoteTime(step.StartedAt), stepFinished, step.InputTokens, step.OutputTokens, step.TotalTokens, quoteText(step.Output), quoteText(step.Error)))
	}
	return d.Transaction(statements...)
}

func (d *DB) ListOrchestrationExecutions(limit int) ([]OrchestrationExecutionRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.Query(fmt.Sprintf(`SELECT id,workflow_id,workflow_version,status,iteration,started_at,finished_at,final_result,error,review_json FROM orchestration_executions ORDER BY started_at DESC LIMIT %d;`, limit))
	if err != nil {
		return nil, err
	}
	out := make([]OrchestrationExecutionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, orchestrationExecutionFromRow(row))
	}
	return out, nil
}

func (d *DB) GetOrchestrationExecution(id string) (OrchestrationExecutionDetail, bool, error) {
	rows, err := d.Query(fmt.Sprintf(`SELECT id,workflow_id,workflow_version,status,iteration,started_at,finished_at,final_result,error,review_json FROM orchestration_executions WHERE id=%s LIMIT 1;`, quoteText(id)))
	if err != nil {
		return OrchestrationExecutionDetail{}, false, err
	}
	if len(rows) == 0 {
		return OrchestrationExecutionDetail{}, false, nil
	}
	stepRows, err := d.Query(fmt.Sprintf(`SELECT execution_id,ordinal,step_id,role,status,model,provider,started_at,finished_at,input_tokens,output_tokens,total_tokens,output,error FROM orchestration_steps WHERE execution_id=%s ORDER BY ordinal ASC;`, quoteText(id)))
	if err != nil {
		return OrchestrationExecutionDetail{}, false, err
	}
	steps := make([]OrchestrationStepRecord, 0, len(stepRows))
	for _, row := range stepRows {
		steps = append(steps, orchestrationStepFromRow(row))
	}
	return OrchestrationExecutionDetail{Execution: orchestrationExecutionFromRow(rows[0]), Steps: steps}, true, nil
}

func orchestrationExecutionFromRow(row Row) OrchestrationExecutionRecord {
	return OrchestrationExecutionRecord{
		ID:              rowString(row, "id"),
		WorkflowID:      rowString(row, "workflow_id"),
		WorkflowVersion: rowInt(row, "workflow_version"),
		Status:          rowString(row, "status"),
		Iteration:       rowInt(row, "iteration"),
		StartedAt:       rowTime(row, "started_at"),
		FinishedAt:      rowTime(row, "finished_at"),
		FinalResult:     rowString(row, "final_result"),
		Error:           rowString(row, "error"),
		ReviewJSON:      rowString(row, "review_json"),
	}
}

func orchestrationStepFromRow(row Row) OrchestrationStepRecord {
	return OrchestrationStepRecord{
		ExecutionID:  rowString(row, "execution_id"),
		Ordinal:      rowInt(row, "ordinal"),
		StepID:       rowString(row, "step_id"),
		Role:         rowString(row, "role"),
		Status:       rowString(row, "status"),
		Model:        rowString(row, "model"),
		Provider:     rowString(row, "provider"),
		StartedAt:    rowTime(row, "started_at"),
		FinishedAt:   rowTime(row, "finished_at"),
		InputTokens:  int64(rowInt(row, "input_tokens")),
		OutputTokens: int64(rowInt(row, "output_tokens")),
		TotalTokens:  int64(rowInt(row, "total_tokens")),
		Output:       rowString(row, "output"),
		Error:        rowString(row, "error"),
	}
}

func rowString(row Row, key string) string {
	v := row[key]
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return ""
	}
}

func rowInt(row Row, key string) int {
	v := row[key]
	switch x := v.(type) {
	case int64:
		return int(x)
	case int:
		return x
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	case []byte:
		n, _ := strconv.Atoi(string(x))
		return n
	default:
		return 0
	}
}

func rowTime(row Row, key string) time.Time {
	raw := strings.TrimSpace(rowString(row, key))
	if raw == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, raw)
	return t
}
