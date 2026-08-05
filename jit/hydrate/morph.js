// DOM morph module. Keeps navigation optional while sharing the kernel lifecycle contract.
(function (window) {
  "use strict";
  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || kit.has("morph")) return;
  var cleanupTree = kit.internal.cleanupTree;

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
      if (fromNode.tagName !== toNode.tagName) {
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
      // Kernel-owned overlay nodes (progress bar, announcer, toasts — marked data-kitwork-ui) are
      // CLIENT truth: the fetched HTML never contains them, so they are invisible to matching and
      // exempt from removal. Without this, an app rooted at <html> loses the progress bar on the
      // first swap (the bar died with the morph, so it "sometimes shows, sometimes doesn't").
      function kernelUI(n) { return n.nodeType === 1 && n.hasAttribute("data-kitwork-ui"); }
      var fromChildren = Array.prototype.slice.call(fromNode.childNodes).filter(function (n) { return !kernelUI(n); });
      var toChildren = Array.prototype.slice.call(toNode.childNodes);

      function getKey(n) {
        return n.nodeType === 1 ? (n.getAttribute("data-kitwork-key") || n.getAttribute("data-kit-key") || n.getAttribute("data-key")) : null;
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

  kit.morph = morph;
  kit.module("morph", morph);
})(window);