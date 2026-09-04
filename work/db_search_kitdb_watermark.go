package work

import (
	"context"
	"fmt"

	kitdbengine "github.com/kitwork/engine/kitdb"
	searchprojection "github.com/kitwork/engine/kitdb/searchprojection"
	"github.com/kitwork/engine/search"
)

const (
	kitDBSearchWatermarkVersion = searchprojection.WatermarkVersion
	kitDBSearchWatermarkSize    = searchprojection.WatermarkSize
)

var errKitDBSearchWatermark = searchprojection.ErrInvalidWatermark

// kitDBSearchWatermark is the last source boundary fully represented by one
// committed search generation. The payload is fixed-size binary metadata, not
// application data, and is independently checksummed before it can advance a
// history pin.
type kitDBSearchWatermark = searchprojection.Watermark

func encodeKitDBSearchWatermark(watermark kitDBSearchWatermark) ([]byte, error) {
	return searchprojection.EncodeWatermark(watermark)
}

func decodeKitDBSearchWatermark(encoded []byte) (kitDBSearchWatermark, error) {
	return searchprojection.DecodeWatermark(encoded)
}

func (t *SchemaTable) kitDBSearchWatermark(cursor kitdbengine.HistoryCursor) kitDBSearchWatermark {
	return kitDBSearchWatermark{
		Cursor: cursor, StructID: t.definition.ID,
		IdentifierLayout: t.kitDBSearchIdentifierLayout(),
		RowGeneration:    t.rowGeneration, RowEpoch: t.rowEpoch,
	}
}

func (t *SchemaTable) kitDBSearchWatermarkMatches(
	watermark kitDBSearchWatermark,
	databaseID string,
) bool {
	return t != nil && t.definition != nil &&
		watermark.Cursor.DatabaseID == databaseID &&
		watermark.StructID == t.definition.ID &&
		watermark.IdentifierLayout == t.kitDBSearchIdentifierLayout() &&
		watermark.RowGeneration == t.rowGeneration &&
		watermark.RowEpoch == t.rowEpoch
}

func (t *SchemaTable) readKitDBSearchWatermark(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
) (kitDBSearchWatermark, bool, error) {
	encoded, err := manager.ReadCheckpoint(ctx, indexKey, schema)
	if err != nil {
		return kitDBSearchWatermark{}, false, err
	}
	if len(encoded) == 0 {
		return kitDBSearchWatermark{}, false, nil
	}
	watermark, err := decodeKitDBSearchWatermark(encoded)
	return watermark, err == nil, err
}

func (t *SchemaTable) writeKitDBSearchWatermark(
	ctx context.Context,
	manager *search.Manager,
	indexKey string,
	schema search.Schema,
	watermark kitDBSearchWatermark,
) error {
	encoded, err := encodeKitDBSearchWatermark(watermark)
	if err != nil {
		return err
	}
	return manager.WriteCheckpoint(ctx, indexKey, schema, encoded)
}

func (t *SchemaTable) kitDBSearchHistoryPinName() string {
	return "search/" + t.definition.ID
}

func kitDBSearchCursorSignature(cursor kitdbengine.HistoryCursor) string {
	return fmt.Sprintf("%s:%d:%08x", cursor.DatabaseID, cursor.Transaction, cursor.Checksum)
}
