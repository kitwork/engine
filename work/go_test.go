package work

import (
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func TestBackgroundGoConcurrentIsolation(t *testing.T) {
	tenant := &Tenant{
		MaxEnergy: 1000000,
	}
	kw := &KitWork{tenant: tenant}

	var wg sync.WaitGroup
	count := 10

	for i := 0; i < count; i++ {
		wg.Add(1)
		fn := value.NewFunc(func(args ...value.Value) value.Value {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond)
			return value.New("ok")
		})
		kw.Go(fn)
	}

	wg.Wait()
}
