package work

// design.work (KITWORK_DESIGN.md, 28/10/2026): a folder of a Kitwork UI site declares its look in a file of its
// own, the seven words of kitwork/design, design.colors(…), design.fonts(…)… Each word has exactly the value,
// meaning, merge and inheritance router.<the same word> has in router.work and config.work (KITWORK_ROUTER.md
// mục 4), so the engine runs design.work into the same FolderRouter, before the folder's config.work and its
// router.work (compileUIFolder), and each design.<word> is the folder's call of router.<word>: the same Go method,
// the same check of its value, the same rule of a word whose nearest folder wins (once, across design.work,
// config.work and router.work of the folder). The app's folder may have one too, the parent of every site, heard
// with the app's config.work (appRouterFiles).
//
// The compiler reads the file's spelling (compiler/design.go); the engine refuses again what it cannot follow:
// kitwork().design anywhere but a design.work, a word not of the seven, a call chained. router.colors(…) and its
// six neighbours keep working in router.work and config.work.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// uiDesign is kitwork/design as a design.work reads it: the seven words of the folder, nothing else.
type uiDesign struct {
	folder *FolderRouter
}

// Member answers every word, before anything of the Go type is read: a word of the seven is the folder's call
// of the router's word of that name; a word of the router is the error saying where it belongs, any other the
// error naming the nearest, as the compiler says each.
func (d *uiDesign) Member(name string) (value.Value, bool) {
	return d.folder.designWord(name), true
}

// Design is kitwork().design: the folder's design while its design.work runs, the error that it is only there
// anywhere else (a router.work, a config.work, a handler at request time).
func (tw *TreeKitWork) Design() value.Value {
	folder := tw.node.folder
	if folder == nil || !folder.ui || folder.running != compiler.DesignFile {
		return routerFailure(compiler.DesignImportOutside)
	}
	return value.New(&uiDesign{folder: folder})
}

// designWord is design.<name> of the folder.
func (f *FolderRouter) designWord(name string) value.Value {
	known, ok := compiler.DesignWordOf(name)
	if !ok {
		if _, router := compiler.RouterWordOf(name); router {
			return routerFailure(compiler.DesignRouterWord(name))
		}
		return routerFailure(compiler.DesignUnknownWord(name))
	}
	return value.NewFunc(func(args ...value.Value) value.Value {
		result := f.call(known, args)
		if result.K == value.Invalid {
			failure, _ := value.FailureFrom(result)
			if strings.HasPrefix(failure.Message, "design.") {
				// said of design.work already (once: a call written twice, each place as it is written)
				return result
			}
			return value.InvalidFailure(failure.Code, designSpelling(failure.Message))
		}
		if chained, isChained := result.V.(*routerCallResult); isChained {
			chained.module = "design"
		}
		return result
	})
}

// designSpelling is a message of a router word said of a call of design.work: router.colors is design.colors
// there, as the author wrote it.
func designSpelling(message string) string {
	for _, word := range compiler.DesignWords() {
		message = strings.ReplaceAll(message, "router."+word, "design."+word)
	}
	return message
}

// designCallPlace is a call of a word whose nearest folder wins as a message names it: its file and line, and,
// when the file is a router file, the spelling it is written with there (router.transition).
func designCallPlace(word string, place routerCallPlace) string {
	if place.file == compiler.DesignFile {
		return fmt.Sprintf("%s:%d", place.file, place.line)
	}
	return fmt.Sprintf("router.%s ở %s:%d", word, place.file, place.line)
}

// designRolelessFolders are the folders of an app with no role for a design.work (KITWORK_DESIGN.md mục 1): code
// shared, schedules, data. assets/ is a site's or a branch's (designInAssets).
var designRolelessFolders = []string{".shared", ".schedule", ".database"}

// misplacedDesign is the boot error of a design.work where no folder's look is declared: in .shared/ or
// .schedule/ (any depth: code only, never a folder of a site), at the top of .database/ (the app's data; a file
// deeper there is a file the app stored, never read as code), of the app's folder and of the site's root.
// relativeTo names the place in the error.
func misplacedDesign(relativeTo string, folders ...string) error {
	seen := map[string]bool{}
	for _, folder := range folders {
		if folder == "" || seen[filepath.Clean(folder)] {
			continue
		}
		seen[filepath.Clean(folder)] = true
		for _, name := range designRolelessFolders {
			place := filepath.Join(folder, name)
			if name == ".database" {
				if fileExists(filepath.Join(place, compiler.DesignFile)) {
					return designMisplaced(relativeTo, filepath.Join(place, compiler.DesignFile))
				}
				continue
			}
			if found := findDesign(place); found != "" {
				return designMisplaced(relativeTo, found)
			}
		}
	}
	return nil
}

// designInAssets is the boot error of a design.work in the assets/ of a folder of a site, at any depth: a folder
// served as files, never a folder of routes.
func designInAssets(relativeTo, folder string) error {
	if found := findDesign(filepath.Join(folder, "assets")); found != "" {
		return designMisplaced(relativeTo, found)
	}
	return nil
}

// findDesign is the first design.work under folder, "" when there is none (or no folder).
func findDesign(folder string) string {
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return ""
	}
	found := ""
	_ = filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() && entry.Name() == compiler.DesignFile {
			found = path
			return errFoundDesign
		}
		return nil
	})
	return found
}

var errFoundDesign = errors.New("design.work found")

// designMisplaced is the error of a design.work at file, named from relativeTo.
func designMisplaced(relativeTo, file string) error {
	shown := file
	if relative, err := filepath.Rel(relativeTo, file); err == nil && !strings.HasPrefix(relative, "..") {
		shown = relative
	}
	return fmt.Errorf("%s: design.work không có vai ở đây; design.work đặt ở gốc app, gốc site hay một thư mục nhánh, cạnh config.work (KITWORK_DESIGN.md mục 1)", filepath.ToSlash(shown))
}

// routerFileKind is what a file run into a folder is, as a boot error names it: "design" for a design.work,
// "router" for router.work and config.work.
func routerFileKind(name string) string {
	if filepath.Base(name) == compiler.DesignFile {
		return "design"
	}
	return "router"
}
