package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type NativeRemote interface {
	Remote
	RequireFeatures(context.Context, ...string) error
	Delegate(context.Context, string, envai.DelegationInput) (envai.Delegation, error)
	Deliver(context.Context, string, envai.DeliveryInput) (envai.DeliveryReceipt, error)
	Task(context.Context, string, string) (envai.DetachedTask, error)
	CancelTask(context.Context, string, string, bool) (envai.TaskCancellation, error)
	ClassifyPhase(context.Context, string, envai.ClassificationInput) (envai.Classification, error)
}

type nativeWorkflow struct {
	server *Server
	remote NativeRemote
}

func (n *nativeWorkflow) Ready(ctx context.Context) error {
	return n.remote.RequireFeatures(ctx, "native_deliveries", "cross_team_delegation", "detached_background", "phase_classification", "delegated_job_lifecycle", "delegation_notify_origin", "strict_model")
}
func (n *nativeWorkflow) origin(task managerstate.Task) (managerstate.Session, error) {
	session, err := n.server.cfg.Store.GetSession(task.OriginSessionID)
	if err != nil {
		return session, fmt.Errorf("workflow needs its originating CEO session: %w", err)
	}
	if task.OriginChatID == "" || task.OriginTurnID == "" || task.OriginChatID != session.ChatID {
		return session, errors.New("workflow origin needs a pinned remote CEO turn")
	}
	session.Turn = &managerstate.SessionTurn{TurnID: task.OriginTurnID}
	return session, nil
}
func (n *nativeWorkflow) StartPhase(ctx context.Context, task managerstate.Task, phaseContext managerstate.PhaseContext) (PhaseExecution, error) {
	var execution PhaseExecution
	origin, err := n.origin(task)
	if err != nil {
		return execution, err
	}
	phase := task.Phases[task.PhaseIndex]
	attempt := phase.Attempts[len(phase.Attempts)-1]
	body, err := json.Marshal(struct {
		Phase   managerstate.Phase        `json:"phase"`
		Context managerstate.PhaseContext `json:"context"`
	}{phase.Phase, phaseContext})
	if err != nil {
		return execution, err
	}
	operation, created, err := n.server.cfg.Store.BeginTool(attempt.RequestID, origin.ChatID, origin.Turn.TurnID, "workflow.delegate", body, "envai")
	if err != nil {
		return execution, err
	}
	if !created {
		if operation.Status != managerstate.Completed {
			return execution, errors.New("delegation outcome uncertain; refusing duplicate admission")
		}
		err = json.Unmarshal([]byte(operation.Output), &execution)
		return execution, err
	}
	teamID, err := n.server.ensureWorkflowTeam(ctx, task.Workflow, phase.Phase.TeamRef)
	if err != nil {
		return execution, err
	}
	target, err := n.remote.CreateChat(ctx, n.server.cfg.WorkspaceID, teamID)
	if err != nil {
		return execution, err
	}
	watchSession, err := n.server.cfg.Store.CreateWorkflowSession("Workflow "+task.ID+" / "+phase.Phase.Name, task.ID)
	if err != nil {
		return execution, err
	}
	teamRef := phase.Phase.TeamRef
	if task.Workflow.ClientID != "" {
		teamRef = clientReference(task.Workflow.ClientID, teamRef)
	}
	if _, err = n.server.cfg.Store.AttachSessionIdentity(watchSession.ID, target.ID, teamRef, teamID); err != nil {
		return execution, err
	}
	watching := n.server.observeSession(watchSession.ID)
	notifyOrigin := false
	strictModel := true
	delegation, err := n.remote.Delegate(ctx, origin.ChatID, envai.DelegationInput{StrictModel: &strictModel, NotifyOrigin: &notifyOrigin, WorkspaceID: n.server.cfg.WorkspaceID, EventID: attempt.RequestID, OriginTurnID: origin.Turn.TurnID, TargetChatID: target.ID, Prompt: "Execute this workflow phase to completion. Use the native delegation lifecycle for specialist work. Preserve the full final result and state any blocker explicitly.\n" + string(body)})
	if err != nil {
		return execution, err
	}
	execution = PhaseExecution{ChatID: delegation.TargetChatID, TurnID: delegation.TurnID, NativeTaskID: delegation.TaskID, OriginChatID: origin.ChatID, SessionID: watchSession.ID, Observed: watching}
	encoded, _ := json.Marshal(execution)
	if err = n.server.cfg.Store.FinishTool(attempt.RequestID, string(encoded), false); err != nil {
		return execution, err
	}
	return execution, nil
}
func (n *nativeWorkflow) FollowPhase(ctx context.Context, execution PhaseExecution) (output string, err error) {
	if !execution.Observed || execution.SessionID == "" || execution.ChatID == "" || execution.OriginChatID == "" || execution.NativeTaskID == "" {
		return "", errors.New("native phase requires a retained observer and complete job identity")
	}

	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		session, err := n.server.cfg.Store.GetSession(execution.SessionID)
		if err != nil {
			return "", err
		}
		if session.ChatID != execution.ChatID {
			return "", errors.New("phase observer chat does not match native job")
		}
		n.server.mu.Lock()
		_, observing := n.server.active["watch:"+execution.SessionID]
		n.server.mu.Unlock()
		if !observing {
			return "", errors.New("phase observer stopped; explicit recovery required")
		}
		job, err := n.remote.Task(ctx, execution.OriginChatID, execution.NativeTaskID)
		if err != nil {
			return "", fmt.Errorf("native job outcome unavailable; explicit recovery required: %w", err)
		}
		if job.TaskID != execution.NativeTaskID || job.TargetChatID != execution.ChatID {
			return "", errors.New("native job result identity mismatch")
		}
		switch job.Status {
		case "completed":
			if job.Output == "" {
				return "", errors.New("native job completed without final synthesis output")
			}
			return job.Output, nil
		case "failed":
			return "", fmt.Errorf("native job failed: %s", job.Error)
		case "cancelled":
			return "", envai.ErrTurnCancelled
		case "running":
		default:
			return "", errors.New("unknown native job state; explicit recovery required")
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func (n *nativeWorkflow) Classify(ctx context.Context, task managerstate.Task, phaseContext managerstate.PhaseContext, output string) (Classification, error) {
	origin, err := n.origin(task)
	if err != nil {
		return Classification{}, err
	}
	phase := task.Phases[task.PhaseIndex]
	attempt := phase.Attempts[len(phase.Attempts)-1]
	body, err := json.Marshal(phaseContext)
	if err != nil {
		return Classification{}, err
	}
	input := envai.ClassificationInput{RequestID: randomID(), TaskContext: body, PhaseID: phase.Phase.ID, Attempt: attempt.Number, FinalOutput: output}
	encoded, _ := json.Marshal(input)
	if _, _, err = n.server.cfg.Store.BeginTool(input.RequestID, origin.ChatID, origin.Turn.TurnID, "workflow.classify", encoded, "envai"); err != nil {
		return Classification{}, err
	}
	response, err := n.remote.ClassifyPhase(ctx, origin.ChatID, input)
	if err != nil {
		return Classification{}, fmt.Errorf("classification request %s paused; its charge/outcome may be unknown; explicit retry creates a new request: %w", input.RequestID, err)
	}
	encoded, _ = json.Marshal(response)
	if err = n.server.cfg.Store.FinishTool(input.RequestID, string(encoded), false); err != nil {
		return Classification{}, err
	}
	return Classification{Decision: managerstate.Decision(response.Decision), Model: response.Model}, nil
}
func (n *nativeWorkflow) Notify(ctx context.Context, task managerstate.Task) error {
	origin, err := n.origin(task)
	if err != nil {
		return err
	}
	output, err := json.Marshal(task)
	if err != nil {
		return err
	}
	index := task.PhaseIndex
	if index == len(task.Phases) {
		index--
	}
	phase := task.Phases[index]
	attempt := phase.Attempts[len(phase.Attempts)-1]
	eventID := "workflow:" + task.ID + ":" + attempt.RequestID + ":" + task.Status
	receipt, err := n.remote.Deliver(ctx, origin.ChatID, envai.DeliveryInput{WorkspaceID: n.server.cfg.WorkspaceID, EventID: eventID, OriginTurnID: origin.Turn.TurnID, TaskID: task.ID, Status: task.Status, Output: string(output)})
	if err == nil && receipt.Status != "queued" && receipt.Status != "delivered" {
		return fmt.Errorf("native notification %s", receipt.Status)
	}
	return err
}
