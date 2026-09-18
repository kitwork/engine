package javascript

import (
	"embed"
	"fmt"
)

// embeddedDeliveryPackages contains only packages authored for the flattened
// KitJS Build API. Legacy component/runtime sources are intentionally absent:
// generation preparation must fail closed instead of combining two contracts.
//
//go:embed service/announce/1.0.0.js service/appearance/1.0.0.js service/camera/1.0.0.js service/capabilities/1.0.0.js service/clipboard/1.0.0.js service/cookie/1.0.0.js service/device/1.0.0.js service/fullscreen/1.0.0.js
//go:embed service/files/1.0.0.js service/files/1.1.0.js service/files/1.2.0.js service/files/1.3.0.js service/navigation/1.0.0.js service/network/1.0.0.js service/notifications/1.0.0.js service/notifications/1.1.0.js service/progress/1.0.0.js service/request/1.0.0.js
//go:embed service/deepLinks/1.0.0.js service/deepLinks/1.1.0.js service/lifecycle/1.0.0.js service/media/1.0.0.js service/qr/1.0.0.js service/secureStorage/1.0.0.js service/share/1.0.0.js service/shell/1.0.0.js service/storage/1.0.0.js service/studioDatabase/1.0.0.js service/studioDatabase/1.1.0.js service/studioSqlite/1.0.0.js service/studioState/1.0.0.js service/wakeLock/1.0.0.js service/window/1.0.0.js
//go:embed component/progress-bar/1.1.0.js component/progress-bar/1.2.0.js component/progress-bar/2.0.0.js
//go:embed component/accordion/1.0.0.js component/dialog/1.0.0.js component/tabs/1.0.0.js component/dropdown/1.0.0.js
//go:embed component/dialog/2.0.0.js component/tabs/2.0.0.js component/dropdown/2.0.0.js component/carousel/2.0.0.js
//go:embed component/alert/1.0.0.js component/switch/1.0.0.js component/pagination/1.0.0.js component/carousel/1.0.0.js
//go:embed component/popover/1.0.0.js component/tooltip/1.0.0.js component/toast/1.0.0.js component/drawer/1.0.0.js component/shortcut/1.0.0.js
//go:embed component/desktop-titlebar/1.0.0.js component/desktop-titlebar/1.1.0.js
//go:embed component/capability-lab/1.0.0.js component/capability-lab/1.1.0.js component/capability-lab/1.2.0.js
//go:embed component/app/1.0.0.js component/app/1.1.0.js component/app/1.2.0.js component/app/1.3.0.js component/app/1.4.0.js component/app/1.5.0.js component/app/1.6.0.js component/app/1.7.0.js component/app/1.8.0.js component/app/1.9.0.js component/app/1.10.0.js component/app/1.11.0.js component/app/1.12.0.js component/app/1.13.0.js component/app/1.14.0.js component/theme/2.0.0.js component/theme/3.0.0.js
//go:embed component/stepper/1.0.0.js component/slider/1.0.0.js component/rating/1.0.0.js component/tags/1.0.0.js component/terminal/1.0.0.js component/dropzone/1.0.0.js component/command/1.0.0.js component/context-menu/1.0.0.js component/data-table/1.0.0.js component/tree/1.0.0.js
//go:embed component/collapse/1.0.0.js component/combobox/1.0.0.js component/otp/1.0.0.js component/copy/1.0.0.js
//go:embed component/rotator/1.0.0.js component/split/1.0.0.js
//go:embed component/calendar/1.0.0.js component/chart/1.0.0.js component/editor/1.0.0.js component/code-editor/1.0.0.js component/kanban/1.0.0.js
//go:embed component/scrolled/1.0.0.js
//go:embed service/network/1.1.0.js service/files/1.4.0.js service/camera/1.1.0.js service/media/1.1.0.js component/app/1.15.0.js component/capability-lab/1.3.0.js
//go:embed service/biometric/1.0.0.js service/geolocation/1.0.0.js service/nfc/1.0.0.js service/notifications/1.2.0.js component/app/1.16.0.js component/capability-lab/1.4.0.js
//go:embed service/studioSqlite/1.1.0.js component/app/1.17.0.js
var embeddedDeliveryPackages embed.FS

// ComponentSource returns the authored JavaScript of a managed component version
// — the exact `kit.component(name, …)` file the engine seals and ships. It exists
// so documentation surfaces can *show* a component instead of hiding it behind a
// versioned host; a version is immutable, so the returned bytes never drift from
// what runs. name and version are validated to a safe alphabet before they touch
// the embedded filesystem so a caller can never walk out of the package.
func ComponentSource(name, version string) ([]byte, error) {
	if !isSafePackageSegment(name) || !isSafePackageSegment(version) {
		return nil, fmt.Errorf("javascript: invalid component identity %q@%q", name, version)
	}
	return embeddedDeliveryPackages.ReadFile("component/" + name + "/" + version + ".js")
}

// isSafePackageSegment allows only the characters used by component names and
// semver versions (letters, digits, dot, dash). It rejects path separators and
// dot-dot so a segment can address exactly one embedded file and nothing else.
func isSafePackageSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for i := 0; i < len(segment); i++ {
		char := segment[i]
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '.' || char == '-':
		default:
			return false
		}
	}
	return true
}
