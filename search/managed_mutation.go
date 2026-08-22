package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	managedMutationBaseOverhead  = int64(256)
	managedMutationFieldOverhead = int64(64)
)

func (managed *managedIndex) mutationPayloadBytes(ctx context.Context, request mutationRequest) (int64, error) {
	if request.kind == mutationDelete {
		if request.identifier == "" || !utf8.ValidString(request.identifier) ||
			len(request.identifier) > managed.build.MaxIdentifierBytes {
			return 0, fmt.Errorf("search: document identifier is invalid or exceeds %d bytes", managed.build.MaxIdentifierBytes)
		}
		return int64(len(request.identifier)) + managedMutationBaseOverhead, nil
	}
	if request.kind != mutationAdd && request.kind != mutationUpdate {
		return 0, fmt.Errorf("search: unknown managed mutation")
	}
	document := request.document
	if document.ID == "" || !utf8.ValidString(document.ID) || len(document.ID) > managed.build.MaxIdentifierBytes {
		return 0, fmt.Errorf("search: document identifier is invalid or exceeds %d bytes", managed.build.MaxIdentifierBytes)
	}
	documentBytes := int64(len(document.ID))
	if documentBytes > int64(managed.build.MaxDocumentBytes) {
		return 0, fmt.Errorf("search: document %q exceeds %d bytes", document.ID, managed.build.MaxDocumentBytes)
	}
	accountedBytes := documentBytes + managedMutationBaseOverhead
	position := 0
	for name, text := range document.Fields {
		if position&63 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		position++
		if _, _, exists := managed.schema.field(name); !exists {
			return 0, fmt.Errorf("search: document %q contains unknown field %q", document.ID, name)
		}
		fieldBytes := int64(len(name)) + int64(len(text))
		if fieldBytes > math.MaxInt64-documentBytes || documentBytes+fieldBytes > int64(managed.build.MaxDocumentBytes) {
			return 0, fmt.Errorf("search: document %q exceeds %d bytes", document.ID, managed.build.MaxDocumentBytes)
		}
		documentBytes += fieldBytes
		if fieldBytes > math.MaxInt64-managedMutationFieldOverhead ||
			accountedBytes > math.MaxInt64-fieldBytes-managedMutationFieldOverhead {
			return 0, ErrMutationTooLarge
		}
		accountedBytes += fieldBytes + managedMutationFieldOverhead
	}
	if accountedBytes <= 0 {
		return 0, ErrMutationTooLarge
	}
	return accountedBytes, nil
}

func (managed *managedIndex) cloneMutation(ctx context.Context, request mutationRequest) (mutationRequest, error) {
	if request.kind == mutationDelete {
		request.identifier = strings.Clone(request.identifier)
		return request, nil
	}
	fields := make(map[string]string, len(request.document.Fields))
	position := 0
	for name, value := range request.document.Fields {
		if position&63 == 0 {
			if err := ctx.Err(); err != nil {
				return mutationRequest{}, err
			}
		}
		position++
		_, field, exists := managed.schema.field(name)
		if !exists {
			return mutationRequest{}, fmt.Errorf("search: document %q changed during admission", request.document.ID)
		}
		fields[field.Name] = strings.Clone(value)
	}
	request.document = Document{ID: strings.Clone(request.document.ID), Fields: fields}
	return request, nil
}

func (managed *managedIndex) admissionError(caller context.Context, err error) error {
	if err == nil {
		return nil
	}
	if caller != nil && caller.Err() != nil {
		return caller.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if managed.ctx.Err() != nil || managed.manager.ctx.Err() != nil {
			return ErrClosed
		}
	}
	return err
}

func (managed *managedIndex) releaseMutationBytes(bytes int64) {
	managed.manager.pendingMutationBytes.Add(-bytes)
	managed.pendingMutationBytes.Add(-bytes)
	managed.manager.mutationBytes.release(bytes)
	managed.mutationBytes.release(bytes)
}
