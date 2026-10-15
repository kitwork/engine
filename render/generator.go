package render

import (
	"strings"

	"github.com/kitwork/engine/version"
)

// generatorTag names the engine in the page it finished, the way a page built by WordPress or Hugo
// says so: what built this page, and which release.
const generatorTag = `<meta name="generator" content="Kitwork ` + version.Number + `">`

// withGenerator puts the generator tag in the document's <head>. A page whose author wrote a
// generator of their own keeps it, and a fragment with no <head> is left alone.
func withGenerator(out string) string {
	end := strings.Index(out, "</head>")
	if end < 0 {
		return out
	}
	head := strings.ToLower(out[:end])
	if strings.Contains(head, `name="generator"`) || strings.Contains(head, `name=generator`) || strings.Contains(head, `name='generator'`) {
		return out
	}
	return out[:end] + generatorTag + out[end:]
}
