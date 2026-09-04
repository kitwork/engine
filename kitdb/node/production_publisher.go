package node

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/kitdb"
)

const maximumProductionPublisherNameBytes = 128

var (
	ErrProductionPublisherExists = errors.New(
		"kitdb node: production anchor publisher already exists",
	)
	ErrProductionPublisherNotFound = errors.New(
		"kitdb node: production anchor publisher does not exist",
	)
	ErrProductionPublisherBusy = errors.New(
		"kitdb node: production anchor publisher is owned by a policy",
	)
	ErrProductionPublisherTopology = errors.New(
		"kitdb node: production anchor publisher destination overlaps another publisher",
	)
	ErrProductionPublisherLimit = errors.New(
		"kitdb node: production anchor publisher entry limit reached",
	)
	ErrProductionPublication = errors.New(
		"kitdb node: production anchor publication failed",
	)
)

// ProductionAnchorReceipt is path-free proof returned only after a publisher
// has retrieved and completely verified one immutable anchor. Implementations
// must not report success from an upload acknowledgement alone.
type ProductionAnchorReceipt struct {
	DatabaseID       string
	FormatVersion    uint16
	Generation       uint64
	Transaction      uint64
	BoundaryChecksum uint32
	Records          uint64
	Bytes            int64
	SHA256           string
	AlreadyPublished bool
	PrunedAnchors    int
}

// ProductionAnchorPublisher moves a verified local anchor to another failure
// domain. It is host-owned, receives filesystem authority, and must be safe to
// call repeatedly with the same content. Tenant code must never implement or
// invoke this interface.
type ProductionAnchorPublisher interface {
	Publish(context.Context, kitdb.BackupAnchor) (ProductionAnchorReceipt, error)
}

// RegisterProductionAnchorPublisher installs one process-local host adapter.
// Publisher names are labels, never paths or credentials.
func (manager *Manager) RegisterProductionAnchorPublisher(
	name string,
	publisher ProductionAnchorPublisher,
) error {
	if manager == nil {
		return ErrClosed
	}
	if err := validateProductionPublisherName(name); err != nil {
		return err
	}
	if publisher == nil {
		return fmt.Errorf("kitdb node: production anchor publisher is nil")
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed || manager.ctx.Err() != nil {
		return ErrClosed
	}
	if manager.productionPublishers[name] != nil {
		return ErrProductionPublisherExists
	}
	if len(manager.productionPublishers) >= manager.limits.production.maxPolicies {
		return ErrProductionPublisherLimit
	}
	if err := manager.validateProductionPublisherRegistrationTopology(publisher); err != nil {
		return err
	}
	manager.productionPublishers[name] = publisher
	return nil
}

// UnregisterProductionAnchorPublisher removes one unused process-local
// adapter. It never deletes a published anchor.
func (manager *Manager) UnregisterProductionAnchorPublisher(name string) error {
	if manager == nil {
		return ErrClosed
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed {
		return ErrClosed
	}
	if manager.productionPublishers[name] == nil {
		return ErrProductionPublisherNotFound
	}
	if manager.productionPublisherOwners[name] != "" {
		return ErrProductionPublisherBusy
	}
	delete(manager.productionPublishers, name)
	return nil
}

// ProductionAnchorPublishers returns registered path-free labels in stable
// order. It exposes no publisher implementation or destination.
func (manager *Manager) ProductionAnchorPublishers() []string {
	if manager == nil {
		return nil
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	names := make([]string, 0, len(manager.productionPublishers))
	for name := range manager.productionPublishers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateProductionPublisherName(name string) error {
	if name == "" || strings.TrimSpace(name) != name ||
		len(name) > maximumProductionPublisherNameBytes {
		return fmt.Errorf("kitdb node: invalid production anchor publisher name")
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("kitdb node: invalid production anchor publisher name")
	}
	return nil
}

func productionAnchorReceipt(
	anchor kitdb.BackupAnchor,
	alreadyPublished bool,
	pruned int,
) ProductionAnchorReceipt {
	return ProductionAnchorReceipt{
		DatabaseID: anchor.DatabaseID, FormatVersion: anchor.FormatVersion,
		Generation: anchor.Generation, Transaction: anchor.Transaction,
		BoundaryChecksum: anchor.BoundaryChecksum, Records: anchor.Records,
		Bytes: anchor.Bytes, SHA256: anchor.SHA256,
		AlreadyPublished: alreadyPublished, PrunedAnchors: pruned,
	}
}

func productionReceiptMatchesAnchor(
	receipt ProductionAnchorReceipt,
	anchor kitdb.BackupAnchor,
) bool {
	return receipt.DatabaseID == anchor.DatabaseID &&
		receipt.FormatVersion == anchor.FormatVersion &&
		receipt.Generation == anchor.Generation &&
		receipt.Transaction == anchor.Transaction &&
		receipt.BoundaryChecksum == anchor.BoundaryChecksum &&
		receipt.Records == anchor.Records &&
		receipt.Bytes == anchor.Bytes &&
		receipt.SHA256 == anchor.SHA256
}

func productionAnchorsMatch(left, right kitdb.BackupAnchor) bool {
	return left.DatabaseID == right.DatabaseID &&
		left.FormatVersion == right.FormatVersion &&
		left.Generation == right.Generation &&
		left.Transaction == right.Transaction &&
		left.BoundaryChecksum == right.BoundaryChecksum &&
		left.Records == right.Records &&
		left.Bytes == right.Bytes &&
		left.SHA256 == right.SHA256
}

type productionDirectoryPublisher interface {
	productionPublisherDirectory() string
}

func (manager *Manager) validateProductionPublisherRegistrationTopology(
	publisher ProductionAnchorPublisher,
) error {
	candidate, ok := publisher.(productionDirectoryPublisher)
	if !ok {
		return nil
	}
	candidateDirectory := candidate.productionPublisherDirectory()
	for _, current := range manager.productionPublishers {
		directoryPublisher, ok := current.(productionDirectoryPublisher)
		if !ok {
			continue
		}
		currentDirectory := directoryPublisher.productionPublisherDirectory()
		candidateInside, err := pathInsideDirectory(currentDirectory, candidateDirectory)
		if err != nil {
			return err
		}
		currentInside, err := pathInsideDirectory(candidateDirectory, currentDirectory)
		if err != nil {
			return err
		}
		if candidateInside || currentInside {
			return ErrProductionPublisherTopology
		}
	}
	return nil
}
