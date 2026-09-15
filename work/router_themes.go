package work

// router.themes(config): the site's appearance modes, derived from the tokens router.css()
// declared. router.css({ theme }) says WHAT the tokens are (Tailwind's word); router.themes() says
// HOW they re-value under a mode (Kitwork's word) — one block of variables under one selector, so
// markup never changes and no utility is minted. A hand-written `<token>-<mode>` rung in css()
// still wins over the derived value; declaring a theme also turns the anti-flash pre-paint on.
//
//	router.themes({ dark: true })
//
// `true` asks for the engine's formula; `false` leaves the mode out. A mode the engine cannot
// derive yet, or a value of another shape, is a named warning at boot — never a silent nothing.

import (
	"fmt"
	"sort"

	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/value"
)

func (f *FolderRouter) Themes(cfg value.Value) *FolderRouter {
	if !cfg.IsMap() {
		fmt.Println("[router] router.themes() takes an object — router.themes({ dark: true }) — ignored")
		return f
	}
	var modes []string
	for mode, v := range cfg.Map() {
		switch {
		case v.K == value.Bool && v.N == 0:
			continue // { dark: false } — say nothing, derive nothing
		case v.K == value.Bool:
			if !jitcss.CanDeriveTheme(mode) {
				fmt.Printf("[router] router.themes(): %s is not a mode this engine can derive (dark is) — ignored\n", mode)
				continue
			}
			modes = append(modes, mode)
		default:
			fmt.Printf("[router] router.themes(): %s takes true or false in this engine (a written-out theme is not supported yet) — ignored\n", mode)
		}
	}
	sort.Strings(modes)
	f.tenant.presentation().SetThemes(modes)
	return f
}
