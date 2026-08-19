package javascript

import "testing"

func TestExpressionValidatorAllowsOnlyStaticAuthoredServiceCommands(t *testing.T) {
	t.Parallel()
	accepted := []string{
		`$app.announce.say(message)`,
		`$app.announce.polite(message); $app.announce.assertive(errorMessage); $app.announce.clear()`,
		`$app.appearance.set("dark"); $app.appearance.toggle(); $app.appearance.system()`,
		`$app.clipboard.writeText(code)`,
		`$app.cookie.set("mode", mode); $app.cookie.remove("mode")`,
		`$app.fullscreen.request(); $app.fullscreen.exit()`,
		`$app.navigation.back(); $app.navigation.forward(); $app.navigation.reload()`,
		`$app.progress.start("load"); $app.progress.update("load", loaded, total); $app.progress.finish("load", "loaded")`,
		`$app.share.open({title: title, url: url})`,
		`$app.storage.set("theme", theme); $app.storage.remove("theme")`,
		`$dialog.open()`,
	}
	for _, source := range accepted {
		calls, err := expressionServiceCalls(source, "action")
		if err != nil {
			t.Errorf("expressionServiceCalls(%q) = %v", source, err)
			continue
		}
		if source == `$dialog.open()` && len(calls) != 0 {
			t.Errorf("ordinary component method produced service calls: %#v", calls)
		}
	}

	rejected := []string{
		`$other.clipboard.writeText(code)`,
		`$app.clipboard`,
		`pass($app.clipboard)`,
		`pass($app.clipboard.writeText)`,
		`$app["clipboard"].writeText(code)`,
		`$app.clipboard["writeText"](code)`,
		`$app.clipboard.writeText(code).then(done)`,
		`result = $app.clipboard.writeText(code)`,
		`ready && $app.clipboard.writeText(code)`,
		`($app.clipboard.writeText(code))`,
		`$app?.clipboard.writeText(code)`,
		`$app.clipboard?.writeText(code)`,
		`$app.clipboard.writeText?.(code)`,
		`$app.clipboard.readText()`,
		`$app.cookie.get("mode")`,
		`$app.fullscreen.active()`,
		`$app.share.canShare(payload)`,
		`$app.storage.get("theme")`,
		`$app.storage.clear()`,
		`$app.loader.visible`,
		`copy = $app.loader`,
		`pass($app.loader.value)`,
		`$app["loader"].visible`,
		`$app.network.snapshot()`,
		`$app.progress.snapshot()`,
		`$app.progress.subscribe(listener)`,
		`$app.request.get("/api/profile")`,
		`$app.clipboard.writeText($other.storage.get("theme"))`,
	}
	for _, source := range rejected {
		if _, err := expressionServiceCalls(source, "action"); err == nil {
			t.Errorf("expressionServiceCalls(%q) unexpectedly succeeded", source)
		}
	}

	if _, err := expressionServiceCalls(`$app.clipboard.writeText(code)`, "binding"); err == nil {
		t.Fatal("service command unexpectedly succeeded in a binding")
	}
}

func TestExpressionValidatorAllowsOnlyExactAppLoaderBindingLeaves(t *testing.T) {
	t.Parallel()
	accepted := []string{
		`$app.loader.visible`,
		`$app.loader.value`,
		`!$app.loader.visible`,
		`$app.loader.value === null ? "12%" : $app.loader.value + "%"`,
	}
	for _, source := range accepted {
		calls, err := expressionServiceCalls(source, "binding")
		if err != nil {
			t.Errorf("expressionServiceCalls(%q) = %v", source, err)
			continue
		}
		if len(calls) == 0 {
			t.Errorf("expressionServiceCalls(%q) lost the versioned loader reference", source)
		}
		for _, call := range calls {
			if !call.Loader || call.Alias != "$app" || call.Service != "progress" {
				t.Errorf("expressionServiceCalls(%q) call = %#v", source, call)
			}
		}
	}

	rejected := []string{
		`$app`, `$app.loader`, `$app.loader.phase`, `$other.loader.visible`,
		`$app["loader"].visible`, `$app.loader["visible"]`, `$app?.loader.visible`,
		`$app.loader?.visible`, `$app.loader.visible.toString()`, `($app.loader.visible)`,
		`pass($app.loader.visible)`, `[$app.loader.value]`, `{value: $app.loader.value}`,
		`user?.name === "Kit" && $app.loader.visible`, `count++ || $app.loader.visible`,
	}
	for _, source := range rejected {
		if _, err := expressionServiceCalls(source, "binding"); err == nil {
			t.Errorf("expressionServiceCalls(%q) unexpectedly succeeded", source)
		}
	}
}
