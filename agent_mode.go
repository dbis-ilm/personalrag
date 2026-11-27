package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dbis-ilm/askyourdocs"

	"github.com/dbis-ilm/personalrag/internal/agent"
	"github.com/dbis-ilm/personalrag/internal/history"
	"github.com/dbis-ilm/personalrag/internal/i18n"
)

const (
	// EventAgentStep reports each tool call of a running agent task.
	EventAgentStep = "agent:step"

	agentMaxTurns = 60
	agentTimeout  = 30 * time.Minute
)

// AgentStep is one tool call of an agent task, as the UI shows it.
type AgentStep struct {
	TurnID  int64  `json:"turnId"`
	Tool    string `json:"tool"`
	Input   string `json:"input"`
	Summary string `json:"summary"`
	Error   string `json:"error,omitempty"`
	Millis  int64  `json:"millis"`
}

// AgentStart identifies a started agent task.
type AgentStart struct {
	ConversationID string `json:"conversationId"`
	TurnID         int64  `json:"turnId"`
}

// Agent starts an agent task: the agent model plans and calls the document
// tools until it can answer. Each tool call arrives as an agent:step event,
// the answer as answer:chunk and answer:done, like an ordinary answer. The
// task runs in the background; StopAgent ends it.
func (s *AppService) Agent(conversationID, task, collectionID string) (AgentStart, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return AgentStart{}, i18n.Errorf("search.empty")
	}
	c, err := s.current()
	if err != nil {
		return AgentStart{}, err
	}
	if c.agent == nil {
		return AgentStart{}, i18n.Errorf("agent.unavailable")
	}

	var earlier []history.Turn
	if conversationID == "" {
		conversationID = askyourdocs.NewRandomHexID(8)
		if _, err := c.hist.Create(s.appCtx, conversationID, task, collectionID); err != nil {
			return AgentStart{}, err
		}
	} else if _, earlier, err = c.hist.Get(s.appCtx, conversationID); err != nil {
		return AgentStart{}, err
	}
	turnID, err := c.hist.AddTurn(s.appCtx, history.Turn{ConversationID: conversationID, Question: task, Mode: "agent", Sources: []byte("[]")})
	if err != nil {
		return AgentStart{}, err
	}

	runCtx, cancel := context.WithTimeout(s.appCtx, agentTimeout)
	s.mu.Lock()
	if s.agentRuns == nil {
		s.agentRuns = map[int64]context.CancelFunc{}
	}
	s.agentRuns[turnID] = cancel
	s.mu.Unlock()

	prompt := agentPrompt(task, collectionID, s.collectionName(collectionID), earlier)
	go s.runAgent(runCtx, cancel, c, turnID, prompt)
	return AgentStart{ConversationID: conversationID, TurnID: turnID}, nil
}

func (s *AppService) runAgent(ctx context.Context, cancel context.CancelFunc, c *core, turnID int64, prompt string) {
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.agentRuns, turnID)
		s.mu.Unlock()
	}()
	steps := 0
	ctx = agent.WithSteps(ctx, func(st agent.Step) {
		steps++
		s.emit(EventAgentStep, AgentStep{TurnID: turnID, Tool: st.Tool, Input: st.Input, Summary: st.Summary, Error: st.Error, Millis: st.Took.Milliseconds()})
	})
	started := time.Now()
	answer, err := agent.Run(ctx, c.agent.g, c.agent.model, c.agent.config, c.agent.tools, prompt, agentMaxTurns)
	took := time.Since(started)

	done := AnswerDone{TurnID: turnID, Note: i18n.T("agent.done", steps, formatDuration(took), modelName(c.agent.spec))}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		done.Error = i18n.T("agent.stopped")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		done.Error = i18n.T("agent.timeout", int(agentTimeout.Minutes()))
	case err != nil:
		slog.Error("agent task failed", "err", err)
		done.Error = i18n.T("answer.failed", err.Error())
	default:
		s.emit(EventAnswerChunk, AnswerChunk{TurnID: turnID, Text: answer})
	}
	if err := c.hist.Finish(s.appCtx, turnID, answer, "", done.Note, done.Error); err != nil {
		slog.Error("history: saving agent answer failed", "err", err)
	}
	s.emit(EventAnswerDone, done)
}

// StopAgent ends a running agent task; its answer stays empty.
func (s *AppService) StopAgent(turnID int64) {
	s.mu.Lock()
	cancel := s.agentRuns[turnID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// agentPrompt gives the task its context: the collection to stay in and
// the last turns of the conversation, so follow-ups ("as a table") work.
func agentPrompt(task, collectionID, collectionName string, earlier []history.Turn) string {
	var b strings.Builder
	if n := len(earlier); n > 0 {
		b.WriteString("Earlier in this conversation:\n")
		for _, t := range earlier[max(0, n-4):] {
			answer := t.Answer
			if r := []rune(answer); len(r) > 1500 {
				answer = string(r[:1500]) + " …"
			}
			fmt.Fprintf(&b, "User: %s\nAssistant: %s\n\n", t.Question, answer)
		}
	}
	if collectionID != "" {
		fmt.Fprintf(&b, "Work only with the collection %q (collection_id %q).\n\n", collectionName, collectionID)
	}
	b.WriteString("Task: ")
	b.WriteString(task)
	return b.String()
}

// formatDuration renders a run time as "42 s" or "3:05 min".
func formatDuration(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s < 60 {
		return fmt.Sprintf("%d s", s)
	}
	return fmt.Sprintf("%d:%02d min", s/60, s%60)
}
