package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ExecutionStatus string
type StepStatus string

const (
	ExecutionPending   ExecutionStatus = "PENDING"
	ExecutionRunning   ExecutionStatus = "RUNNING"
	ExecutionCompleted ExecutionStatus = "COMPLETED"
	ExecutionFailed    ExecutionStatus = "FAILED"
	ExecutionCancelled ExecutionStatus = "CANCELLED"
	StepPending        StepStatus      = "PENDING"
	StepRunning        StepStatus      = "RUNNING"
	StepCompleted      StepStatus      = "COMPLETED"
	StepFailed         StepStatus      = "FAILED"
)

type ReviewIssue struct {
	Severity    string `json:"severity,omitempty"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Suggestion  string `json:"suggestion,omitempty"`
}

type ReviewVerdict struct {
	Status  string        `json:"status"`
	Score   int           `json:"score,omitempty"`
	Issues  []ReviewIssue `json:"issues,omitempty"`
	Summary string        `json:"summary,omitempty"`
}

type RoutingDecision struct {
	Profile          string   `json:"profile,omitempty"`
	Family           string   `json:"family,omitempty"`
	CandidateRank    int      `json:"candidate_rank,omitempty"`
	CandidateCount   int      `json:"candidate_count,omitempty"`
	Fallback         bool     `json:"fallback,omitempty"`
	QuotaKnown       bool     `json:"quota_known,omitempty"`
	QuotaRemaining   float64  `json:"quota_remaining,omitempty"`
	CostKnown        bool     `json:"cost_known,omitempty"`
	CostPerMTokens   float64  `json:"cost_per_m_tokens,omitempty"`
	Rationale        []string `json:"rationale,omitempty"`
}

type StepResult struct {
	StepID, Model, Provider, Output, Error string
	Role                                   Role
	Status                                 StepStatus
	StartedAt, FinishedAt                  time.Time
	InputTokens, OutputTokens, TotalTokens int64
	Routing                                RoutingDecision
}

type Execution struct {
	ID, WorkflowID, FinalResult, Error string
	WorkflowVersion, Iteration         int
	Status                             ExecutionStatus
	StartedAt, FinishedAt              time.Time
	Steps                              []StepResult
	Review                             ReviewVerdict
	PersistenceError                   string `json:"persistence_error,omitempty"`
}

type Request struct{ ID string; Input string }

type Call struct {
	ExecutionID    string
	Workflow       Workflow
	Step           Step
	Input          string
	Iteration      int
	AvoidProviders []string
	AvoidFamilies  []string
	AvoidModels    []string
}

type CallResult struct {
	Model, Provider, Family                   string
	InputTokens, OutputTokens, TotalTokens   int64
	Output                                    string
	Routing                                   RoutingDecision
}

type Executor interface{ Execute(context.Context, Call) (CallResult, error) }
type VerdictParser interface{ Parse(string) (ReviewVerdict, error) }

// Recorder is an optional persistence/telemetry hook. Engine correctness and
// successful data-plane delivery do not depend on persistence. Recorder errors
// are retained in Execution.PersistenceError instead of replacing a completed
// model result with a storage failure.
type Recorder interface{ Save(Execution) error }

type Engine struct {
	Registry *Registry
	Executor Executor
	Verdicts VerdictParser
	Recorder Recorder
}

func (e *Engine) Run(ctx context.Context, workflowID string, req Request) (ex Execution, runErr error) {
	if e.Registry == nil || e.Executor == nil || e.Verdicts == nil {
		return Execution{}, errors.New("orchestration engine is not configured")
	}
	wf, ok := e.Registry.Get(workflowID)
	if !ok || !wf.Enabled {
		return Execution{}, fmt.Errorf("workflow_not_found: %s", workflowID)
	}
	if err := wf.Validate(); err != nil {
		return Execution{}, fmt.Errorf("workflow_invalid: %w", err)
	}

	ex = Execution{ID: req.ID, WorkflowID: wf.ID, WorkflowVersion: wf.Version, Status: ExecutionRunning, StartedAt: time.Now()}
	recorder := e.Recorder
	if recorder == nil {
		if auto, ok := e.Executor.(Recorder); ok {
			recorder = auto
		}
	}
	defer func() {
		if ex.FinishedAt.IsZero() && ex.Status != ExecutionRunning {
			ex.FinishedAt = time.Now()
		}
		if recorder != nil && ex.ID != "" {
			if err := recorder.Save(ex); err != nil {
				ex.PersistenceError = err.Error()
			}
		}
	}()

	candidate := ""
	critique := ""
	avoidProviders, avoidFamilies, avoidModels := []string{}, []string{}, []string{}
	for iteration := 1; iteration <= wf.Policy.MaxIterations; iteration++ {
		ex.Iteration = iteration
		for _, step := range wf.Steps {
			if iteration > 1 && (step.Role == RoleDeveloper || step.Role == RoleCritic) {
				continue
			}
			if err := ctx.Err(); err != nil {
				ex.Status = ExecutionCancelled
				ex.Error = err.Error()
				ex.FinishedAt = time.Now()
				return ex, err
			}

			input := buildInput(step.Role, req.Input, candidate, critique, ex.Review)
			started := time.Now()
			result, err := e.Executor.Execute(ctx, Call{
				ExecutionID:    ex.ID,
				Workflow:       wf,
				Step:           step,
				Input:          input,
				Iteration:      iteration,
				AvoidProviders: append([]string(nil), avoidProviders...),
				AvoidFamilies:  append([]string(nil), avoidFamilies...),
				AvoidModels:    append([]string(nil), avoidModels...),
			})
			sr := StepResult{
				StepID:       step.ID,
				Role:         step.Role,
				Status:       StepCompleted,
				StartedAt:    started,
				FinishedAt:   time.Now(),
				Model:        result.Model,
				Provider:     result.Provider,
				Output:       result.Output,
				InputTokens:  result.InputTokens,
				OutputTokens: result.OutputTokens,
				TotalTokens:  result.TotalTokens,
				Routing:      result.Routing,
			}
			if err != nil {
				sr.Status = StepFailed
				sr.Error = err.Error()
				ex.Steps = append(ex.Steps, sr)
				if ctx.Err() != nil {
					ex.Status = ExecutionCancelled
				} else {
					ex.Status = ExecutionFailed
				}
				ex.Error = err.Error()
				ex.FinishedAt = time.Now()
				return ex, err
			}
			ex.Steps = append(ex.Steps, sr)

			avoidProviders = appendUnique(avoidProviders, result.Provider)
			avoidFamilies = appendUnique(avoidFamilies, result.Family)
			avoidModels = appendUnique(avoidModels, result.Model)

			switch step.Role {
			case RoleDeveloper, RoleFixer:
				candidate = result.Output
			case RoleCritic:
				critique = result.Output
			case RoleReviewer:
				verdict, parseErr := e.Verdicts.Parse(result.Output)
				if parseErr != nil {
					ex.Status = ExecutionFailed
					ex.Error = "invalid reviewer verdict: " + parseErr.Error()
					ex.FinishedAt = time.Now()
					return ex, errors.New(ex.Error)
				}
				ex.Review = verdict
				if strings.EqualFold(verdict.Status, "PASS") {
					ex.Status = ExecutionCompleted
					ex.FinalResult = candidate
					ex.FinishedAt = time.Now()
					return ex, nil
				}
			}
		}
	}

	// Review-limit exhaustion still returns the best last candidate, but records
	// an explicit failed quality gate. The data plane can return the candidate
	// while admin history makes the non-PASS outcome unambiguous.
	ex.Status = ExecutionCompleted
	ex.FinalResult = candidate
	ex.FinishedAt = time.Now()
	if ex.Review.Status == "" {
		ex.Review.Status = "FAIL"
	}
	if ex.Error == "" {
		ex.Error = "workflow_review_limit"
	}
	return ex, nil
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func buildInput(role Role, original, candidate, critique string, review ReviewVerdict) string {
	switch role {
	case RoleDeveloper:
		return "Original request:\n" + original
	case RoleCritic:
		return "Original request:\n" + original + "\n\nCandidate:\n" + candidate + "\n\nFind concrete defects, omissions, security/performance/edge-case issues. Do not rewrite the answer."
	case RoleFixer:
		issues := review.Summary
		for _, issue := range review.Issues {
			issues += "\n- " + issue.Description
		}
		return "Original request:\n" + original + "\n\nCurrent candidate:\n" + candidate + "\n\nCritique:\n" + critique + "\n\nReviewer blocking issues:\n" + issues + "\n\nReturn the corrected final candidate only."
	case RoleReviewer:
		return "Original request:\n" + original + "\n\nCandidate to review:\n" + candidate + "\n\nReturn a strict PASS or FAIL quality-gate verdict with blocking issues."
	default:
		return original
	}
}

type StrictVerdictParser struct{}

func (StrictVerdictParser) Parse(v string) (ReviewVerdict, error) {
	t := strings.TrimSpace(v)
	if t == "" {
		return ReviewVerdict{}, errors.New("reviewer verdict is empty")
	}

	// Structured output is accepted when available, but orchestration does not
	// depend on JSON mode because many compatible providers do not expose it.
	if strings.HasPrefix(t, "{") {
		var verdict ReviewVerdict
		if err := json.Unmarshal([]byte(t), &verdict); err == nil {
			verdict.Status = strings.ToUpper(strings.TrimSpace(verdict.Status))
			if verdict.Status == "PASS" || verdict.Status == "FAIL" {
				return verdict, nil
			}
		}
	}

	fields := strings.Fields(t)
	if len(fields) == 0 {
		return ReviewVerdict{}, errors.New("reviewer verdict is empty")
	}
	first := strings.ToUpper(strings.Trim(fields[0], ":;,.!?[](){}"))
	switch first {
	case "PASS":
		return ReviewVerdict{Status: "PASS", Summary: t}, nil
	case "FAIL":
		return ReviewVerdict{Status: "FAIL", Summary: t}, nil
	default:
		return ReviewVerdict{}, errors.New("reviewer must start response with exact PASS or FAIL")
	}
}
