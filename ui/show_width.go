package ui

import (
	"fmt"
	"strings"
)

// A show from data is the hidden attribute (reboot.css: display none, !important in the first layer).
// Beside it, a width that shows the element — show="$files.selected == 'page'" medium:show="true" —
// means: below medium the data decides, from medium it is always shown (a tab's panel on a phone, side
// by side with the others on a wider screen: docs/components/tabs.md). reboot.css gives back the
// display the element's own classes or tag give it (revert-layer): a frame's direction (flex-row,
// flex-column, grid; a button, a badge, a link holding elements), the clamp of lines, a text kept whole
// (wrap="false"), an image, an icon. An element with none of these — a text, a link of words, a field —
// has nothing to give back: the browser's own [hidden] keeps it hidden at every width, so the width
// written would do nothing, and the author hears so.

// shownByWidth warns of a width that shows an element whose show is live when, as compiled, it has no
// display of its own: the width does nothing there.
func (c *compiler) shownByWidth(n *Node, p *attrPlan, o *elemOp) {
	if p.showExpr == nil || ownDisplay(n, o) {
		return
	}
	for _, a := range n.Attrs {
		if strings.HasSuffix(a.Name, ":show") && strings.TrimSpace(a.Value) == "true" {
			c.warn(n.File, a.Line, a.Column, fmt.Sprintf(`%s="true" bên cạnh show từ dữ liệu không hiện lại một <%s>: thuộc tính hidden của show giấu nó ở mọi độ rộng, vì nó không có cách hiện của riêng nó để trả lại (khung, image, icon, badge, button thì có); đặt show và %s lên một <box> bọc nó`, a.Name, n.Tag, a.Name))
			return
		}
	}
}

// ownDisplay reports whether an element as compiled has a display of its own for the width's
// revert-layer to give back: its tag's (reboot.css, the icons' CSS) or one of its classes'.
func ownDisplay(n *Node, o *elemOp) bool {
	switch o.tag {
	case "img", "picture", "video", "canvas", "svg":
		return true
	}
	if n.Tag == "icon" {
		return true
	}
	for _, class := range o.staticClass {
		switch base := class[strings.LastIndex(class, ":")+1:]; {
		case base == "flex-row", base == "flex-column", base == "grid", strings.HasPrefix(base, "lines-"),
			base == "wrap-false" && n.Tag == "text": // inline-block on a text only (on a frame it is flex-wrap)
			return true
		}
	}
	return false
}
