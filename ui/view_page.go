package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// readView turns a view.ui, root <view> with <content /> where the route's page goes, into the
// compiler's own name for the frame, "page" (<body>), and keeps what was written for messages. A
// <page> root, or <page /> where the page goes, is a spelling that never stands for the frame and
// says so.
func (c *compiler) readView(root *Node) error {
	switch root.Tag {
	case "view":
	case "page":
		return errAt(root, "gốc của view.ui là <view> (trước 22/10/2026 khung là page.ui với gốc <page>)")
	default:
		return errAt(root, "view.ui phải có gốc <view>, không phải <%s>; trang của route đặt trong content.ui", root.Tag)
	}
	if place := framePlace(root, "page"); place != nil {
		return errAt(place, "chỗ đặt trang trong view.ui là <content />, không phải <page />")
	}
	root.Tag = "page"
	c.hostTags[root] = "view"
	return nil
}

// frameOrPageRoot is the error of a component's or a layout piece's file whose root is <view>, or
// <page> (the frame's tag before 22/10/2026): the frame is the root of view.ui alone.
func frameOrPageRoot(root *Node, file string) error {
	switch root.Tag {
	case "view":
		return errAt(root, "gốc của %s là <view>: <view> là khung của trang, chỉ là gốc của view.ui", file)
	case "page":
		return errAt(root, "gốc của %s là <page>: khung của trang là <view>, chỉ là gốc của view.ui (trước 22/10/2026 viết <page> trong page.ui)", file)
	}
	return nil
}

// writtenAs is how the author writes a tag the compiler renamed as it read the frame: the frame
// "page" is written <view>. For messages that name a tag by its compiled name.
func writtenAs(tag string) string {
	if tag == "page" {
		return "view"
	}
	return tag
}

// framePlace is the first <tag /> under n with no attribute and nothing inside: where a frame puts the
// route's page.
func framePlace(n *Node, tag string) *Node {
	for _, child := range n.Children {
		if child.Tag == tag && len(child.Attrs) == 0 && len(child.SignificantChildren()) == 0 {
			return child
		}
		if found := framePlace(child, tag); found != nil {
			return found
		}
	}
	return nil
}

// OldNames reports a route folder of a Kitwork UI site still holding the frame by its name before
// 22/10/2026, page.ui (now view.ui, root <view>, with <content /> where the page goes), read at boot
// so a site does not lose its frame without a word: from that day the engine looks for view.ui only.
func OldNames(dir string) error {
	info, err := os.Stat(filepath.Join(dir, "page.ui"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}
	return fmt.Errorf("page.ui là tên cũ của khung (trước 22/10/2026): đổi tên thành view.ui và viết gốc <view>; chỗ đặt trang vẫn là <content />")
}
