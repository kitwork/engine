package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type managedReplacementCommandKind uint8

const (
	managedReplacementAdd managedReplacementCommandKind = iota
	managedReplacementFlush
	managedReplacementCommit
	managedReplacementAbort
)

type managedReplacementCommand struct {
	kind           managedReplacementCommandKind
	ctx            context.Context
	releaseContext func()
	document       Document
	reservedBytes  int64
	queuedDocument bool
	queueSlots     chan struct{}
	response       chan managedReplacementResponse
}

type managedReplacementResponse struct {
	info IndexInfo
	err  error
}

type managedReplacementState struct {
	managed *managedIndex
	ctx     context.Context
	cancel  func()

	commands   chan managedReplacementCommand
	queueSlots chan struct{}
	started    chan error
	done       chan struct{}

	finishOnce sync.Once
	terminalMu sync.Mutex
	terminal   error
}

// ManagedReplacement incrementally rebuilds one manager-owned tenant index.
// It is intentionally serial: callers should feed rows in source order and
// Commit or Abort before starting ordinary mutations for the same tenant.
type ManagedReplacement struct {
	state *managedReplacementState
}

func (managed *managedIndex) beginReplacement(ctx context.Context) (*ManagedReplacement, error) {
	if managed == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("search: replacement context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, releaseLifetime := linkContexts(ctx, managed.ctx, managed.manager.ctx)
	replacementCtx, cancelTimeout := context.WithTimeout(lifetime, managed.manager.options.ReplacementTimeout)
	var cancelOnce sync.Once
	state := &managedReplacementState{
		managed: managed,
		ctx:     replacementCtx,
		cancel: func() {
			cancelOnce.Do(func() {
				cancelTimeout()
				releaseLifetime()
			})
		},
		commands:   make(chan managedReplacementCommand),
		queueSlots: make(chan struct{}, managed.manager.options.MutationQueueSize),
		started:    make(chan error, 1),
		done:       make(chan struct{}),
	}

	managed.admission.Lock()
	if !managed.accepting {
		managed.admission.Unlock()
		state.cancel()
		return nil, ErrClosed
	}
	managed.failureMu.Lock()
	failure := managed.failure
	managed.failureMu.Unlock()
	if failure != nil {
		managed.admission.Unlock()
		state.cancel()
		return nil, failure
	}
	if managed.replacement != nil {
		managed.admission.Unlock()
		state.cancel()
		return nil, ErrReplacementActive
	}
	managed.replacement = state
	managed.manager.queuedReplacements.Add(1)
	sent := false
	select {
	case managed.requests <- mutationRequest{kind: mutationBeginReplacement, replacement: state}:
		sent = true
	case <-ctx.Done():
	case <-state.ctx.Done():
	case <-managed.ctx.Done():
	case <-managed.manager.ctx.Done():
	}
	managed.admission.Unlock()
	if !sent {
		managed.manager.queuedReplacements.Add(-1)
		managed.clearReplacement(state)
		admissionErr := state.contextError()
		state.cancel()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, admissionErr
	}

	select {
	case err := <-state.started:
		if err != nil {
			return nil, err
		}
		select {
		case <-state.done:
			return nil, state.terminalError()
		default:
		}
		return &ManagedReplacement{state: state}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-state.done:
		return nil, state.terminalError()
	case <-managed.ctx.Done():
		return nil, ErrClosed
	case <-managed.manager.ctx.Done():
		return nil, ErrClosed
	}
}

// Add validates, owns, and analyzes one document under bounded queue and byte
// budgets. Once accepted it waits for a definitive result even if ctx expires.
func (replacement *ManagedReplacement) Add(ctx context.Context, document Document) error {
	if replacement == nil || replacement.state == nil {
		return ErrClosed
	}
	if ctx == nil {
		return fmt.Errorf("search: replacement add context is nil")
	}
	state := replacement.state
	if err := state.acquireQueueSlot(ctx); err != nil {
		return err
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			<-state.queueSlots
		}
	}()

	managed := state.managed
	request := mutationRequest{kind: mutationAdd, document: document}
	admissionCtx, releaseAdmission := linkContexts(ctx, state.ctx)
	reservedBytes, err := managed.mutationPayloadBytes(admissionCtx, request)
	if err != nil {
		releaseAdmission()
		return managed.admissionError(ctx, err)
	}
	if err := managed.mutationBytes.acquire(admissionCtx, reservedBytes); err != nil {
		releaseAdmission()
		return managed.admissionError(ctx, err)
	}
	if err := managed.manager.mutationBytes.acquire(admissionCtx, reservedBytes); err != nil {
		managed.mutationBytes.release(reservedBytes)
		releaseAdmission()
		return managed.admissionError(ctx, err)
	}
	releaseBudget := true
	defer func() {
		if releaseBudget {
			managed.releaseReplacementBytes(reservedBytes)
		}
	}()
	managed.pendingReplacementDocs.Add(1)
	managed.pendingReplacementBytes.Add(reservedBytes)
	managed.manager.pendingReplacementBytes.Add(reservedBytes)

	request, err = managed.cloneMutation(admissionCtx, request)
	if err != nil {
		releaseAdmission()
		return managed.admissionError(ctx, err)
	}
	command := managedReplacementCommand{
		kind: managedReplacementAdd, ctx: admissionCtx, releaseContext: releaseAdmission,
		document: request.document, reservedBytes: reservedBytes, queuedDocument: true,
		queueSlots: state.queueSlots,
		response:   make(chan managedReplacementResponse, 1),
	}
	if err := state.send(ctx, command); err != nil {
		releaseAdmission()
		return err
	}
	releaseSlot = false
	releaseBudget = false
	response := <-command.response
	return response.err
}

// Flush writes the current bounded segment without publishing it.
func (replacement *ManagedReplacement) Flush(ctx context.Context) error {
	response, err := replacement.command(ctx, managedReplacementFlush)
	if err != nil {
		return err
	}
	return response.err
}

// Commit atomically publishes the completed replacement. A pre-publication
// error leaves the session active so Commit can be retried or Abort can clean
// it up.
func (replacement *ManagedReplacement) Commit(ctx context.Context) (IndexInfo, error) {
	response, err := replacement.command(ctx, managedReplacementCommit)
	if err != nil {
		return IndexInfo{}, err
	}
	return response.info, response.err
}

// Abort discards every uncommitted replacement artifact. It is idempotent.
func (replacement *ManagedReplacement) Abort() error {
	if replacement == nil || replacement.state == nil {
		return nil
	}
	state := replacement.state
	command := managedReplacementCommand{
		kind: managedReplacementAbort, ctx: state.ctx,
		response: make(chan managedReplacementResponse, 1),
	}
	select {
	case state.commands <- command:
		response := <-command.response
		return response.err
	case <-state.done:
		return nil
	}
}

func (replacement *ManagedReplacement) command(
	ctx context.Context,
	kind managedReplacementCommandKind,
) (managedReplacementResponse, error) {
	if replacement == nil || replacement.state == nil {
		return managedReplacementResponse{}, ErrClosed
	}
	if ctx == nil {
		return managedReplacementResponse{}, fmt.Errorf("search: replacement context is nil")
	}
	if err := ctx.Err(); err != nil {
		return managedReplacementResponse{}, err
	}
	state := replacement.state
	operationCtx, releaseOperation := linkContexts(ctx, state.ctx)
	command := managedReplacementCommand{
		kind: kind, ctx: operationCtx, releaseContext: releaseOperation,
		response: make(chan managedReplacementResponse, 1),
	}
	if err := state.send(ctx, command); err != nil {
		releaseOperation()
		return managedReplacementResponse{}, err
	}
	return <-command.response, nil
}

func (state *managedReplacementState) acquireQueueSlot(ctx context.Context) error {
	select {
	case <-state.done:
		return state.terminalError()
	default:
	}
	select {
	case state.queueSlots <- struct{}{}:
		select {
		case <-state.done:
			<-state.queueSlots
			return state.terminalError()
		default:
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-state.done:
		return state.terminalError()
	}
}

func (state *managedReplacementState) send(ctx context.Context, command managedReplacementCommand) error {
	select {
	case <-state.done:
		return state.terminalError()
	default:
	}
	select {
	case state.commands <- command:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-state.done:
		return state.terminalError()
	}
}

func (state *managedReplacementState) signalStarted(err error) {
	state.started <- err
}

func (state *managedReplacementState) rejectStart(err error) {
	state.signalStarted(err)
	state.finish(err)
}

func (state *managedReplacementState) finish(err error) {
	state.finishOnce.Do(func() {
		if err == nil {
			err = ErrClosed
		}
		state.terminalMu.Lock()
		state.terminal = err
		state.terminalMu.Unlock()
		close(state.done)
		state.managed.clearReplacement(state)
		state.cancel()
	})
}

func (state *managedReplacementState) terminalError() error {
	state.terminalMu.Lock()
	err := state.terminal
	state.terminalMu.Unlock()
	if err == nil {
		return ErrClosed
	}
	return err
}

func (state *managedReplacementState) contextError() error {
	if state.managed.ctx.Err() != nil || state.managed.manager.ctx.Err() != nil {
		return ErrClosed
	}
	if err := state.ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}

func (managed *managedIndex) clearReplacement(state *managedReplacementState) {
	managed.admission.Lock()
	if managed.replacement == state {
		managed.replacement = nil
	}
	managed.admission.Unlock()
}

func (managed *managedIndex) releaseReplacementBytes(bytes int64) {
	managed.manager.pendingReplacementBytes.Add(-bytes)
	managed.pendingReplacementBytes.Add(-bytes)
	managed.pendingReplacementDocs.Add(-1)
	managed.manager.mutationBytes.release(bytes)
	managed.mutationBytes.release(bytes)
}

func (managed *managedIndex) completeReplacementCommand(
	command managedReplacementCommand,
	response managedReplacementResponse,
) {
	managed.releaseReplacementCommand(command)
	command.response <- response
}

func (managed *managedIndex) releaseReplacementCommand(command managedReplacementCommand) {
	if command.releaseContext != nil {
		command.releaseContext()
	}
	if command.queuedDocument {
		managed.releaseReplacementBytes(command.reservedBytes)
		<-command.queueSlots
	}
}

func (managed *managedIndex) finishReplacementCommand(
	state *managedReplacementState,
	command managedReplacementCommand,
	response managedReplacementResponse,
	terminal error,
) {
	managed.releaseReplacementCommand(command)
	state.finish(terminal)
	command.response <- response
}

func (managed *managedIndex) runManagedReplacement(
	state *managedReplacementState,
) (bool, error) {
	if state == nil || state.managed != managed {
		if state != nil {
			state.rejectStart(fmt.Errorf("search: invalid managed replacement"))
		}
		return false, nil
	}
	if err := acquireBoundedSlot(state.ctx, managed.manager.replacementSlots); err != nil {
		state.rejectStart(state.contextError())
		return false, nil
	}
	managed.manager.activeReplacements.Add(1)
	defer func() {
		managed.manager.activeReplacements.Add(-1)
		<-managed.manager.replacementSlots
	}()

	replacement, err := managed.writer.BeginReplacement()
	if err != nil {
		state.rejectStart(err)
		return false, nil
	}
	state.signalStarted(nil)
	for {
		select {
		case command := <-state.commands:
			switch command.kind {
			case managedReplacementAdd:
				err := replacement.Add(command.ctx, command.document)
				managed.completeReplacementCommand(command, managedReplacementResponse{err: err})
			case managedReplacementFlush:
				err := replacement.Flush(command.ctx)
				managed.completeReplacementCommand(command, managedReplacementResponse{err: err})
			case managedReplacementCommit:
				info, commitErr := replacement.Commit(command.ctx)
				if commitErr != nil {
					if errors.Is(commitErr, ErrDurabilityUncertain) {
						terminal := managed.fail(commitErr)
						managed.finishReplacementCommand(
							state, command, managedReplacementResponse{info: info, err: terminal}, terminal,
						)
						return false, terminal
					}
					managed.completeReplacementCommand(command, managedReplacementResponse{info: info, err: commitErr})
					continue
				}
				published, openErr := managed.refreshSnapshot()
				if openErr != nil {
					terminal := managed.fail(fmt.Errorf("committed generation %d cannot be opened: %w", info.Generation, openErr))
					managed.finishReplacementCommand(
						state, command, managedReplacementResponse{info: info, err: terminal}, terminal,
					)
					return false, terminal
				}
				managed.manager.commits.Add(1)
				managed.finishReplacementCommand(
					state, command, managedReplacementResponse{info: published}, nil,
				)
				return true, nil
			case managedReplacementAbort:
				abortErr := replacement.Abort()
				managed.finishReplacementCommand(
					state, command, managedReplacementResponse{err: abortErr}, abortErr,
				)
				return false, nil
			default:
				managed.completeReplacementCommand(command, managedReplacementResponse{
					err: fmt.Errorf("search: unknown replacement command"),
				})
			}
		case <-state.ctx.Done():
			abortErr := replacement.Abort()
			state.finish(errors.Join(state.contextError(), abortErr))
			return false, nil
		}
	}
}
