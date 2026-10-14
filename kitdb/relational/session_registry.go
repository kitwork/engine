package relational

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
)

const DefaultMaximumSessions = 1024
const MaximumSessions = 65536

// Records contain no SQL, credentials, network addresses or filesystem paths.
// State is atomic so catalog queries never acquire another connection's lock.
type nodeSessionRecord struct {
	id                          int64
	username, database, adapter string
	started                     string
	state                       atomic.Uint32
	close                       func() error
	ctx                         context.Context
	cancel                      context.CancelFunc
	terminating                 atomic.Bool
	account                     managed.UserInfo
}

func (node *PostgresNode) registerSession(account managed.UserInfo, database, adapter string, closeSession func() error, attach func(*nodeSessionRecord)) (*nodeSessionRecord, error) {
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.closed {
		return nil, pgwire.NewError("08003", "node is closed")
	}
	if len(node.sessionRegistry) >= node.maximumSessions {
		return nil, pgwire.NewError("53300", "node session limit reached")
	}
	if node.sessionRegistry == nil {
		node.sessionRegistry = make(map[int64]*nodeSessionRecord)
	}
	node.nextSessionID++
	account.Grants = nil
	record := &nodeSessionRecord{id: node.nextSessionID, username: account.Name, database: database, adapter: adapter, account: account,
		started: time.Now().UTC().Format(time.RFC3339Nano), close: closeSession}
	record.ctx, record.cancel = context.WithCancel(context.Background())
	// Attach before publication so a concurrent node drain cannot miss removal.
	attach(record)
	node.sessionRegistry[record.id] = record
	return record, nil
}

func (node *PostgresNode) unregisterSession(record *nodeSessionRecord) {
	if record == nil {
		return
	}
	record.cancel()
	node.mu.Lock()
	delete(node.sessionRegistry, record.id)
	node.mu.Unlock()
}

// operationContext joins caller cancellation with the physical session lifetime.
func (record *nodeSessionRecord) operationContext(ctx context.Context) (context.Context, func()) {
	if record == nil {
		return ctx, func() {}
	}
	joined, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(record.ctx, cancel)
	if record.ctx.Err() != nil {
		cancel()
	}
	return joined, func() { stop(); cancel() }
}

// BindTermination lets the transport interrupt idle reads and queued queries.
func (session *postgresSession) BindTermination(terminate func()) func() {
	if session.record == nil {
		return func() {}
	}
	stop := context.AfterFunc(session.record.ctx, terminate)
	return func() { stop() }
}

func (node *PostgresNode) sessionRows(account managed.UserInfo) []map[string]any {
	admin := account.Admin
	if node.catalog != nil && node.catalog.AuthEnabled() {
		admin = false
		for _, live := range node.catalog.SessionAccounts() {
			if account.SameAccount(live) && live.Admin && !live.Disabled {
				admin = account.Admin
				break
			}
		}
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	rows := make([]map[string]any, 0, len(node.sessionRegistry))
	for _, record := range node.sessionRegistry {
		if !admin && !account.SameAccount(record.account) {
			continue
		}
		state := "idle"
		switch record.state.Load() {
		case 1:
			state = "transaction"
		case 2:
			state = "failed"
		}
		rows = append(rows, map[string]any{"session_id": record.id, "username": record.username,
			"terminating":   record.terminating.Load(),
			"database_name": record.database, "adapter": record.adapter, "started_at": record.started,
			"transaction_state": state})
	}
	return rows
}

func (session *postgresSession) registerNodeSession() (pgwire.Session, error) {
	_, err := session.node.registerSession(session.account, session.authenticator.database, "pgwire", session.Close, func(record *nodeSessionRecord) { session.record = record })
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func (session *postgresSession) refreshSessionState() {
	if session == nil || session.record == nil {
		return
	}
	status := session.TransactionStatus()
	state := uint32(0)
	if status == pgwire.TransactionActive {
		state = 1
	}
	if status == pgwire.TransactionFailed {
		state = 2
	}
	session.record.state.Store(state)
}
