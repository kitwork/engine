package kitsql

import (
	"context"
	"errors"
	"net/http"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/node"
)

func executionError(err error) (int, string) {
	switch {
	case errors.Is(err, kitdb.ErrDurabilityUncertain):
		return http.StatusInternalServerError, "KSQL_DURABILITY"
	case errors.Is(err, kitdb.ErrTransactionConflict):
		return http.StatusConflict, "KSQL_CONFLICT"
	case errors.Is(err, kitdb.ErrSnapshotAdmissionTimeout):
		return http.StatusServiceUnavailable, "KSQL_ADMISSION_TIMEOUT"
	case errors.Is(err, kitdb.ErrSnapshotAdmissionFull), errors.Is(err, kitdb.ErrSnapshotMemory), errors.Is(err, kitdb.ErrTooManySnapshots),
		errors.Is(err, kitdb.ErrTooManyTransactions), errors.Is(err, kitdb.ErrActiveTransactionMemory),
		errors.Is(err, node.ErrCheckpointBackoff), errors.Is(err, node.ErrMaintenanceQueueFull), errors.Is(err, kitdb.ErrSnapshotsActive):
		return http.StatusServiceUnavailable, "KSQL_BUSY"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "KSQL_TIMEOUT"
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "KSQL_CANCELLED"
	case errors.Is(err, kitdb.ErrClosed), errors.Is(err, kitdb.ErrUnavailable), errors.Is(err, node.ErrClosed):
		return http.StatusServiceUnavailable, "KSQL_UNAVAILABLE"
	default:
		return http.StatusBadRequest, "KSQL_EXECUTION"
	}
}

func writeExecutionError(writer http.ResponseWriter, err error) {
	status, code := executionError(err)
	writeProtocolError(writer, status, code, err.Error())
}
