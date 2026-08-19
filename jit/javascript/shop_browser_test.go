package javascript

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	shopAsideTagRE    = regexp.MustCompile(`(?is)<aside\b[^>]*>`)
	shopDataKitAttrRE = regexp.MustCompile(`(?i)\b(data-kit-[a-z0-9:_-]+)\s*=\s*"([^"]*)"`)
)

const (
	shopHistoricalArtifactName   = "hydrate.kit.0.5.0.3d9a3213c5e76157c829866ad214b23aabee235ed997d07a54dcf797b7eee3be.js"
	shopHistoricalArtifactSHA256 = "3d9a3213c5e76157c829866ad214b23aabee235ed997d07a54dcf797b7eee3be"
)

func TestShopExampleContract(t *testing.T) {
	artifactSource := readVanillaFile(t, "examples", "shop", shopHistoricalArtifactName)
	if digest := ContentHash(artifactSource); digest != shopHistoricalArtifactSHA256 {
		t.Fatalf("historical shop artifact SHA-256 = %s, want %s", digest, shopHistoricalArtifactSHA256)
	}
	wantScript := "./" + shopHistoricalArtifactName
	routes := []string{"products.html", "cart.html", "checkout.html"}
	var sharedKitAttributes map[string]string
	for _, route := range routes {
		route := route
		t.Run(strings.TrimSuffix(route, ".html"), func(t *testing.T) {
			source := string(readVanillaFile(t, "examples", "shop", route))
			lower := strings.ToLower(source)
			matches := externalScriptRE.FindAllStringSubmatch(source, -1)
			if len(matches) != 1 {
				t.Fatalf("%s external script count = %d, want one sealed Hydrate artifact", route, len(matches))
			}
			got := matches[0][1]
			if got == "" {
				got = matches[0][2]
			}
			if got != wantScript {
				t.Fatalf("%s external script = %q, want %q", route, got, wantScript)
			}
			for _, forbidden := range []string{
				"data-kit-app", "data-kit-hydrate", "data-kit-plan", "data-kitwork-plan",
				"__kitjs_plan__", "<style", "<dialog", "<details",
			} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("%s contains forbidden standalone demo construct %q", route, forbidden)
				}
			}
			for _, shared := range []string{
				`id="shop-cart"`, `data-kit-component="shop-cart"`, `data-kit-version="1.0.0"`, `data-kit-as="$cart"`,
				`id="shop-cart-count"`, `id="shop-cart-total"`,
				`id="shop-nav-products"`, `id="shop-nav-cart"`, `id="shop-nav-checkout"`,
			} {
				if !strings.Contains(source, shared) {
					t.Fatalf("%s lost shared retained-shell contract %s", route, shared)
				}
			}

			host := ""
			for _, aside := range shopAsideTagRE.FindAllString(source, -1) {
				if strings.Contains(aside, `id="shop-cart"`) {
					host = aside
					break
				}
			}
			if host == "" {
				t.Fatalf("%s does not retain shop-cart on an aside host", route)
			}
			attributes := make(map[string]string)
			for _, match := range shopDataKitAttrRE.FindAllStringSubmatch(host, -1) {
				attributes[strings.ToLower(match[1])] = match[2]
			}
			if sharedKitAttributes == nil {
				sharedKitAttributes = attributes
			} else if !sameStringMap(sharedKitAttributes, attributes) {
				t.Fatalf("%s shop-cart data-kit host attributes = %#v, want %#v", route, attributes, sharedKitAttributes)
			}
		})
	}

	shopJS := string(readVanillaFile(t, "examples", "shop", "shop.js"))
	for _, definition := range []string{
		`kit.component("shop-cart"`,
		`kit.component("shop-products"`,
		`kit.component("shop-checkout"`,
		`kit.component("shop-dialog"`,
	} {
		if !strings.Contains(shopJS, definition) {
			t.Fatalf("shop.js does not register the closed component graph member %s", definition)
		}
	}
	for _, route := range routes {
		source := string(readVanillaFile(t, "examples", "shop", route))
		for _, match := range regexp.MustCompile(`(?is)<[^>]+\bdata-kit-component\s*=\s*"[^"]+"[^>]*>`).FindAllString(source, -1) {
			if !strings.Contains(match, `data-kit-version="1.0.0"`) {
				t.Fatalf("%s has an unpinned shop component host: %s", route, match)
			}
		}
	}
}

func sameStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func TestBrowserHydrateShop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Hydrate shop browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifactSource := readVanillaFile(t, "examples", "shop", shopHistoricalArtifactName)
	assetPath := "/examples/shop/" + shopHistoricalArtifactName
	var artifactRequests atomic.Int64
	products := readVanillaFile(t, "examples", "shop", "products.html")
	pages := map[string][]byte{
		"/examples/shop/products.html": products,
		"/examples/shop/cart.html":     readVanillaFile(t, "examples", "shop", "cart.html"),
		"/examples/shop/checkout.html": readVanillaFile(t, "examples", "shop", "checkout.html"),
	}
	initialProducts := injectBrowserAssertions(t, products, shopBrowserAssertions(assetPath))
	directPages := make(map[string][]byte, len(pages))
	for path, source := range pages {
		directPages[path] = injectBrowserAssertions(t, source, shopDirectLoadAssertions(assetPath))
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			artifactRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifactSource)
			return
		}

		page, ok := pages[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		if request.URL.Query().Get("direct") == "1" {
			_, _ = response.Write(directPages[request.URL.Path])
			return
		}
		if request.URL.Path == "/examples/shop/products.html" && request.Header.Get("X-KitJS-Drive") == "" {
			_, _ = response.Write(initialProducts)
			return
		}
		_, _ = response.Write(page)
	}))
	defer server.Close()

	runShopBrowser(t, browser, server.URL+"/examples/shop/products.html")
	if got := artifactRequests.Load(); got != 1 {
		t.Fatalf("shop navigation requested its sealed artifact %d times, want only the initial load", got)
	}

	for _, route := range []string{"products.html", "cart.html", "checkout.html"} {
		route := route
		t.Run("direct-"+strings.TrimSuffix(route, ".html"), func(t *testing.T) {
			runVanillaBrowser(t, browser, server.URL+"/examples/shop/"+route+"?direct=1")
		})
	}
	if got := artifactRequests.Load(); got != 4 {
		t.Fatalf("sealed artifact request count after three isolated direct loads = %d, want 4 total", got)
	}
}

func runShopBrowser(t *testing.T, browser, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--metrics-recording-only",
		"--no-first-run",
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + t.TempDir(),
		"--virtual-time-budget=30000",
		"--dump-dom",
		target,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("headless shop proof timed out: %v\n%s", ctx.Err(), boundedVanillaOutput(output))
	}
	if runErr != nil {
		t.Fatalf("headless shop proof failed to run: %v\n%s", runErr, boundedVanillaOutput(output))
	}
	t.Fatalf("headless shop proof did not pass\n%s", boundedVanillaOutput(output))
}

func shopDirectLoadAssertions(assetPath string) string {
	return fmt.Sprintf(`__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;

  await waitFor(function () {
    return globalThis.kit && document.getElementById("shop-cart-count").textContent.trim() === "0";
  }, "direct-loaded shop route did not boot");
  assert(Object.keys(kit).join(",") === "version,component",
    "direct-loaded shop route expanded the public API: " + Object.keys(kit).join(","));
  assert(!document.querySelector("[data-kit-app],[data-kit-hydrate],[data-kit-plan],[data-kitwork-plan]"),
    "direct-loaded shop route required an activation marker");
  var scripts = Array.prototype.slice.call(document.querySelectorAll("script[src]"));
  assert(scripts.length === 1, "direct-loaded shop route did not use exactly one sealed artifact");
  assert(new URL(scripts[0].src, location.href).pathname === %q,
    "direct-loaded shop route did not load the canonical sealed artifact");

  if (location.pathname.slice(-13) === "products.html") {
    assert(document.querySelectorAll("[data-shop-add]").length >= 2,
      "direct-loaded products route did not activate products");
  } else if (location.pathname.slice(-9) === "cart.html") {
    await waitFor(function () { return !!document.getElementById("shop-cart-empty"); },
      "direct-loaded empty cart did not render");
  } else {
    var submit = document.getElementById("shop-checkout-submit");
    assert(submit && submit.disabled, "direct-loaded checkout was not initially disabled");
  }
});`, assetPath)
}

func shopBrowserAssertions(assetPath string) string {
	return fmt.Sprintf(`__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var root = document.documentElement;

  function publicContract(label) {
    assert(document.documentElement === root, label + " replaced documentElement");
    assert(Object.keys(kit).join(",") === "version,component",
      label + " expanded the public API: " + Object.keys(kit).join(","));
    assert(!document.querySelector("[data-kit-app],[data-kit-hydrate],[data-kit-plan],[data-kitwork-plan]"),
      label + " introduced an activation marker");
  }

  await waitFor(function () {
    return globalThis.kit && document.getElementById("shop-cart-count").textContent.trim() === "0";
  }, "shop did not boot");
  publicContract("initial products route");
  var scripts = Array.prototype.slice.call(document.querySelectorAll("script[src]"));
  assert(scripts.length === 1, "shop route did not use exactly one sealed artifact");
  assert(new URL(scripts[0].src, location.href).pathname === %q,
    "shop route did not load the canonical sealed artifact");

  var cart = document.getElementById("shop-cart");
  var cartCount = document.getElementById("shop-cart-count");
  var cartTotal = document.getElementById("shop-cart-total");
  assert(cart && cartCount && cartTotal, "retained cart shell is incomplete");

  var realFetch = globalThis.fetch.bind(globalThis);
  var fetches = [];
  globalThis.fetch = function (source, options) {
    fetches.push(new URL(String(source), location.href).pathname);
    return realFetch(source, options);
  };

  function retained(label) {
    publicContract(label);
    assert(document.getElementById("shop-cart") === cart, label + " replaced the cart component host");
    assert(document.getElementById("shop-cart-count") === cartCount, label + " replaced cart count output");
    assert(document.getElementById("shop-cart-total") === cartTotal, label + " replaced cart total output");
  }

  async function navigate(selector, pathname, ready, label) {
    var before = fetches.length;
    var link = document.querySelector(selector);
    assert(link, label + " link is missing");
    link.click();
    await waitFor(function () {
      return location.pathname === pathname && (!ready || ready());
    }, label + " did not commit");
    assert(fetches.length === before + 1,
      label + " started " + (fetches.length - before) + " fetches instead of one");
    assert(fetches[before] === pathname, label + " fetched " + fetches[before]);
    retained(label);
  }

  async function pop(direction, pathname, ready, label) {
    var before = fetches.length;
    history[direction]();
    await waitFor(function () {
      return location.pathname === pathname && (!ready || ready());
    }, label + " did not commit");
    assert(fetches.length === before + 1,
      label + " started " + (fetches.length - before) + " fetches instead of one");
    retained(label);
  }

  function enter(id, value) {
    var input = document.getElementById(id);
    assert(input, id + " is missing");
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }

  var add = document.querySelectorAll("[data-shop-add]");
  assert(add.length >= 2, "products route needs at least two products");
  var detachedAdd = add[0];
  add[0].click();
  await waitFor(function () {
    return cartCount.textContent.trim() === "1" && cartTotal.textContent.trim() === "$24.00";
  }, "first product did not update cart total");
  add[0].click();
  add[1].click();
  await waitFor(function () { return cartCount.textContent.trim() === "3"; },
    "product quantities did not update the retained cart");
  var productsTotal = cartTotal.textContent.trim();
  assert(productsTotal !== "$0.00" && productsTotal !== "$24.00",
    "second product did not contribute to total");

  await navigate("#shop-nav-cart", "/examples/shop/cart.html", function () {
    return document.querySelectorAll("[data-shop-cart-row]").length === 2;
  }, "products to cart");
  assert(cartCount.textContent.trim() === "3", "cart route lost product quantities");
  assert(cartTotal.textContent.trim() === productsTotal, "cart route lost product total");
  detachedAdd.click();
  await nextTurn();
  assert(cartCount.textContent.trim() === "3", "removed products route retained a live action");

  var beforeRemove = cartTotal.textContent.trim();
  document.querySelector("[data-shop-remove]").click();
  await waitFor(function () {
    return document.querySelectorAll("[data-shop-cart-row]").length === 1 &&
      cartCount.textContent.trim() === "1";
  }, "cart remove did not remove one keyed product");
  assert(cartTotal.textContent.trim() !== beforeRemove && cartTotal.textContent.trim() !== "$0.00",
    "cart remove did not recompute total");
  var checkoutTotal = cartTotal.textContent.trim();

  await navigate("#shop-cart-checkout", "/examples/shop/checkout.html", function () {
    return !!document.getElementById("shop-checkout-submit");
  }, "cart to checkout");
  assert(cartCount.textContent.trim() === "1" && cartTotal.textContent.trim() === checkoutTotal,
    "checkout route lost retained cart state");

  var submit = document.getElementById("shop-checkout-submit");
  assert(submit.disabled, "empty checkout form was not disabled");
  enter("shop-checkout-name", "Ada Lovelace");
  enter("shop-checkout-email", "ada@example.com");
  assert(submit.disabled, "partially complete checkout became ready");
  enter("shop-checkout-address", "12 St James's Square");
  await waitFor(function () { return !submit.disabled; }, "complete checkout did not become ready");

  await pop("back", "/examples/shop/cart.html", function () {
    return document.querySelectorAll("[data-shop-cart-row]").length === 1;
  }, "history back to cart");
  assert(cartCount.textContent.trim() === "1" && cartTotal.textContent.trim() === checkoutTotal,
    "history back lost cart state");
  await pop("forward", "/examples/shop/checkout.html", function () {
    return !!document.getElementById("shop-checkout-submit");
  }, "history forward to checkout");
  assert(document.getElementById("shop-checkout-submit").disabled,
    "new checkout boundary retained disposed form state");

  enter("shop-checkout-name", "Ada Lovelace");
  enter("shop-checkout-email", "ada@example.com");
  enter("shop-checkout-address", "12 St James's Square");
  submit = document.getElementById("shop-checkout-submit");
  await waitFor(function () { return !submit.disabled; }, "restored checkout did not become ready");
  var checkout = document.getElementById("shop-checkout");
  var dialog = document.getElementById("shop-confirm-dialog");
  var dialogPanel = dialog && dialog.querySelector("[role='dialog']");
  var shell = document.getElementById("shop-shell");
  assert(dialog && !checkout.contains(dialog), "confirmation dialog is not an external sibling component");
  assert(shell, "shop shell is missing");
  document.getElementById("shop-checkout-form").requestSubmit();
  await waitFor(function () {
    return !dialog.hidden && shell.hasAttribute("inert") &&
      document.activeElement === document.getElementById("shop-dialog-cancel");
  }, "external dialog did not open and focus safely from checkout");
  assert(dialogPanel && dialogPanel.getAttribute("aria-modal") === "true" &&
    dialogPanel.getAttribute("aria-labelledby") === "shop-dialog-title",
    "custom confirmation dialog lost dialog semantics");
  assert(shell.hasAttribute("inert") && shell.getAttribute("aria-hidden") === "true",
    "open dialog did not make the background inert");
  var confirm = document.getElementById("shop-dialog-confirm");
  var cancel = document.getElementById("shop-dialog-cancel");
  assert(confirm && cancel, "custom dialog controls are incomplete");
  assert(document.activeElement === cancel, "open dialog did not focus its safe first control");
  var shiftTabAllowed = cancel.dispatchEvent(new KeyboardEvent("keydown", {
    key: "Tab", shiftKey: true, bubbles: true, cancelable: true
  }));
  assert(shiftTabAllowed === false && document.activeElement === confirm,
    "Shift+Tab did not stay inside the custom dialog");
  var tabAllowed = confirm.dispatchEvent(new KeyboardEvent("keydown", {
    key: "Tab", bubbles: true, cancelable: true
  }));
  assert(tabAllowed === false && document.activeElement === cancel,
    "Tab did not stay inside the custom dialog");
  cancel.dispatchEvent(new KeyboardEvent("keydown", {
    key: "Escape", bubbles: true, cancelable: true
  }));
  await waitFor(function () { return dialog.hidden; }, "Escape did not close the custom dialog");
  assert(!shell.hasAttribute("inert") && !shell.hasAttribute("aria-hidden"),
    "closing the dialog did not restore the background");
  assert(document.activeElement === submit, "closing the dialog did not restore trigger focus");
  assert(!document.getElementById("shop-order-success") && cartCount.textContent.trim() === "1",
    "cancelled dialog invoked its external callback");

  // A close in the same task must invalidate the pending focus/inert work from
  // open instead of allowing its timer to re-lock a hidden dialog.
  document.getElementById("shop-checkout-form").requestSubmit();
  dialog.dispatchEvent(new KeyboardEvent("keydown", {
    key: "Escape", bubbles: true, cancelable: true
  }));
  await waitFor(function () {
    return dialog.hidden && !shell.hasAttribute("inert") && !shell.hasAttribute("aria-hidden");
  }, "synchronous open-close let stale dialog work re-lock the page");

  document.getElementById("shop-checkout-form").requestSubmit();
  await waitFor(function () {
    return !dialog.hidden && shell.hasAttribute("inert") &&
      document.activeElement === document.getElementById("shop-dialog-cancel");
  }, "external dialog did not reopen safely");
  document.getElementById("shop-dialog-confirm").click();
  await waitFor(function () {
    return !!document.getElementById("shop-order-success") &&
      cartCount.textContent.trim() === "0" && cartTotal.textContent.trim() === "$0.00";
  }, "external dialog callback did not complete the order and clear cart");
  assert(document.getElementById("shop-order-name").textContent.trim() === "Ada Lovelace",
    "external dialog callback did not retain the submitted customer name");
  assert(dialog.hidden, "confirmation dialog stayed open after confirmation");

  // Fifty Back/Forward round trips are one hundred Drive navigations without
  // tripping the browser's anti-abuse throttle for rapid pushState calls.
  // Every pop must produce exactly one fetch, which catches duplicate Drive
  // listeners without depending on implementation-private counters.
  for (var cycle = 0; cycle < 50; cycle++) {
    await pop("back", "/examples/shop/cart.html", function () {
      return !!document.getElementById("shop-cart-empty");
    }, "repeat cart " + cycle);
    await pop("forward", "/examples/shop/checkout.html", function () {
      return !!document.getElementById("shop-checkout-submit");
    }, "repeat checkout " + cycle);
  }
  retained("after one hundred repeated navigations");
});`, assetPath)
}
