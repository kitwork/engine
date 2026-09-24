package work

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

// Drive turns Kitwork Drive on for the whole site: a same-origin link or GET form is fetched and
// morphed in place instead of reloading the page.
//
//	router.drive()        // on
//	router.drive(true)
//	router.drive(false)   // the default
//
// It is site-wide and off by default, because Drive changes what a visitor sees happen when they
// click. Before this existed it switched itself on wherever a root marker appeared — so the decision
// rode on an attribute authors wrote for an unrelated reason, and a page could gain SPA navigation
// simply by being marked as a Kitwork page.
//
// A single link steps back out with `data-kit-reload="true"`.
func (f *FolderRouter) Drive(args ...value.Value) (*FolderRouter, error) {
	if len(args) > 1 {
		return f, fmt.Errorf("router.drive expects zero arguments or one boolean")
	}
	enabled := true
	if len(args) == 1 {
		if args[0].K != value.Bool {
			return f, fmt.Errorf("router.drive expects a boolean; got %s", args[0].K)
		}
		enabled = args[0].N != 0
	}
	if f == nil || f.tenant == nil {
		return f, nil
	}
	if !f.tenant.presentation().SetDrive(enabled) {
		return f, fmt.Errorf("router.drive cannot change a frozen site generation")
	}
	return f, nil
}
