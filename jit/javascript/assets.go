package javascript

import "embed"

// embeddedDeliveryPackages contains only packages authored for the flattened
// KitJS Build API. Legacy component/runtime sources are intentionally absent:
// generation preparation must fail closed instead of combining two contracts.
//
//go:embed service/announce/1.0.0.js service/appearance/1.0.0.js service/clipboard/1.0.0.js service/cookie/1.0.0.js service/fullscreen/1.0.0.js
//go:embed service/navigation/1.0.0.js service/network/1.0.0.js service/progress/1.0.0.js service/request/1.0.0.js
//go:embed service/share/1.0.0.js service/storage/1.0.0.js
//go:embed component/progress-bar/1.1.0.js component/progress-bar/1.2.0.js component/progress-bar/2.0.0.js
//go:embed component/accordion/1.0.0.js component/dialog/1.0.0.js component/tabs/1.0.0.js component/dropdown/1.0.0.js
//go:embed component/dialog/2.0.0.js component/tabs/2.0.0.js component/dropdown/2.0.0.js
//go:embed component/alert/1.0.0.js component/switch/1.0.0.js component/pagination/1.0.0.js component/carousel/1.0.0.js
//go:embed component/popover/1.0.0.js component/tooltip/1.0.0.js component/toast/1.0.0.js component/drawer/1.0.0.js component/shortcut/1.0.0.js
//go:embed component/app/1.0.0.js component/app/1.1.0.js component/theme/2.0.0.js component/theme/3.0.0.js
var embeddedDeliveryPackages embed.FS
