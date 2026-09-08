;(function (global, kit) {
"use strict";

// KitJS managed component: capability-lab@1.0.0
// Discovery is deliberately separate from permission and test outcome. A
// native manifest grant proves neither an OS permission nor a successful run.
var DEFINITIONS = Object.freeze([
  Object.freeze({ id: "announce", label: "Announcements", api: "announce.say", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "appearance", label: "Appearance", api: "appearance.toggle", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "cookie", label: "Cookies", api: "cookie.get", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "fullscreen", label: "Fullscreen", api: "fullscreen.request", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "navigation", label: "Navigation", api: "navigation.reload", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "progress", label: "Progress", api: "progress.snapshot", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "request", label: "HTTP request", api: "request.get", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "storage", label: "Local storage", api: "storage.get", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "lifecycle", label: "App lifecycle", api: "lifecycle.snapshot", group: "Web UI", capability: null, fallback: true }),
  Object.freeze({ id: "network", label: "Network status", api: "network.status", group: "Hybrid", capability: "network.status", fallback: true }),
  Object.freeze({ id: "clipboard", label: "Clipboard", api: "clipboard.writeText", group: "Hybrid", capability: "clipboard.writeText", fallback: true }),
  Object.freeze({ id: "share", label: "Share sheet", api: "share.open", group: "Hybrid", capability: "share.open", fallback: true }),
  Object.freeze({ id: "wake-lock", label: "Giữ màn hình sáng", api: "wakeLock.request", group: "Hybrid", capability: "screen.keepAwake", fallback: true }),
  Object.freeze({ id: "notifications", label: "Local notification", api: "notifications.show", group: "Hybrid", capability: "notifications.show", fallback: true }),
  Object.freeze({ id: "deep-links", label: "Deep links", api: "deepLinks.snapshot", group: "Native", capability: "deepLinks.receive", fallback: false }),
  Object.freeze({ id: "device", label: "Device info", api: "device.info", group: "Native", capability: "device.info", fallback: false }),
  Object.freeze({ id: "vibration", label: "Vibration", api: "device.vibrate", group: "Native", capability: "device.vibrate", fallback: false }),
  Object.freeze({ id: "secure-storage", label: "Secure storage", api: "secureStorage.get", group: "Native", capability: "secureStorage.get", fallback: false }),
  Object.freeze({ id: "files", label: "Blob / FileRef", api: "files.importBlob", group: "Native", capability: "files.import", fallback: false }),
  Object.freeze({ id: "file-picker", label: "File picker", api: "files.pick", group: "Native", capability: "files.import", fallback: false }),
  Object.freeze({ id: "file-share", label: "File share", api: "files.share", group: "Native", capability: "files.share", fallback: false }),
  Object.freeze({ id: "file-export", label: "File export", api: "files.export", group: "Native", capability: "files.export", fallback: false }),
  Object.freeze({ id: "media", label: "Image library", api: "media.pickImage", group: "Native", capability: "files.import", fallback: false }),
  Object.freeze({ id: "camera", label: "Camera capture", api: "camera.capture", group: "Native", capability: "camera.capture", fallback: false }),
  Object.freeze({ id: "qr", label: "QR scanner", api: "qr.scan", group: "Native", capability: "qr.scan", fallback: false }),
  Object.freeze({ id: "shell", label: "External URL", api: "shell.open", group: "Native", capability: "shell.open", fallback: false })
]);
var wakeLocks = new WeakMap();

function method(path) {
  var parts = path.split(".");
  var namespace = kit && kit[parts[0]];
  return namespace && typeof namespace[parts[1]] === "function" ? namespace[parts[1]] : null;
}

function browserAvailable(definition) {
  if (!definition.fallback || !method(definition.api)) return false;
  if (definition.id === "clipboard") {
    try {
      return Boolean(global.navigator && global.navigator.clipboard &&
        typeof global.navigator.clipboard.writeText === "function");
    } catch (_) { return false; }
  }
  if (definition.id === "share") {
    try {
      return Boolean(global.navigator && typeof global.navigator.share === "function") ||
        Boolean(global.navigator && global.navigator.clipboard &&
          typeof global.navigator.clipboard.writeText === "function");
    } catch (_) { return false; }
  }
  if (definition.id === "fullscreen") {
    try {
      return Boolean(global.document && global.document.documentElement &&
        typeof global.document.documentElement.requestFullscreen === "function");
    } catch (_) { return false; }
  }
  if (definition.id === "wake-lock") {
    try {
      return Boolean(global.navigator && global.navigator.wakeLock &&
        typeof global.navigator.wakeLock.request === "function");
    } catch (_) { return false; }
  }
  if (definition.id === "notifications") {
    try {
      return typeof global.Notification === "function";
    } catch (_) { return false; }
  }
  return true;
}

function forgetWakeLock(scope) {
  var sentinel = wakeLocks.get(scope);
  wakeLocks.delete(scope);
  scope.wakeLockActive = false;
  return sentinel;
}

function releaseWakeLock(scope, recordResult) {
  var sentinel = forgetWakeLock(scope);
  if (!sentinel || typeof sentinel.release !== "function") return Promise.resolve(false);
  return Promise.resolve().then(function () {
    return sentinel.release();
  }).then(function () {
    if (recordResult && scope.active) {
      scope.record("wake-lock", "pass", "not-required", "released");
      scope.statusText = "Đã trả screen wake lock.";
    }
    return true;
  }, function (error) {
    if (recordResult && scope.active) {
      scope.recordError("wake-lock", error);
      scope.statusText = "Không trả được screen wake lock · " + errorCode(error) + ".";
    }
    return false;
  });
}

function entry(definition, provider, support, permission, result, detail) {
  return Object.freeze({
    id: definition.id,
    label: definition.label,
    api: definition.api,
    group: definition.group,
    capability: definition.capability || "Không cần native grant",
    provider: provider,
    support: support,
    permission: permission,
    result: result,
    detail: detail || ""
  });
}

function initialEntries() {
  return Object.freeze(DEFINITIONS.map(function (definition) {
    return entry(definition, "checking", "checking", "unknown", "not-run", "");
  }));
}

function errorCode(error) {
  var code = "FAILED";
  try {
    if (error && typeof error.code === "string") code = error.code;
  } catch (_) { /* Component output never includes raw platform errors. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "UNSUPPORTED" || code === "TIMEOUT" || code === "OVERLOADED" ||
    code === "TOO_LARGE") return code;
  return "FAILED";
}

kit.component("capability-lab", {
  active: true,
  checking: true,
  copying: false,
  wakeLockBusy: false,
  wakeLockActive: false,
  notificationBusy: false,
  notificationPermission: "unknown",
  lifecycleState: "active",
  lifecycleHistory: "Lịch sử: đang chờ tín hiệu",
  deepLinkPath: "Chưa nhận deep link",
  availableCount: 0,
  nativeCount: 0,
  unavailableCount: 0,
  summaryText: "Đang kiểm tra graph và native host…",
  statusText: "Discovery không mở camera, picker hay permission prompt.",
  items: initialEntries(),

  init: function (context) {
    var scope = this;
    var stopLifecycle = null;
    var stopDeepLinks = null;
    var lifecycleStates = [];
    var subscribeLifecycle = method("lifecycle.subscribe");
    if (subscribeLifecycle) {
      stopLifecycle = subscribeLifecycle.call(kit.lifecycle, function (snapshot) {
        if (!scope.active) return;
        scope.lifecycleState = snapshot.state;
        if (lifecycleStates[lifecycleStates.length - 1] !== snapshot.state) {
          lifecycleStates.push(snapshot.state);
          if (lifecycleStates.length > 8) lifecycleStates.shift();
          scope.lifecycleHistory = "Lịch sử: " + lifecycleStates.join(" → ");
        }
        scope.record("lifecycle", "pass", "not-required", snapshot.state);
      });
    }
    var subscribeDeepLinks = method("deepLinks.subscribe");
    if (subscribeDeepLinks) {
      stopDeepLinks = subscribeDeepLinks.call(kit.deepLinks, function (snapshot) {
        if (!scope.active) return;
        scope.deepLinkPath = snapshot.path;
        scope.record("deep-links", "pass", "granted", "received");
        scope.statusText = "Đã nhận deep link nội bộ đã chuẩn hóa: " + snapshot.path;
      });
    }
    context.listen(global.document, "visibilitychange", function () {
      if (global.document.visibilityState === "hidden") forgetWakeLock(scope);
    });
    context.cleanup(function () {
      scope.active = false;
      if (stopLifecycle) stopLifecycle();
      if (stopDeepLinks) stopDeepLinks();
      stopLifecycle = null;
      stopDeepLinks = null;
      lifecycleStates = [];
      releaseWakeLock(scope, false);
    });
    return this.refresh();
  },

  checkDeepLink: function () {
    var snapshot = method("deepLinks.snapshot");
    if (!snapshot) {
      this.record("deep-links", "skipped", "not-applicable", "UNAVAILABLE");
      this.statusText = "Deep Link service không có trong graph.";
      return Promise.resolve(false);
    }
    var scope = this;
    return Promise.resolve().then(function () {
      return snapshot.call(kit.deepLinks);
    }).then(function (value) {
      if (!scope.active) return false;
      if (!value) {
        scope.deepLinkPath = "Chưa nhận deep link";
        scope.record("deep-links", "not-run", "unknown", "waiting");
        scope.statusText = "Chưa có deep link. Hãy mở URL scheme của app rồi kiểm tra lại.";
        return false;
      }
      scope.deepLinkPath = value.path;
      scope.record("deep-links", "pass", "granted", "received");
      scope.statusText = "Snapshot deep link hiện tại: " + value.path;
      return true;
    }, function (error) {
      if (scope.active) {
        scope.recordError("deep-links", error);
        scope.statusText = "Không đọc được deep link · " + errorCode(error) + ".";
      }
      return false;
    });
  },

  refresh: function () {
    if (this.checking && this.items !== null && this.availableCount + this.unavailableCount > 0) {
      return Promise.resolve(false);
    }
    var scope = this;
    var supports = method("capabilities.supports");
    this.checking = true;
    this.statusText = "Đang đọc API graph và capability grant…";
    var checks = DEFINITIONS.map(function (definition) {
      if (!definition.capability || !supports) return Promise.resolve(false);
      return Promise.resolve().then(function () {
        return supports.call(kit.capabilities, definition.capability);
      }).then(function (value) { return value === true; }, function () { return false; });
    });
    return Promise.all(checks).then(function (nativeSupport) {
      if (!scope.active) return false;
      var available = 0;
      var nativeCount = 0;
      var unavailable = 0;
      var next = DEFINITIONS.map(function (definition, index) {
        var installed = Boolean(method(definition.api));
        var provider = "none";
        if (installed && nativeSupport[index]) provider = "native";
        else if (installed && browserAvailable(definition)) provider = "web";
        var support = provider === "none" ? "unavailable" : "available";
        if (support === "available") available++;
        else unavailable++;
        if (provider === "native") nativeCount++;
        var permission = provider === "native" ? "unknown" :
          (provider === "web" ? "not-required" : "not-applicable");
        var old = scope.items[index];
        return entry(definition, provider, support, permission,
          old && old.result ? old.result : "not-run", old && old.detail ? old.detail : "");
      });
      scope.items = Object.freeze(next);
      scope.availableCount = available;
      scope.nativeCount = nativeCount;
      scope.unavailableCount = unavailable;
      scope.summaryText = available + "/" + DEFINITIONS.length + " service sẵn sàng · " +
        nativeCount + " qua native host";
      scope.statusText = "Đã làm mới. Permission vẫn là unknown cho tới khi người dùng chạy phép thử tương ứng.";
      return true;
    }, function () {
      if (!scope.active) return false;
      scope.summaryText = "Không hoàn tất được capability discovery.";
      scope.statusText = "Discovery thất bại nhưng không mở rộng quyền hay tự chạy side effect.";
      return false;
    }).then(function (value) {
      if (scope.active) scope.checking = false;
      return value;
    });
  },

  record: function (id, outcome, permission, detail) {
    if (typeof id !== "string" ||
      ["not-run", "running", "pass", "cancelled", "failed", "skipped"].indexOf(outcome) < 0 ||
      ["unknown", "prompt", "granted", "denied", "restricted", "not-required", "not-applicable"].indexOf(permission) < 0) {
      throw new TypeError("Invalid capability result state");
    }
    if (detail !== undefined && (typeof detail !== "string" || detail.length > 512)) {
      throw new TypeError("Capability result detail must be at most 512 characters");
    }
    var found = false;
    var next = this.items.map(function (current, index) {
      if (current.id !== id) return current;
      found = true;
      return entry(DEFINITIONS[index], current.provider, current.support, permission, outcome, detail || "");
    });
    if (!found) throw new TypeError("Unknown capability case");
    this.items = Object.freeze(next);
    return true;
  },

  recordError: function (id, error) {
    var code = errorCode(error);
    var outcome = code === "CANCELLED" ? "cancelled" : "failed";
    var permission = code === "DENIED" ? "denied" : "unknown";
    return this.record(id, outcome, permission, code);
  },

  toggleWakeLock: function () {
    if (this.wakeLockBusy) return Promise.resolve(false);
    var sentinel = wakeLocks.get(this);
    if (sentinel && sentinel.released !== true) {
      var releasingScope = this;
      this.wakeLockBusy = true;
      return releaseWakeLock(this, true).then(function (value) {
        if (releasingScope.active) releasingScope.wakeLockBusy = false;
        return value;
      });
    }
    forgetWakeLock(this);
    var request = method("wakeLock.request");
    if (!request) {
      this.record("wake-lock", "skipped", "not-applicable", "UNAVAILABLE");
      this.statusText = "Wake Lock service không có trong graph.";
      return Promise.resolve(false);
    }
    var scope = this;
    this.wakeLockBusy = true;
    this.record("wake-lock", "running", "not-required", "requesting");
    return Promise.resolve().then(function () {
      return request.call(kit.wakeLock, "screen");
    }).then(function (acquired) {
      if (!acquired || typeof acquired.release !== "function") {
        throw new Error("Invalid wake lock sentinel");
      }
      if (!scope.active) {
        return Promise.resolve(acquired.release()).then(function () { return false; });
      }
      wakeLocks.set(scope, acquired);
      scope.wakeLockActive = true;
      scope.record("wake-lock", "pass", "not-required", "active");
      scope.statusText = "Screen wake lock đang hoạt động. Rời trang hoặc đưa app xuống nền sẽ tự trả lock.";
      return true;
    }, function (error) {
      if (scope.active) {
        scope.recordError("wake-lock", error);
        scope.statusText = "Không giữ được màn hình sáng · " + errorCode(error) + ".";
      }
      return false;
    }).then(function (value) {
      if (scope.active) scope.wakeLockBusy = false;
      return value;
    });
  },

  requestNotificationPermission: function () {
    if (this.notificationBusy) return Promise.resolve(false);
    var request = method("notifications.requestPermission");
    if (!request) {
      this.record("notifications", "skipped", "not-applicable", "UNAVAILABLE");
      this.statusText = "Notification service không có trong graph.";
      return Promise.resolve(false);
    }
    var scope = this;
    this.notificationBusy = true;
    this.record("notifications", "running", "prompt", "requesting");
    return Promise.resolve().then(function () {
      return request.call(kit.notifications);
    }).then(function (permission) {
      if (!scope.active) return false;
      scope.notificationPermission = permission;
      if (permission === "granted") {
        scope.record("notifications", "pass", "granted", "permission-granted");
        scope.statusText = "Đã cấp quyền local notification.";
        return true;
      }
      scope.record("notifications", "failed", permission, "permission-" + permission);
      scope.statusText = permission === "denied" ?
        "Quyền notification đã bị từ chối." : "Hệ điều hành chưa cấp quyền notification.";
      return false;
    }, function (error) {
      if (scope.active) {
        scope.recordError("notifications", error);
        scope.statusText = "Không xin được quyền notification · " + errorCode(error) + ".";
      }
      return false;
    }).then(function (value) {
      if (scope.active) scope.notificationBusy = false;
      return value;
    });
  },

  showNotification: function () {
    if (this.notificationBusy) return Promise.resolve(false);
    var permission = method("notifications.permission");
    var show = method("notifications.show");
    if (!permission || !show) {
      this.record("notifications", "skipped", "not-applicable", "UNAVAILABLE");
      this.statusText = "Notification service không có trong graph.";
      return Promise.resolve(false);
    }
    var scope = this;
    this.notificationBusy = true;
    this.record("notifications", "running", "unknown", "showing");
    return Promise.resolve().then(function () {
      return permission.call(kit.notifications);
    }).then(function (state) {
      if (!scope.active) return false;
      scope.notificationPermission = state;
      if (state !== "granted") {
        scope.record("notifications", "failed", state, "permission-" + state);
        scope.statusText = "Hãy cấp quyền notification trước khi gửi phép thử.";
        return false;
      }
      return show.call(kit.notifications, {
        title: "Kitwork Capability Lab",
        body: "Local notification đã đi qua cùng contract trên nền tảng này.",
        tag: "capability-lab.local"
      }).then(function (shown) {
        if (!scope.active) return false;
        if (shown !== true) throw new Error("Invalid notification result");
        scope.record("notifications", "pass", "granted", "shown");
        scope.statusText = "Đã gửi local notification thử nghiệm.";
        return true;
      });
    }).catch(function (error) {
      if (scope.active) {
        scope.recordError("notifications", error);
        scope.statusText = "Không gửi được notification · " + errorCode(error) + ".";
      }
      return false;
    }).then(function (value) {
      if (scope.active) scope.notificationBusy = false;
      return value;
    });
  },

  reportJSON: function () {
    return JSON.stringify({
      schema: "kitwork.capability-lab.v1",
      available: this.availableCount,
      native: this.nativeCount,
      unavailable: this.unavailableCount,
      items: this.items
    }, null, 2);
  },

  copyReport: function () {
    if (this.copying) return Promise.resolve(false);
    var writeText = method("clipboard.writeText");
    if (!writeText) {
      this.statusText = "Clipboard service không có trong graph.";
      return Promise.resolve(false);
    }
    var scope = this;
    this.copying = true;
    return Promise.resolve().then(function () {
      return writeText.call(kit.clipboard, scope.reportJSON());
    }).then(function () {
      if (scope.active) scope.statusText = "Đã sao chép report JSON; không có raw path, handle hay platform error.";
      return true;
    }, function (error) {
      if (scope.active) scope.statusText = "Không sao chép được report · " + errorCode(error) + ".";
      return false;
    }).then(function (value) {
      if (scope.active) scope.copying = false;
      return value;
    });
  }
});
})(globalThis, kit);
