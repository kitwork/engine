package javascript

import "embed"

// embeddedSources is the canonical browser-source catalog. Every embedded file
// is also a valid classic browser script after its ordered core/dependencies
// and may be served separately. The Go composer only selects and concatenates
// these files; it does not transform JavaScript or require a Node-based build
// step.
//
// Keep this list explicit. Files that have not passed the current KitJS
// contract stay in the tree as migration input, but cannot accidentally enter
// a generated browser artifact.
//
//go:embed core/global.js core/expression.js core/component.js core/dom.js core/lifecycle.js core/morph.js core/drive.js core/boot.js
//go:embed service/announce/1.0.0.js service/clipboard/1.0.0.js service/cookie/1.0.0.js service/fullscreen/1.0.0.js
//go:embed service/navigation/1.0.0.js service/network/1.0.0.js service/request/1.0.0.js service/share/1.0.0.js service/storage/1.0.0.js
//go:embed component/accordion/1.0.0.js component/announce/1.0.0.js component/clipboard/1.0.0.js component/combobox/1.0.0.js
//go:embed component/command-palette/1.0.0.js component/counter/1.0.0.js component/dialog/1.0.0.js component/drawer/1.0.0.js component/dropdown/1.0.0.js
//go:embed component/menu/1.0.0.js component/popover/1.0.0.js component/progress-bar/1.0.0.js component/tabs/1.0.0.js
//go:embed component/theme/1.0.0.js component/toast/1.0.0.js component/tooltip/1.0.0.js
var embeddedSources embed.FS
