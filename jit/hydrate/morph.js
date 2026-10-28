// DOM morph module. Keeps navigation optional while sharing the kernel lifecycle contract.
(function (window) {
  "use strict";
  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.module || kitwork.has("morph")) return;
  var cleanupTree = kitwork.internal.cleanupTree;
  // Nodes the client made for itself (Drive's transition stylesheet): the
  // fetched HTML never has them, so a swap must neither match them nor remove them. Remembered here,
  // in memory, and not by an attribute: data-kitwork-generated also marks what the SERVER generated,
  // which a fetched page does carry and a swap must replace like anything else. Drive adds to it.
  var clientOwned = kitwork.internal.clientOwned || (kitwork.internal.clientOwned = new WeakSet());
  // What makes an element the same component on both sides of a swap (see morph, below).
  var IDENTITY = ["data-kitwork-component", "data-kitwork-scope"];

  // ---- morph: kernel primitive — make an existing DOM node match a new one, preserving
  // focus, cursor, scroll and input state (nodes are PATCHED, never recreated).
  function morph(fromNode, toNode) {
    if (fromNode.nodeType !== toNode.nodeType) {
      cleanupTree(fromNode);
      fromNode.replaceWith(toNode.cloneNode(true));
      return;
    }
    if (fromNode.nodeType === 3) {
      if (fromNode.nodeValue !== toNode.nodeValue) fromNode.nodeValue = toNode.nodeValue;
      return;
    }
    if (fromNode.nodeType === 1) {
      // A different tag is a different element — and so is a different component or scope literal
      // on the same tag. Patched in place, a tabs host that became a toast host kept the tabs'
      // scope, listeners and alias: kitwork.components.toast and $note answered as the tabs (02/10).
      // The same component on both pages is kept, state and all, which is what Drive is for.
      if (fromNode.tagName !== toNode.tagName || IDENTITY.some(function (name) {
        return fromNode.getAttribute(name) !== toNode.getAttribute(name);
      })) {
        cleanupTree(fromNode);
        fromNode.replaceWith(toNode.cloneNode(true));
        return;
      }
      var fromAttrs = fromNode.attributes, toAttrs = toNode.attributes, i;
      for (i = fromAttrs.length - 1; i >= 0; i--) {
        if (!toNode.hasAttribute(fromAttrs[i].name)) fromNode.removeAttribute(fromAttrs[i].name);
      }
      for (i = 0; i < toAttrs.length; i++) {
        if (fromNode.getAttribute(toAttrs[i].name) !== toAttrs[i].value) fromNode.setAttribute(toAttrs[i].name, toAttrs[i].value);
      }
      if (fromNode.tagName === "INPUT" || fromNode.tagName === "TEXTAREA") {
        if (fromNode.value !== toNode.value) fromNode.value = toNode.value;
        if (toNode.hasAttribute("checked") !== fromNode.checked) fromNode.checked = toNode.checked;
      } else if (fromNode.tagName === "SELECT") {
        if (fromNode.value !== toNode.value) fromNode.value = toNode.value;
      }
      // Client-owned nodes (clientOwned, above) are CLIENT truth: the fetched HTML never contains
      // them, so they are invisible to matching and exempt from removal. Without this, an app rooted
      // at <html> loses them on the first swap (the overlay died with the morph, so it "sometimes
      // shows, sometimes doesn't"). (Until 10/10/2026 they carried data-kitwork-ui; that went.)
      function kernelUI(n) { return clientOwned.has(n); }
      var fromChildren = Array.prototype.slice.call(fromNode.childNodes).filter(function (n) { return !kernelUI(n); });
      var toChildren = Array.prototype.slice.call(toNode.childNodes);

      // A row's identity is its data-kitwork-key, the marker a live for writes on each row it makes
      // (data-kit-key until 26/10/2026). Plain data-key is never read: a name any other library on
      // the page might also be using.
      function getKey(n) {
        return n.nodeType === 1 ? n.getAttribute("data-kitwork-key") : null;
      }

      var fromKeys = {};
      for (var j = 0; j < fromChildren.length; j++) {
        var key = getKey(fromChildren[j]);
        if (key) fromKeys[key] = fromChildren[j];
      }

      var activeIndex = 0;
      for (var j = 0; j < toChildren.length; j++) {
        var tChild = toChildren[j];
        var tKey = getKey(tChild);
        var fChild = null;

        if (tKey && fromKeys[tKey]) {
          fChild = fromKeys[tKey];
          if (fromNode.childNodes[activeIndex] !== fChild) {
            fromNode.insertBefore(fChild, fromNode.childNodes[activeIndex]);
          }
          var idx = fromChildren.indexOf(fChild);
          if (idx >= 0) fromChildren.splice(idx, 1);
        } else {
          for (var k = 0; k < fromChildren.length; k++) {
            var cand = fromChildren[k];
            if (!getKey(cand) && cand.nodeType === tChild.nodeType && (cand.nodeType !== 1 || cand.tagName === tChild.tagName)) {
              fChild = cand;
              if (fromNode.childNodes[activeIndex] !== fChild) {
                fromNode.insertBefore(fChild, fromNode.childNodes[activeIndex]);
              }
              fromChildren.splice(k, 1);
              break;
            }
          }
        }

        if (fChild) {
          morph(fChild, tChild);
        } else {
          var newCloned = tChild.cloneNode(true);
          fromNode.insertBefore(newCloned, fromNode.childNodes[activeIndex]);
        }
        activeIndex++;
      }

      // Tail cleanup: drop leftovers the new page doesn't have — but never a kernel overlay.
      for (var r = fromNode.childNodes.length - 1; r >= activeIndex; r--) {
        var leftover = fromNode.childNodes[r];
        if (!kernelUI(leftover)) {
          cleanupTree(leftover);
          leftover.remove();
        }
      }
    }
  }

  kitwork.morph = morph;
  kitwork.module("morph", morph);
})(window);