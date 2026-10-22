package kitdb

import (
	"container/list"
	"sync"
)

const defaultMainPageCacheBytes int64 = 16 << 20

type mainRow struct {
	key     []byte
	value   []byte
	deleted bool
}

// Page-local positions avoid retaining two slice headers per decoded row.
// Offsets are validated against the owned data buffer before publication.
type pageRow struct {
	keyStart   int
	valueStart int
	end        int
	deleted    bool
}

type rowPage struct {
	block  int
	data   []byte
	rows   []pageRow
	weight int64
}

func (page *rowPage) key(index int) []byte {
	row := page.rows[index]
	return page.data[row.keyStart:row.valueStart]
}

func (page *rowPage) row(index int) mainRow {
	row := page.rows[index]
	return mainRow{
		key: page.data[row.keyStart:row.valueStart], value: page.data[row.valueStart:row.end],
		deleted: row.deleted,
	}
}

type rowPageCache struct {
	mu      sync.Mutex
	maximum int64
	used    int64
	recent  list.List
	byBlock map[int]*list.Element
}

type rowPageEntry struct {
	page *rowPage
}

func newRowPageCache(maximum int64) *rowPageCache {
	if maximum < 0 {
		maximum = 0
	}
	return &rowPageCache{maximum: maximum, byBlock: make(map[int]*list.Element)}
}

func (cache *rowPageCache) enabled() bool {
	return cache != nil && cache.maximum > 0
}

func (cache *rowPageCache) get(block int) (*rowPage, bool) {
	if cache == nil || cache.maximum == 0 {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, found := cache.byBlock[block]
	if !found {
		return nil, false
	}
	cache.recent.MoveToFront(element)
	return element.Value.(rowPageEntry).page, true
}

func (cache *rowPageCache) addOrGet(page *rowPage) *rowPage {
	if cache == nil || page == nil || cache.maximum == 0 || page.weight > cache.maximum {
		return page
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element, found := cache.byBlock[page.block]; found {
		cache.recent.MoveToFront(element)
		return element.Value.(rowPageEntry).page
	}
	for cache.used+page.weight > cache.maximum {
		oldest := cache.recent.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(rowPageEntry)
		delete(cache.byBlock, entry.page.block)
		cache.used -= entry.page.weight
		cache.recent.Remove(oldest)
	}
	element := cache.recent.PushFront(rowPageEntry{page: page})
	cache.byBlock[page.block] = element
	cache.used += page.weight
	return page
}

func (cache *rowPageCache) usage() (bytes int64, pages int) {
	if cache == nil {
		return 0, 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.used, len(cache.byBlock)
}
