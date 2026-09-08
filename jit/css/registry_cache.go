package css

import (
	"regexp"
	"sync"
)

// The registry is a fixed list compiled once, not per class. ResolveCore walks every pattern for
// every class it is asked about, so recompiling ~170 regexes on each call made the JIT pass cost
// grow with the size of the registry rather than with the page. Compiled lazily and cached by index
// so a pattern nobody reaches is never compiled at all.
var (
	registryCache   = make([]*regexp.Regexp, len(Registry))
	registryCacheMu sync.RWMutex
)

func registryRe(i int, pattern string) *regexp.Regexp {
	registryCacheMu.RLock()
	re := registryCache[i]
	registryCacheMu.RUnlock()
	if re != nil {
		return re
	}
	re = regexp.MustCompile(pattern)
	registryCacheMu.Lock()
	registryCache[i] = re
	registryCacheMu.Unlock()
	return re
}
