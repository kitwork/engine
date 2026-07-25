// Kitwork client kernel.
//
// One root:
//   window.kitwork
//
// One registry.
// One set of delegated listeners.
// One observer.
//
// No eval.
// No new Function.
(function () {
  "use strict";

  var kitwork = window.kitwork || {};

  if (kitwork.runtime) {
    return;
  }

  window.kitwork = kitwork;

  kitwork.runtime = {
    name: "kitwork",
    version: "1.0.0",
    engine: "web",
    development: false,

    info: function () {
      return {
        name: this.name,
        version: this.version,
        engine: this.engine,
        development: this.development
      };
    }
  };

  kitwork.storage = {
    prefix: "kitwork:",

    key: function (key) {
      return this.prefix + key;
    },

    get: async function (key, options) {
      options = options || {};

      var value;

      try {
        value = window.localStorage.getItem(this.key(key));
      } catch (_) {
        return options.default;
      }

      if (value === null) {
        return options.default;
      }

      try {
        return JSON.parse(value);
      } catch (_) {
        return value;
      }
    },

    set: async function (key, value) {
      if (value === undefined) {
        throw new TypeError(
          "kitwork.storage.set: value cannot be undefined"
        );
      }

      var encoded = JSON.stringify(value);

      try {
        window.localStorage.setItem(
          this.key(key),
          encoded
        );

        return true;
      } catch (error) {
        throw error;
      }
    },

    remove: async function (key) {
      try {
        window.localStorage.removeItem(
          this.key(key)
        );

        return true;
      } catch (error) {
        throw error;
      }
    },

    clear: async function () {
      var prefix = this.prefix;
      var keys = [];

      try {
        for (
          var index = 0;
          index < window.localStorage.length;
          index++
        ) {
          var key = window.localStorage.key(index);

          if (
            key &&
            key.indexOf(prefix) === 0
          ) {
            keys.push(key);
          }
        }

        for (
          var removeIndex = 0;
          removeIndex < keys.length;
          removeIndex++
        ) {
          window.localStorage.removeItem(
            keys[removeIndex]
          );
        }

        return true;
      } catch (error) {
        throw error;
      }
    },

    keys: async function () {
      var prefix = this.prefix;
      var keys = [];

      try {
        for (
          var index = 0;
          index < window.localStorage.length;
          index++
        ) {
          var key = window.localStorage.key(index);

          if (
            key &&
            key.indexOf(prefix) === 0
          ) {
            keys.push(
              key.slice(prefix.length)
            );
          }
        }

        return keys;
      } catch (error) {
        throw error;
      }
    },

    has: async function (key) {
      try {
        return (
          window.localStorage.getItem(
            this.key(key)
          ) !== null
        );
      } catch (error) {
        throw error;
      }
    },

    namespace: function (name) {
      if (
        typeof name !== "string" ||
        name.trim() === ""
      ) {
        throw new TypeError(
          "kitwork.storage.namespace: name is required"
        );
      }

      var storage = this;
      var namespace = name.trim() + ":";

      return {
        get: function (key, options) {
          return storage.get(
            namespace + key,
            options
          );
        },

        set: function (key, value) {
          return storage.set(
            namespace + key,
            value
          );
        },

        remove: function (key) {
          return storage.remove(
            namespace + key
          );
        },

        has: function (key) {
          return storage.has(
            namespace + key
          );
        },

        keys: async function () {
          var keys = await storage.keys();
          var result = [];

          for (
            var index = 0;
            index < keys.length;
            index++
          ) {
            if (
              keys[index].indexOf(namespace) === 0
            ) {
              result.push(
                keys[index].slice(namespace.length)
              );
            }
          }

          return result;
        },

        clear: async function () {
          var keys = await this.keys();

          for (
            var index = 0;
            index < keys.length;
            index++
          ) {
            await this.remove(keys[index]);
          }

          return true;
        },

        namespace: function (child) {
          return storage.namespace(
            namespace + child
          );
        }
      };
    }
  };

  kitwork.theme = {
    get: function () {
      var saved = kitwork.storage.get("theme");

      if (saved === "dark" || saved === "light") {
        return saved;
      }

      return document.documentElement.classList.contains("dark")
        ? "dark"
        : "light";
    },

    set: function (value) {
      var theme = value === "dark" ? "dark" : "light";

      document.documentElement.classList.toggle(
        "dark",
        theme === "dark"
      );

      kitwork.storage.set("theme", theme);

      return theme;
    },

    toggle: function () {
      return this.set(
        this.get() === "dark" ? "light" : "dark"
      );
    }
  };

  kitwork.window = {
    supported: function () {
      return !!(
        kitwork.bridge &&
        typeof kitwork.bridge.call === "function"
      );
    },

    minimize: function () {
      return this.call("minimize");
    },

    maximize: function () {
      return this.call("maximize");
    },

    close: function () {
      return this.call("close");
    },

    drag: function () {
      return this.call("drag");
    },

    call: function (action) {
      if (!this.supported()) {
        return false;
      }

      return kitwork.bridge.call(
        "window." + action,
        {}
      );
    }
  };

  var DRAG = '[data-kit-drag="true"]';
  var UNDRAG = '[data-kit-drag="false"]';

  document.addEventListener("mousedown", function (e) {
    if (e.button !== 0) return;
    if (!e.target.closest) return;
    if (e.target.closest(UNDRAG)) return;

    if (e.target.closest(DRAG)) {
      kitwork.window.drag();
    }
  });

  document.addEventListener("dblclick", function (e) {
    if (!e.target.closest) return;
    if (e.target.closest(UNDRAG)) return;

    if (e.target.closest(DRAG)) {
      kitwork.window.maximize();
    }
  });

  kitwork.platform = (function () {
    var userAgent =
      window.navigator.userAgent || "";

    var platform =
      window.navigator.platform || "";

    function detectOS() {
      if (/Android/i.test(userAgent)) {
        return "android";
      }

      if (/iPhone|iPad|iPod/i.test(userAgent)) {
        return "ios";
      }

      if (/Win/i.test(platform)) {
        return "windows";
      }

      if (/Mac/i.test(platform)) {
        return "macos";
      }

      if (/Linux/i.test(platform)) {
        return "linux";
      }

      return "unknown";
    }

    function detectMobile() {
      return /Android|iPhone|iPad|iPod|Mobile/i.test(
        userAgent
      );
    }

    var os = detectOS();
    var mobile = detectMobile();

    return {
      name: "web",
      os: os,

      web: true,
      native: false,

      mobile: mobile,
      desktop: !mobile,

      touch:
        "ontouchstart" in window ||
        navigator.maxTouchPoints > 0,

      language:
        navigator.language || "en",

      online:
        navigator.onLine,

      info: function () {
        return {
          name: this.name,
          os: this.os,

          web: this.web,
          native: this.native,

          mobile: this.mobile,
          desktop: this.desktop,

          touch: this.touch,
          language: this.language,
          online: navigator.onLine
        };
      }
    };
  })();

  kitwork.clipboard = {
    write: function (value) {
      var text = value == null
        ? ""
        : String(value);

      if (
        navigator.clipboard &&
        typeof navigator.clipboard.writeText === "function"
      ) {
        return navigator.clipboard
          .writeText(text)
          .then(function () {
            return true;
          });
      }

      return new Promise(function (resolve, reject) {
        var area = document.createElement("textarea");

        area.value = text;
        area.setAttribute("readonly", "");
        area.style.position = "fixed";
        area.style.left = "-9999px";
        area.style.opacity = "0";

        document.body.appendChild(area);
        area.select();

        try {
          var copied = document.execCommand("copy");
          area.remove();

          if (!copied) {
            reject(
              new Error("kitwork.clipboard.write: copy failed")
            );

            return;
          }

          resolve(true);
        } catch (error) {
          area.remove();
          reject(error);
        }
      });
    },

    read: function () {
      if (
        navigator.clipboard &&
        typeof navigator.clipboard.readText === "function"
      ) {
        return navigator.clipboard.readText();
      }

      return Promise.reject(
        new Error(
          "kitwork.clipboard.read: clipboard reading is not supported"
        )
      );
    }
  };


  kitwork.share = {
    supported: function () {
      return typeof navigator.share === "function";
    },

    send: function (options) {
      options = options || {};

      var data = {};

      if (options.title != null) {
        data.title = String(options.title);
      }

      if (options.text != null) {
        data.text = String(options.text);
      }

      if (options.url != null) {
        data.url = String(options.url);
      }

      if (
        options.files &&
        options.files.length
      ) {
        data.files = options.files;
      }

      if (
        data.files &&
        typeof navigator.canShare === "function" &&
        !navigator.canShare({ files: data.files })
      ) {
        return Promise.reject(
          new Error(
            "kitwork.share.send: files are not supported"
          )
        );
      }

      if (!this.supported()) {
        return Promise.reject(
          new Error(
            "kitwork.share.send: not supported"
          )
        );
      }

      return navigator.share(data).then(function () {
        return true;
      });
    }
  };

  kitwork.notification = {
    supported: function () {
      return "Notification" in window;
    },

    permission: function () {
      if (!this.supported()) {
        return "unsupported";
      }

      return Notification.permission;
    },

    request: function () {
      if (!this.supported()) {
        return Promise.resolve("unsupported");
      }

      return Notification.requestPermission();
    },

    show: function (title, options) {
      if (!this.supported()) {
        return Promise.reject(
          new Error(
            "kitwork.notification.show: not supported"
          )
        );
      }

      if (Notification.permission !== "granted") {
        return Promise.reject(
          new Error(
            "kitwork.notification.show: permission not granted"
          )
        );
      }

      var notification = new Notification(
        String(title || ""),
        options || {}
      );

      return Promise.resolve(notification);
    }
  };

  kitwork.network = (function () {
    var listeners = [];

    function online() {
      return navigator.onLine;
    }

    function connection() {
      var value =
        navigator.connection ||
        navigator.mozConnection ||
        navigator.webkitConnection ||
        null;

      if (!value) {
        return null;
      }

      return {
        type: value.type || null,
        effectiveType: value.effectiveType || null,
        downlink: value.downlink || null,
        rtt: value.rtt || null,
        saveData: !!value.saveData
      };
    }

    function info() {
      return {
        online: online(),
        connection: connection()
      };
    }

    function emit() {
      var value = info();

      for (
        var index = 0;
        index < listeners.length;
        index++
      ) {
        try {
          listeners[index](value);
        } catch (_) { }
      }
    }

    window.addEventListener("online", emit);
    window.addEventListener("offline", emit);

    var nativeConnection =
      navigator.connection ||
      navigator.mozConnection ||
      navigator.webkitConnection;

    if (
      nativeConnection &&
      typeof nativeConnection.addEventListener === "function"
    ) {
      nativeConnection.addEventListener(
        "change",
        emit
      );
    }

    return {
      isOnline: online,

      info: info,

      onChange: function (listener) {
        if (typeof listener !== "function") {
          throw new TypeError(
            "kitwork.network.onChange: listener must be a function"
          );
        }

        listeners.push(listener);

        return function () {
          var index = listeners.indexOf(listener);

          if (index >= 0) {
            listeners.splice(index, 1);
          }
        };
      }
    };
  })();

  kitwork.permissions = {
    supported: function (name) {
      if (name === "notification") {
        return "Notification" in window;
      }

      if (name === "geolocation") {
        return "geolocation" in navigator;
      }

      if (name === "camera" || name === "microphone") {
        return !!(
          navigator.mediaDevices &&
          navigator.mediaDevices.getUserMedia
        );
      }

      if (name === "clipboard-read") {
        return !!(
          navigator.clipboard &&
          navigator.clipboard.readText
        );
      }

      if (name === "clipboard-write") {
        return !!(
          navigator.clipboard &&
          navigator.clipboard.writeText
        );
      }

      return !!(
        navigator.permissions &&
        navigator.permissions.query
      );
    },

    query: function (name) {
      if (!this.supported(name)) {
        return Promise.resolve("unsupported");
      }

      if (name === "notification") {
        return Promise.resolve(
          Notification.permission
        );
      }

      if (
        !navigator.permissions ||
        typeof navigator.permissions.query !== "function"
      ) {
        return Promise.resolve("prompt");
      }

      var permissionName = name;

      if (name === "geolocation") {
        permissionName = "geolocation";
      }

      if (name === "camera") {
        permissionName = "camera";
      }

      if (name === "microphone") {
        permissionName = "microphone";
      }

      if (name === "clipboard-read") {
        permissionName = "clipboard-read";
      }

      if (name === "clipboard-write") {
        permissionName = "clipboard-write";
      }

      return navigator.permissions
        .query({
          name: permissionName
        })
        .then(function (permission) {
          return permission.state;
        })
        .catch(function () {
          return "prompt";
        });
    },

    request: function (name) {
      if (!this.supported(name)) {
        return Promise.resolve("unsupported");
      }

      if (name === "notification") {
        return Notification.requestPermission();
      }

      if (name === "camera") {
        return navigator.mediaDevices
          .getUserMedia({
            video: true
          })
          .then(function (stream) {
            stream.getTracks().forEach(function (track) {
              track.stop();
            });

            return "granted";
          })
          .catch(function (error) {
            if (
              error &&
              (
                error.name === "NotAllowedError" ||
                error.name === "SecurityError"
              )
            ) {
              return "denied";
            }

            return "prompt";
          });
      }

      if (name === "microphone") {
        return navigator.mediaDevices
          .getUserMedia({
            audio: true
          })
          .then(function (stream) {
            stream.getTracks().forEach(function (track) {
              track.stop();
            });

            return "granted";
          })
          .catch(function (error) {
            if (
              error &&
              (
                error.name === "NotAllowedError" ||
                error.name === "SecurityError"
              )
            ) {
              return "denied";
            }

            return "prompt";
          });
      }

      if (name === "geolocation") {
        return new Promise(function (resolve) {
          navigator.geolocation.getCurrentPosition(
            function () {
              resolve("granted");
            },
            function (error) {
              if (error.code === error.PERMISSION_DENIED) {
                resolve("denied");
                return;
              }

              resolve("prompt");
            }
          );
        });
      }

      return this.query(name);
    }
  };

  kitwork.network = (function () {
    var listeners = [];

    function online() {
      return navigator.onLine;
    }

    function connection() {
      var value =
        navigator.connection ||
        navigator.mozConnection ||
        navigator.webkitConnection ||
        null;

      if (!value) {
        return null;
      }

      return {
        type: value.type || null,
        effectiveType: value.effectiveType || null,
        downlink: value.downlink || null,
        rtt: value.rtt || null,
        saveData: !!value.saveData
      };
    }

    function info() {
      return {
        online: online(),
        connection: connection()
      };
    }

    function emit() {
      var value = info();

      for (
        var index = 0;
        index < listeners.length;
        index++
      ) {
        try {
          listeners[index](value);
        } catch (_) { }
      }
    }

    window.addEventListener("online", emit);
    window.addEventListener("offline", emit);

    var nativeConnection =
      navigator.connection ||
      navigator.mozConnection ||
      navigator.webkitConnection;

    if (
      nativeConnection &&
      typeof nativeConnection.addEventListener === "function"
    ) {
      nativeConnection.addEventListener(
        "change",
        emit
      );
    }

    return {
      isOnline: online,

      info: info,

      onChange: function (listener) {
        if (typeof listener !== "function") {
          throw new TypeError(
            "kitwork.network.onChange: listener must be a function"
          );
        }

        listeners.push(listener);

        return function () {
          var index = listeners.indexOf(listener);

          if (index >= 0) {
            listeners.splice(index, 1);
          }
        };
      }
    };
  })();

  kitwork.device = (function () {
    var userAgent =
      navigator.userAgent || "";

    var mobile =
      /Android|iPhone|iPad|iPod|Mobile/i.test(
        userAgent
      );

    var device = {
      mobile: mobile,
      desktop: !mobile,

      touch:
        "ontouchstart" in window ||
        (navigator.maxTouchPoints || 0) > 0,

      cores:
        navigator.hardwareConcurrency || null,

      memory:
        navigator.deviceMemory || null,

      language:
        navigator.language || null,

      languages:
        navigator.languages
          ? Array.prototype.slice.call(
            navigator.languages
          )
          : [],

      info: function () {
        return {
          mobile: device.mobile,
          desktop: device.desktop,
          touch: device.touch,
          cores: device.cores,
          memory: device.memory,
          language: device.language,
          languages: device.languages.slice()
        };
      }
    };

    return device;
  })();

  kitwork.display = {
    width: function () {
      return window.innerWidth;
    },

    height: function () {
      return window.innerHeight;
    },

    screenWidth: function () {
      return window.screen
        ? window.screen.width
        : window.innerWidth;
    },

    screenHeight: function () {
      return window.screen
        ? window.screen.height
        : window.innerHeight;
    },

    pixelRatio: function () {
      return window.devicePixelRatio || 1;
    },

    orientation: function () {
      if (window.innerWidth > window.innerHeight) {
        return "landscape";
      }

      if (window.innerHeight > window.innerWidth) {
        return "portrait";
      }

      return "square";
    },

    fullscreen: function () {
      return !!document.fullscreenElement;
    },

    info: function () {
      return {
        width: this.width(),
        height: this.height(),
        screenWidth: this.screenWidth(),
        screenHeight: this.screenHeight(),
        pixelRatio: this.pixelRatio(),
        orientation: this.orientation(),
        fullscreen: this.fullscreen()
      };
    },

    requestFullscreen: function (element) {
      element = element || document.documentElement;

      if (
        element &&
        typeof element.requestFullscreen === "function"
      ) {
        return element.requestFullscreen();
      }

      return Promise.reject(
        new Error(
          "kitwork.display.requestFullscreen: not supported"
        )
      );
    },

    exitFullscreen: function () {
      if (
        typeof document.exitFullscreen === "function"
      ) {
        return document.exitFullscreen();
      }

      return Promise.reject(
        new Error(
          "kitwork.display.exitFullscreen: not supported"
        )
      );
    },

    toggleFullscreen: function (element) {
      if (this.fullscreen()) {
        return this.exitFullscreen();
      }

      return this.requestFullscreen(element);
    }
  };

  kitwork.location = {
    supported: function () {
      return !!navigator.geolocation;
    },

    current: function (options) {
      options = options || {};

      if (!this.supported()) {
        return Promise.reject(
          new Error("kitwork.location.current: not supported")
        );
      }

      return new Promise(function (resolve, reject) {
        navigator.geolocation.getCurrentPosition(
          function (position) {
            resolve({
              latitude: position.coords.latitude,
              longitude: position.coords.longitude,
              accuracy: position.coords.accuracy,
              altitude: position.coords.altitude,
              altitudeAccuracy: position.coords.altitudeAccuracy,
              heading: position.coords.heading,
              speed: position.coords.speed,
              timestamp: position.timestamp
            });
          },

          function (error) {
            reject(
              new Error(
                "kitwork.location.current: " +
                (error.message || "location failed")
              )
            );
          },

          {
            enableHighAccuracy: !!options.highAccuracy,
            timeout:
              options.timeout == null
                ? 10000
                : options.timeout,
            maximumAge:
              options.maximumAge == null
                ? 0
                : options.maximumAge
          }
        );
      });
    },

    watch: function (listener, options) {
      options = options || {};

      if (typeof listener !== "function") {
        throw new TypeError(
          "kitwork.location.watch: listener must be a function"
        );
      }

      if (!this.supported()) {
        throw new Error(
          "kitwork.location.watch: not supported"
        );
      }

      var id = navigator.geolocation.watchPosition(
        function (position) {
          listener({
            latitude: position.coords.latitude,
            longitude: position.coords.longitude,
            accuracy: position.coords.accuracy,
            altitude: position.coords.altitude,
            altitudeAccuracy: position.coords.altitudeAccuracy,
            heading: position.coords.heading,
            speed: position.coords.speed,
            timestamp: position.timestamp
          });
        },

        function (error) {
          if (typeof options.onError === "function") {
            options.onError(error);
          }
        },

        {
          enableHighAccuracy: !!options.highAccuracy,
          timeout:
            options.timeout == null
              ? 10000
              : options.timeout,
          maximumAge:
            options.maximumAge == null
              ? 0
              : options.maximumAge
        }
      );

      return function () {
        navigator.geolocation.clearWatch(id);
      };
    }
  };

  kitwork.media = {
    supported: function () {
      return !!(
        navigator.mediaDevices &&
        typeof navigator.mediaDevices.getUserMedia === "function"
      );
    },

    open: function (options) {
      options = options || {};

      if (!this.supported()) {
        return Promise.reject(
          new Error("kitwork.media.open: not supported")
        );
      }

      var constraints = {
        video:
          options.video === undefined
            ? true
            : options.video,

        audio:
          options.audio === undefined
            ? false
            : options.audio
      };

      return navigator.mediaDevices.getUserMedia(
        constraints
      );
    },

    camera: function (options) {
      options = options || {};

      return this.open({
        video: {
          facingMode:
            options.facingMode || "environment",

          width: options.width
            ? { ideal: options.width }
            : undefined,

          height: options.height
            ? { ideal: options.height }
            : undefined
        },

        audio: false
      });
    },

    microphone: function (options) {
      options = options || {};

      return this.open({
        video: false,

        audio: {
          echoCancellation:
            options.echoCancellation !== false,

          noiseSuppression:
            options.noiseSuppression !== false,

          autoGainControl:
            options.autoGainControl !== false
        }
      });
    },

    attach: function (element, stream) {
      if (!element) {
        return Promise.reject(
          new TypeError(
            "kitwork.media.attach: element is required"
          )
        );
      }

      if (!(stream instanceof MediaStream)) {
        return Promise.reject(
          new TypeError(
            "kitwork.media.attach: stream must be a MediaStream"
          )
        );
      }

      element.srcObject = stream;

      if (element.tagName === "VIDEO") {
        element.autoplay = true;
        element.playsInline = true;
        element.muted = true;
      }

      var result = element.play();

      if (
        result &&
        typeof result.then === "function"
      ) {
        return result.then(function () {
          return element;
        });
      }

      return Promise.resolve(element);
    },

    detach: function (element) {
      if (!element) {
        return false;
      }

      if (typeof element.pause === "function") {
        element.pause();
      }

      element.srcObject = null;

      return true;
    },

    stop: function (stream) {
      if (!stream || typeof stream.getTracks !== "function") {
        return false;
      }

      stream.getTracks().forEach(function (track) {
        track.stop();
      });

      return true;
    },

    enableAudio: function (stream, enabled) {
      if (!stream) {
        return false;
      }

      stream.getAudioTracks().forEach(function (track) {
        track.enabled = enabled !== false;
      });

      return true;
    },

    enableVideo: function (stream, enabled) {
      if (!stream) {
        return false;
      }

      stream.getVideoTracks().forEach(function (track) {
        track.enabled = enabled !== false;
      });

      return true;
    },

    devices: function () {
      if (
        !navigator.mediaDevices ||
        typeof navigator.mediaDevices.enumerateDevices !==
        "function"
      ) {
        return Promise.resolve([]);
      }

      return navigator.mediaDevices
        .enumerateDevices()
        .then(function (devices) {
          return devices.map(function (device) {
            return {
              id: device.deviceId,
              groupId: device.groupId,
              kind: device.kind,
              label: device.label
            };
          });
        });
    }
  };

  kitwork.camera = {
    supported: function () {
      return kitwork.media.supported();
    },

    open: function (options) {
      return kitwork.media.camera(options);
    },

    attach: function (element, stream) {
      return kitwork.media.attach(element, stream);
    },

    stop: function (stream) {
      return kitwork.media.stop(stream);
    },

    capture: function (source, options) {
      options = options || {};

      if (!source) {
        return Promise.reject(
          new TypeError(
            "kitwork.camera.capture: source is required"
          )
        );
      }

      if (source instanceof MediaStream) {
        var track = source.getVideoTracks()[0];

        if (!track) {
          return Promise.reject(
            new Error(
              "kitwork.camera.capture: video track not found"
            )
          );
        }

        if (
          typeof window.ImageCapture === "function"
        ) {
          var imageCapture = new ImageCapture(track);

          return imageCapture.takePhoto().catch(function () {
            return captureStreamFrame(source, options);
          });
        }

        return captureStreamFrame(source, options);
      }

      if (
        source.tagName === "VIDEO" ||
        source.tagName === "IMG" ||
        source.tagName === "CANVAS"
      ) {
        return captureElement(source, options);
      }

      return Promise.reject(
        new TypeError(
          "kitwork.camera.capture: unsupported source"
        )
      );
    },

    take: function (options) {
      options = options || {};

      var media = kitwork.media;
      var stream;
      var video;

      return media
        .camera(options)
        .then(function (openedStream) {
          stream = openedStream;
          video = document.createElement("video");

          video.style.position = "fixed";
          video.style.left = "-9999px";
          video.style.width = "1px";
          video.style.height = "1px";

          document.body.appendChild(video);

          return media.attach(video, stream);
        })
        .then(function () {
          return waitForVideo(video);
        })
        .then(function () {
          return captureElement(video, options);
        })
        .finally(function () {
          if (stream) {
            media.stop(stream);
          }

          if (video) {
            media.detach(video);
            video.remove();
          }
        });
    }
  };

  function captureStreamFrame(stream, options) {
    var video = document.createElement("video");

    video.style.position = "fixed";
    video.style.left = "-9999px";
    video.style.width = "1px";
    video.style.height = "1px";

    document.body.appendChild(video);

    return kitwork.media
      .attach(video, stream)
      .then(function () {
        return waitForVideo(video);
      })
      .then(function () {
        return captureElement(video, options);
      })
      .finally(function () {
        kitwork.media.detach(video);
        video.remove();
      });
  }

  function waitForVideo(video) {
    if (
      video.readyState >= 2 &&
      video.videoWidth > 0
    ) {
      return Promise.resolve(video);
    }

    return new Promise(function (resolve, reject) {
      function ready() {
        cleanup();
        resolve(video);
      }

      function failed() {
        cleanup();

        reject(
          new Error(
            "kitwork.camera: video could not be loaded"
          )
        );
      }

      function cleanup() {
        video.removeEventListener(
          "loadedmetadata",
          ready
        );

        video.removeEventListener(
          "canplay",
          ready
        );

        video.removeEventListener(
          "error",
          failed
        );
      }

      video.addEventListener(
        "loadedmetadata",
        ready,
        { once: true }
      );

      video.addEventListener(
        "canplay",
        ready,
        { once: true }
      );

      video.addEventListener(
        "error",
        failed,
        { once: true }
      );
    });
  }

  function captureElement(source, options) {
    options = options || {};

    var width =
      options.width ||
      source.videoWidth ||
      source.naturalWidth ||
      source.width;

    var height =
      options.height ||
      source.videoHeight ||
      source.naturalHeight ||
      source.height;

    if (!width || !height) {
      return Promise.reject(
        new Error(
          "kitwork.camera.capture: invalid image size"
        )
      );
    }

    var canvas = document.createElement("canvas");

    canvas.width = width;
    canvas.height = height;

    var context = canvas.getContext("2d");

    if (!context) {
      return Promise.reject(
        new Error(
          "kitwork.camera.capture: canvas is not supported"
        )
      );
    }

    if (options.mirror) {
      context.translate(width, 0);
      context.scale(-1, 1);
    }

    context.drawImage(
      source,
      0,
      0,
      width,
      height
    );

    var type =
      options.type || "image/jpeg";

    var quality =
      options.quality == null
        ? 0.9
        : options.quality;

    return new Promise(function (resolve, reject) {
      canvas.toBlob(
        function (blob) {
          if (!blob) {
            reject(
              new Error(
                "kitwork.camera.capture: capture failed"
              )
            );

            return;
          }

          resolve(blob);
        },
        type,
        quality
      );
    });
  }

  kitwork.recorder = {
    supported: function () {
      return typeof window.MediaRecorder === "function";
    },

    create: function (stream, options) {
      options = options || {};

      if (!this.supported()) {
        throw new Error(
          "kitwork.recorder.create: not supported"
        );
      }

      if (!(stream instanceof MediaStream)) {
        throw new TypeError(
          "kitwork.recorder.create: stream must be a MediaStream"
        );
      }

      var recorderOptions = {};

      if (options.mimeType) {
        if (
          typeof MediaRecorder.isTypeSupported === "function" &&
          !MediaRecorder.isTypeSupported(options.mimeType)
        ) {
          throw new Error(
            "kitwork.recorder.create: unsupported mime type"
          );
        }

        recorderOptions.mimeType = options.mimeType;
      }

      if (options.audioBitsPerSecond) {
        recorderOptions.audioBitsPerSecond =
          options.audioBitsPerSecond;
      }

      if (options.videoBitsPerSecond) {
        recorderOptions.videoBitsPerSecond =
          options.videoBitsPerSecond;
      }

      var nativeRecorder = new MediaRecorder(
        stream,
        recorderOptions
      );

      var chunks = [];
      var stopPromise = null;
      var stopResolve = null;
      var stopReject = null;

      nativeRecorder.addEventListener(
        "dataavailable",
        function (event) {
          if (
            event.data &&
            event.data.size > 0
          ) {
            chunks.push(event.data);
          }
        }
      );

      nativeRecorder.addEventListener(
        "stop",
        function () {
          var type =
            nativeRecorder.mimeType ||
            options.mimeType ||
            "application/octet-stream";

          var blob = new Blob(
            chunks,
            {
              type: type
            }
          );

          chunks = [];

          if (stopResolve) {
            stopResolve({
              blob: blob,
              type: type,
              size: blob.size,
              duration: null
            });
          }

          stopPromise = null;
          stopResolve = null;
          stopReject = null;
        }
      );

      nativeRecorder.addEventListener(
        "error",
        function (event) {
          var error =
            event.error ||
            new Error(
              "kitwork.recorder: recording failed"
            );

          if (stopReject) {
            stopReject(error);
          }

          stopPromise = null;
          stopResolve = null;
          stopReject = null;
        }
      );

      return {
        start: function (timeslice) {
          if (nativeRecorder.state !== "inactive") {
            return false;
          }

          chunks = [];

          if (timeslice != null) {
            nativeRecorder.start(timeslice);
          } else {
            nativeRecorder.start();
          }

          return true;
        },

        pause: function () {
          if (nativeRecorder.state !== "recording") {
            return false;
          }

          nativeRecorder.pause();

          return true;
        },

        resume: function () {
          if (nativeRecorder.state !== "paused") {
            return false;
          }

          nativeRecorder.resume();

          return true;
        },

        stop: function () {
          if (nativeRecorder.state === "inactive") {
            return Promise.reject(
              new Error(
                "kitwork.recorder.stop: recorder is not active"
              )
            );
          }

          if (stopPromise) {
            return stopPromise;
          }

          stopPromise = new Promise(
            function (resolve, reject) {
              stopResolve = resolve;
              stopReject = reject;
            }
          );

          nativeRecorder.stop();

          return stopPromise;
        },

        state: function () {
          return nativeRecorder.state;
        },

        stream: function () {
          return stream;
        },

        native: function () {
          return nativeRecorder;
        }
      };
    }
  };

  kitwork.audio = {
    create: function (source, options) {
      options = options || {};

      var audio = new Audio();
      var objectURL = null;
      var ended = false;

      audio.preload = options.preload || "metadata";
      audio.loop = !!options.loop;
      audio.autoplay = false;
      audio.volume =
        options.volume == null
          ? 1
          : clamp(options.volume, 0, 1);

      audio.muted = !!options.muted;

      if (options.playbackRate != null) {
        audio.playbackRate = options.playbackRate;
      }

      if (typeof source === "string") {
        audio.src = source;
      } else if (source instanceof Blob) {
        objectURL = URL.createObjectURL(source);
        audio.src = objectURL;
      } else if (source instanceof ArrayBuffer) {
        objectURL = URL.createObjectURL(
          new Blob([source], {
            type:
              options.type ||
              "application/octet-stream"
          })
        );

        audio.src = objectURL;
      } else {
        throw new TypeError(
          "kitwork.audio.create: source must be a URL, Blob, File or ArrayBuffer"
        );
      }

      function cleanupURL() {
        if (!objectURL) {
          return;
        }

        URL.revokeObjectURL(objectURL);
        objectURL = null;
      }

      function waitForMetadata() {
        if (audio.readyState >= 1) {
          return Promise.resolve(audio);
        }

        return new Promise(function (resolve, reject) {
          function ready() {
            cleanup();
            resolve(audio);
          }

          function failed() {
            cleanup();

            reject(
              audio.error ||
              new Error(
                "kitwork.audio: failed to load audio"
              )
            );
          }

          function cleanup() {
            audio.removeEventListener(
              "loadedmetadata",
              ready
            );

            audio.removeEventListener(
              "error",
              failed
            );
          }

          audio.addEventListener(
            "loadedmetadata",
            ready,
            { once: true }
          );

          audio.addEventListener(
            "error",
            failed,
            { once: true }
          );
        });
      }

      var player = {
        play: function () {
          ended = false;

          return audio.play().then(function () {
            return player;
          });
        },

        pause: function () {
          audio.pause();

          return player;
        },

        resume: function () {
          return player.play();
        },

        stop: function () {
          audio.pause();

          try {
            audio.currentTime = 0;
          } catch (_) { }

          ended = false;

          return player;
        },

        seek: function (seconds) {
          seconds = Number(seconds);

          if (!Number.isFinite(seconds)) {
            throw new TypeError(
              "kitwork.audio.seek: seconds must be a number"
            );
          }

          var duration = audio.duration;

          if (Number.isFinite(duration)) {
            seconds = clamp(
              seconds,
              0,
              duration
            );
          } else {
            seconds = Math.max(0, seconds);
          }

          audio.currentTime = seconds;

          return player;
        },

        volume: function (value) {
          if (value === undefined) {
            return audio.volume;
          }

          audio.volume = clamp(
            Number(value),
            0,
            1
          );

          return player;
        },

        mute: function () {
          audio.muted = true;

          return player;
        },

        unmute: function () {
          audio.muted = false;

          return player;
        },

        toggleMute: function () {
          audio.muted = !audio.muted;

          return audio.muted;
        },

        loop: function (value) {
          if (value === undefined) {
            return audio.loop;
          }

          audio.loop = !!value;

          return player;
        },

        rate: function (value) {
          if (value === undefined) {
            return audio.playbackRate;
          }

          value = Number(value);

          if (
            !Number.isFinite(value) ||
            value <= 0
          ) {
            throw new TypeError(
              "kitwork.audio.rate: value must be greater than zero"
            );
          }

          audio.playbackRate = value;

          return player;
        },

        currentTime: function () {
          return audio.currentTime || 0;
        },

        duration: function () {
          return Number.isFinite(audio.duration)
            ? audio.duration
            : null;
        },

        state: function () {
          if (ended) {
            return "ended";
          }

          if (audio.paused) {
            return audio.currentTime > 0
              ? "paused"
              : "idle";
          }

          return "playing";
        },

        info: function () {
          return {
            state: player.state(),
            currentTime: player.currentTime(),
            duration: player.duration(),
            volume: audio.volume,
            muted: audio.muted,
            loop: audio.loop,
            playbackRate: audio.playbackRate,
            source: audio.currentSrc || audio.src
          };
        },

        ready: function () {
          return waitForMetadata().then(function () {
            return player;
          });
        },

        on: function (name, listener) {
          if (typeof listener !== "function") {
            throw new TypeError(
              "kitwork.audio.on: listener must be a function"
            );
          }

          audio.addEventListener(
            name,
            listener
          );

          return function () {
            audio.removeEventListener(
              name,
              listener
            );
          };
        },

        destroy: function () {
          audio.pause();

          audio.removeAttribute("src");
          audio.load();

          cleanupURL();

          return true;
        },

        element: function () {
          return audio;
        }
      };

      audio.addEventListener(
        "ended",
        function () {
          ended = true;
        }
      );

      return player;
    },

    play: function (source, options) {
      var player = this.create(
        source,
        options
      );

      return player.play().then(function () {
        return player;
      });
    }
  };

  function clamp(value, minimum, maximum) {
    if (!Number.isFinite(value)) {
      return minimum;
    }

    return Math.min(
      maximum,
      Math.max(minimum, value)
    );
  }

  kitwork.sensors = {
    orientation: {
      supported: function () {
        return "DeviceOrientationEvent" in window;
      },

      permission: function () {
        if (!this.supported()) {
          return Promise.resolve("unsupported");
        }

        if (
          typeof DeviceOrientationEvent.requestPermission ===
          "function"
        ) {
          return DeviceOrientationEvent
            .requestPermission()
            .then(function (state) {
              return state === "granted"
                ? "granted"
                : "denied";
            });
        }

        return Promise.resolve("granted");
      },

      watch: function (listener) {
        if (typeof listener !== "function") {
          throw new TypeError(
            "kitwork.sensors.orientation.watch: listener must be a function"
          );
        }

        if (!this.supported()) {
          throw new Error(
            "kitwork.sensors.orientation.watch: not supported"
          );
        }

        function handle(event) {
          listener({
            alpha:
              event.alpha == null
                ? null
                : event.alpha,

            beta:
              event.beta == null
                ? null
                : event.beta,

            gamma:
              event.gamma == null
                ? null
                : event.gamma,

            absolute:
              !!event.absolute,

            timestamp:
              event.timeStamp
          });
        }

        window.addEventListener(
          "deviceorientation",
          handle
        );

        return function () {
          window.removeEventListener(
            "deviceorientation",
            handle
          );
        };
      }
    },

    motion: {
      supported: function () {
        return "DeviceMotionEvent" in window;
      },

      permission: function () {
        if (!this.supported()) {
          return Promise.resolve("unsupported");
        }

        if (
          typeof DeviceMotionEvent.requestPermission ===
          "function"
        ) {
          return DeviceMotionEvent
            .requestPermission()
            .then(function (state) {
              return state === "granted"
                ? "granted"
                : "denied";
            });
        }

        return Promise.resolve("granted");
      },

      watch: function (listener) {
        if (typeof listener !== "function") {
          throw new TypeError(
            "kitwork.sensors.motion.watch: listener must be a function"
          );
        }

        if (!this.supported()) {
          throw new Error(
            "kitwork.sensors.motion.watch: not supported"
          );
        }

        function number(value) {
          return value == null
            ? null
            : value;
        }

        function vector(value) {
          if (!value) {
            return null;
          }

          return {
            x: number(value.x),
            y: number(value.y),
            z: number(value.z)
          };
        }

        function rotation(value) {
          if (!value) {
            return null;
          }

          return {
            alpha: number(value.alpha),
            beta: number(value.beta),
            gamma: number(value.gamma)
          };
        }

        function handle(event) {
          listener({
            acceleration:
              vector(event.acceleration),

            accelerationIncludingGravity:
              vector(
                event.accelerationIncludingGravity
              ),

            rotationRate:
              rotation(event.rotationRate),

            interval:
              event.interval == null
                ? null
                : event.interval,

            timestamp:
              event.timeStamp
          });
        }

        window.addEventListener(
          "devicemotion",
          handle
        );

        return function () {
          window.removeEventListener(
            "devicemotion",
            handle
          );
        };
      }
    }
  };
})();