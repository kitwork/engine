package work

import (
	"encoding/json"

	colcap "github.com/kitwork/engine/capabilities/collection"
	"github.com/kitwork/engine/value"
)

type CollectionManager = colcap.Manager
type CollectionHandle = colcap.Handle

func (w *KitWork) Collection() *CollectionManager {
	val := w.Capability("collection")
	if mgr, ok := val.V.(*colcap.Manager); ok {
		return mgr
	}
	return colcap.NewManager(w.tenant)
}

func collectionValue(input any) value.Value {
	body, _ := json.Marshal(input)
	var decoded any
	_ = json.Unmarshal(body, &decoded)
	return value.New(decoded)
}
