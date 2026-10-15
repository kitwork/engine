package kitsql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
)

func TestExecutionErrorClassification(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
		retry  bool
	}{
		{kitdb.ErrTransactionConflict, 409, "KSQL_CONFLICT", true},
		{kitdb.ErrSnapshotAdmissionFull, 503, "KSQL_BUSY", true},
		{kitdb.ErrSnapshotMemory, 503, "KSQL_BUSY", true},
		{kitdb.ErrTooManySnapshots, 503, "KSQL_BUSY", true},
		{kitdb.ErrActiveTransactionMemory, 503, "KSQL_BUSY", true},
		{node.ErrCheckpointBackoff, 503, "KSQL_BUSY", true},
		{node.ErrMaintenanceQueueFull, 503, "KSQL_BUSY", true},
		{kitdb.ErrSnapshotsActive, 503, "KSQL_BUSY", true},
		{node.ErrClosed, 503, "KSQL_UNAVAILABLE", false},
		{kitdb.ErrSnapshotAdmissionTimeout, 503, "KSQL_ADMISSION_TIMEOUT", true},
		{context.DeadlineExceeded, 504, "KSQL_TIMEOUT", false},
		{context.Canceled, 408, "KSQL_CANCELLED", false},
		{kitdb.ErrClosed, 503, "KSQL_UNAVAILABLE", false},
		{errors.Join(kitdb.ErrDurabilityUncertain, kitdb.ErrUnavailable), 500, "KSQL_DURABILITY", false},
		{errors.New("invalid SQL"), 400, "KSQL_EXECUTION", false},
		{pgwire.NewError("42501", "permission denied"), 403, "KSQL_FORBIDDEN", false},
		{pgwire.NewError("42601", "permission denied"), 400, "KSQL_EXECUTION", false},
		{errors.New("42501 permission denied"), 400, "KSQL_EXECUTION", false},
		{errors.Join(kitdb.ErrDurabilityUncertain, pgwire.NewError("42501", "denied")), 500, "KSQL_DURABILITY", false},
	} {
		t.Run(test.code+test.err.Error(), func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", test.err)
			writer := httptest.NewRecorder()
			writeExecutionError(writer, err)
			var response Response
			if e := json.Unmarshal(writer.Body.Bytes(), &response); e != nil {
				t.Fatal(e)
			}
			if writer.Code != test.status || response.Error == nil || response.Error.Code != test.code {
				t.Fatalf("classification: %d %+v", writer.Code, response)
			}
			remote := &ProtocolError{Status: writer.Code, Code: response.Error.Code, Message: response.Error.Message}
			if remote.Retryable() != test.retry {
				t.Fatalf("retryable=%v", remote.Retryable())
			}
		})
	}
	var empty *ProtocolError
	if empty.Retryable() {
		t.Fatal("nil error retryable")
	}
}
