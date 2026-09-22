package work

// router.themes(config): the site's appearance modes, derived from the tokens router.css()
// declared. router.css({ theme }) says WHAT the tokens are (Tailwind's word); router.themes() says
// HOW they re-value under a mode (Kitwork's word) — one block of variables under one selector, so
// markup never changes and no utility is minted. A hand-written `<token>-<mode>` rung in css()
// still wins over the derived value; declaring a theme also turns the anti-flash pre-paint on.
//
//	router.themes({ dark: true })
//	router.themes({ dark: true, prepaint: false })   // opt OUT of the anti-flash pre-paint
//
// `true` asks for the engine's formula; `false` leaves the mode out. A mode the engine cannot
// derive yet, or a value of another shape, is a named warning at boot — never a silent nothing.
//
// The anti-flash pre-paint is the theme's own job, not a switch: the visitor's saved light/dark
// choice lives in the browser, so the server-rendered page cannot know it, and a tiny synchronous
// script at the top of <head> (jit/theme) applies it before the first frame. It is on for EVERY
// page as soon as the site declares a dark it switches by class — router.css({ darkMode: ["class"]
// }) or a derived mode here — and falls back to a per-page toggle scan for a site that declared
// neither. `prepaint` is therefore opt-out only: `false` never injects; `true` is a named warning
// that points at darkMode (there is no opt-in — declare the dark you have). It replaces
// router.jittheme(true|false) (22/09), kept as a deprecated alias.

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
	prepaint := ""
	for mode, v := range cfg.Map() {
		if mode == "prepaint" {
			switch {
			case v.K == value.Bool && v.N == 0:
				prepaint = "off"
			case v.K == value.Bool:
				fmt.Println("[router] router.themes(): prepaint is the theme's own job — on for every page once router.css({ darkMode: ['class'] }) or a mode here is declared; only prepaint: false is a setting — ignored")
			default:
				fmt.Println("[router] router.themes(): prepaint takes false (opt out) — ignored")
			}
			continue
		}
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
	if prepaint != "" {
		f.tenant.presentation().SetThemeMode(prepaint)
	}
	return f
}
