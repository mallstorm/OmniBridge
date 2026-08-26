package orchestration

import (
	"context"
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

type ReviewIssue struct{ Severity, Category, Description, Suggestion string }
type ReviewVerdict struct {
	Status  string
	Score   int
	Issues  []ReviewIssue
	Summary string
}

type StepResult struct {
	StepID, Model, Provider, Output, Error string
	Role                                   Role
	Status                                 StepStatus
	StartedAt, FinishedAt                  time.Time
	InputTokens, OutputTokens, TotalTokens int64
}

type Execution struct {
	ID, WorkflowID, FinalResult, Error string
	WorkflowVersion, Iteration         int
	Status                             ExecutionStatus
	StartedAt, FinishedAt              time.Time
	Steps                              []StepResult
	Review                             ReviewVerdict
}

type Request struct{ ID string; Input string }

type Call struct {
	ExecutionID    string
	Workflow       Workflow
	Step           Step
	Input          string
	Iteration      int
	AvoidProviders []string
	AvoidModels    []string
}

type CallResult struct {
	Model, Provider, Output                 string
	InputTokens, OutputTokens, TotalTokens int64
}

type Executor interface{ Execute(context.Context, Call) (CallResult, error) }
type VerdictParser interface{ Parse(string) (ReviewVerdict, error) }

// Recorder is an optional persistence/telemetry hook. Engine correctness does
// not depend on persistence; recorder failures are returned only when the
// workflow itself otherwise succeeded, so a storage outage is observable but
// never masks the primary model/runtime error.
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
			if err := recorder.Save(ex); err != nil && runErr == nil {
				runErr = fmt.Errorf("workflow_persistence_failed: %w", err)
			}
		}
	}()

	candidate := ""
	critique := ""
	avoidProviders, avoidModels := []string{}, []string{}
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

			if wf.Policy.Diversity != DiversityNone {
				if result.Provider != "" {
					avoidProviders = append(avoidProviders, result.Provider)
				}
				if result.Model != "" {
					avoidModels = append(avoidModels, result.Model)
				}
			}

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
	upper := strings.ToUpper(t)
	if strings.HasPrefix(upper, "PASS") {
		return ReviewVerdict{Status: "PASS", Summary: t}, nil
	}
	if strings.HasPrefix(upper, "FAIL") {
		return ReviewVerdict{Status: "FAIL", Summary: t}, nil
	}
	return ReviewVerdict{}, errors.New("reviewer must start response with PASS or FAIL")
}
