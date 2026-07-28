// Optional remote component loader. Off by default; core component registration stays in kernel.
(function (window, document) {
  "use strict";
  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.module || kitwork.has("componentLoader")) return;
  kitwork.cdnComponents = kitwork.cdnComponents || "";

  // ---- IndexedDB persistence for dynamic CDN components (opt-in via data-kit-persist="true") ----
  var DB_NAME = "kitwork";
  var STORE_NAME = "blueprints";
  var dbPromise = null;
  function getDB() {
    if (dbPromise) return dbPromise;
    dbPromise = new Promise(function (resolve, reject) {
      if (!window.indexedDB) { reject(new Error("IndexedDB not supported")); return; }
      var req = indexedDB.open(DB_NAME, 1);
      req.onupgradeneeded = function (e) {
        var db = e.target.result;
        if (!db.objectStoreNames.contains(STORE_NAME)) {
          db.createObjectStore(STORE_NAME);
        }
      };
      req.onsuccess = function (e) { resolve(e.target.result); };
      req.onerror = function (e) { reject(e.target.error); };
    });
    return dbPromise;
  }
  function dbGet(key) {
    return getDB().then(function (db) {
      return new Promise(function (resolve, reject) {
        var tx = db.transaction(STORE_NAME, "readonly");
        var store = tx.objectStore(STORE_NAME);
        var req = store.get(key);
        req.onsuccess = function (e) { resolve(e.target.result); };
        req.onerror = function (e) { reject(e.target.error); };
      });
    });
  }
  function dbSet(key, val) {
    return getDB().then(function (db) {
      return new Promise(function (resolve, reject) {
        var tx = db.transaction(STORE_NAME, "readwrite");
        var store = tx.objectStore(STORE_NAME);
        var req = store.put(val, key);
        req.onsuccess = function () { resolve(); };
        req.onerror = function (e) { reject(e.target.error); };
      });
    });
  }

  function injectScriptCode(code, cname) {
    try {
      var blob = new Blob([code], { type: "application/javascript" });
      var url = URL.createObjectURL(blob);
      var s = document.createElement("script");
      s.src = url;
      s.async = true;
      s.onload = function () {
        URL.revokeObjectURL(url);
        kitwork.render();
      };
      s.onerror = function () {
        URL.revokeObjectURL(url);
        console.error("kitjs: failed to execute stored component '" + cname + "'");
      };
      document.head.appendChild(s);
    } catch (e) {
      console.error("kitjs: failed to create blob for '" + cname + "'", e);
    }
  }

  function fetchCodeAndStore(url, cname) {
    fetch(url)
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.text();
      })
      .then(function (code) {
        dbSet(cname, code).catch(function (err) {
          console.warn("kitjs: failed to store component '" + cname + "' to IndexedDB", err);
        });
        injectScriptCode(code, cname);
      })
      .catch(function (err) {
        console.error("kitjs: failed to fetch component '" + cname + "' from " + url, err);
      });
  }

  var loadingComponents = {};
  function loadComponentFromCDN(cname) {
    var base = kitwork.cdnComponents;
    if (!base || loadingComponents[cname]) return;          // opt-in only — off by default
    // Validate name[@version] so a hostile attribute can never build an unexpected URL.
    var m = /^([a-z][a-z0-9-]*)(?:@([v0-9.]+))?$/.exec(cname);
    if (!m) { console.error("kitjs: invalid component name '" + cname + "'"); return; }
    loadingComponents[cname] = true;
    var url = base.replace(/\/+$/, "") + "/" + m[1] + "/" + (m[2] || m[1]) + ".js";

    if (kitwork.useIndexed && window.indexedDB) {
      dbGet(cname).then(function (cachedCode) {
        if (cachedCode) {
          injectScriptCode(cachedCode, cname);
        } else {
          fetchCodeAndStore(url, cname);
        }
      }).catch(function () {
        fetchCodeAndStore(url, cname);
      });
    } else {
      var s = document.createElement("script");
      s.src = url;
      s.async = true;
      s.onload = function () { kitwork.render(); };
      s.onerror = function () { console.error("kitjs: failed to load component '" + cname + "' from " + url); };
      document.head.appendChild(s);
    }
  }

  kitwork.module("componentLoader", { load: loadComponentFromCDN });
})(window, document);
