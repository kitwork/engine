package work

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

// Reload says whether moving between pages loads each page whole. It is the one switch for every
// site, a Kitwork UI site (router.work) and a template site (router.kitwork.js) alike:
//
//	router.reload(false)   // a link fetches the next page and swaps it in place (Kitwork Drive)
//	router.reload(true)    // every link loads the next page whole, as a browser does
//
// The default differs by kind, because each keeps what it already did: a Kitwork UI site does not
// reload (a link swaps only <content />, keeping header, footer and what is open), a template site
// does (it moves without a reload only once it writes router.reload(false)). A site whose third-party
// scripts only work on a full load writes router.reload(true); one link can step out on its own with
// reload="true" (.ui) or data-kit-reload="true" (template). How the change looks is a separate word,
// router.transition. Site-wide for now: the engine does not split it by folder yet.
//
// A site that moves without a reload gets Drive on EVERY page, a page with no directive too: a page
// without it is where the next click would reload and drop what the swap keeps.
func (f *FolderRouter) Reload(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 || args[0].K != value.Bool {
		return f, fmt.Errorf("router.reload expects true or false")
	}
	if f == nil || f.tenant == nil {
		return f, nil
	}
	if !f.tenant.presentation().SetDrive(args[0].N == 0) {
		return f, fmt.Errorf("router.reload cannot change a frozen site generation")
	}
	return f, nil
}

// Drive is gone (11/10/2026): moving between pages without a reload is router.reload(false), the
// one switch for every site. A router that still calls router.drive() keeps booting and keeps its
// chain (the method returns the router), but the call applies nothing, whatever it is given, and the
// boot names it once for the file (removedMethodWarnings), so the site moves to router.reload(false)
// knowingly instead of losing Drive in silence.
func (f *FolderRouter) Drive(args ...value.Value) *FolderRouter {
	f.removed(removedDrive)
	return f
}
