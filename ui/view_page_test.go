package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The frame is view.ui, root <view>, with <content /> where the route's page goes; the route's page is
// content.ui, root <content> (22/10/2026, KITWORK_UI §4). The frame becomes <body>, the page <main>,
// and Drive is told which frame a page has (data-kitwork-layout) by the frame's file.
func TestViewAndContentMakeThePage(t *testing.T) {
	got := mustCompile(t, map[string]string{
		"/site/view.ui":    `<view background="canvas"><header /><content /></view>`,
		"/site/header.ui":  `<header><text>Kitwork</text></header>`,
		"/site/content.ui": `<content gap="4"><text>Xin chào</text></content>`,
	})
	for _, want := range []string{`data-kitwork-layout="view.ui"`, `<header`, `<main`, `Xin chào`, `Kitwork`} {
		if !strings.Contains(got, want) {
			t.Errorf("the page is missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "Kitwork") > strings.Index(got, "Xin chào") {
		t.Errorf("the header must come before the page, where <content /> stands in view.ui:\n%s", got)
	}
}

// What the frame was written before 22/10/2026, and what never stands for it, is an error in the
// author's words with the new spelling. CONTROL: written the new way it compiles (the test above).
func TestOldSpellingOfTheFrameIsAnError(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"view root page", map[string]string{"/site/view.ui": `<page><content /></page>`, "/site/content.ui": `<content>x</content>`},
			"view.ui:1:1: gốc của view.ui là <view> (trước 22/10/2026 khung là page.ui với gốc <page>)"},
		{"view root other", map[string]string{"/site/view.ui": `<box><content /></box>`, "/site/content.ui": `<content>x</content>`},
			"view.ui:1:1: view.ui phải có gốc <view>, không phải <box>; trang của route đặt trong content.ui"},
		{"view place page", map[string]string{"/site/view.ui": `<view><page /></view>`, "/site/content.ui": `<content>x</content>`},
			"view.ui:1:7: chỗ đặt trang trong view.ui là <content />, không phải <page />"},
		{"content root page", map[string]string{"/site/content.ui": `<page>x</page>`},
			"content.ui:1:1: content.ui phải có gốc <content>, không phải <page>"},
	} {
		t.Run(c.name, func(t *testing.T) { mustFailExactly(t, c.files, c.want) })
	}
}

// The writing area of an editor is <textarea> (22/10/2026): the field a form has, made rich by the
// editor around it. A <content> there is the spelling before that day. Outside an editor a <textarea>
// is the plain field it always was. CONTROL: the editor with its <textarea> compiles to a writing area
// (role="textbox") posting under its name.
func TestEditorWritingAreaIsATextarea(t *testing.T) {
	area := `<content><form method="post"><editor><textarea name="body" alt="Nội dung" /></editor></form></content>`
	got := mustCompile(t, map[string]string{"/site/content.ui": area})
	for _, want := range []string{`role="textbox"`, `name="body"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the editor's <textarea> is not its writing area, missing %q:\n%s", want, got)
		}
	}
	mustFailExactly(t, map[string]string{"/site/content.ui": `<content><form method="post"><editor><content name="body" alt="Nội dung" /></editor></form></content>`},
		"content.ui:1:38: vùng viết của editor là <textarea> (trước 22/10/2026 viết <content>)")
	plain := mustCompile(t, map[string]string{"/site/content.ui": `<content><form method="post"><textarea name="note" alt="Ghi chú"></textarea></form></content>`})
	if !strings.Contains(plain, `<textarea`) || strings.Contains(plain, `role="textbox"`) {
		t.Errorf("a <textarea> outside an editor is the plain field:\n%s", plain)
	}
}

// A page.ui left from before 22/10/2026 stops a Kitwork UI site at boot (the route tree asks OldNames of
// every folder): the engine looks for view.ui only, and the site must not lose its frame without a
// word. CONTROL: a folder written the new way passes.
func TestOldNamesAtBoot(t *testing.T) {
	folder := func(files map[string]string) string {
		dir := t.TempDir()
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	want := "page.ui là tên cũ của khung (trước 22/10/2026): đổi tên thành view.ui và viết gốc <view>; chỗ đặt trang vẫn là <content />"
	if err := OldNames(folder(map[string]string{"page.ui": `<page><content /></page>`, "content.ui": `<content>x</content>`})); err == nil || err.Error() != want {
		t.Errorf("OldNames = %v, want %q", err, want)
	}
	if err := OldNames(folder(map[string]string{"view.ui": `<view><content /></view>`, "content.ui": `<content>x</content>`, "notfound.ui": `<content>404</content>`})); err != nil {
		t.Errorf("CONTROL: OldNames = %v, want nil", err)
	}
}
