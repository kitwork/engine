;(function () {
"use strict";

var shopDialogPrivate = new WeakMap();

function shopDialogState() {
  var host = document.getElementById("shop-confirm-dialog");
  if (!host) return null;
  var state = shopDialogPrivate.get(host);
  if (!state) {
    state = { callback: null, originID: "", generation: 0 };
    shopDialogPrivate.set(host, state);
  }
  return state;
}

function shopFocusLater(element, state, generation) {
  setTimeout(function () {
    if (state && state.generation !== generation) return;
    if (element && element.isConnected && typeof element.focus === "function") {
      element.focus();
    }
  }, 0);
}

function shopSetBackgroundBlocked(blocked) {
  var shell = document.getElementById("shop-shell");
  if (!shell) return;
  if (blocked) {
    shell.setAttribute("inert", "");
    shell.setAttribute("aria-hidden", "true");
    return;
  }
  shell.removeAttribute("inert");
  shell.removeAttribute("aria-hidden");
}

kit.component("shop-products", {
  products: [
    {
      id: "field-notes",
      name: "Field Notes",
      description: "A compact notebook for ideas that should not wait.",
      price: 24
    },
    {
      id: "desk-lamp",
      name: "Focus Lamp",
      description: "Warm, dimmable light for a quieter workspace.",
      price: 58
    },
    {
      id: "day-bag",
      name: "Day Bag",
      description: "A light everyday bag with room for the essentials.",
      price: 72
    }
  ],

  money: function (amount) {
    return "$" + Number(amount).toFixed(2);
  }
});

kit.component("shop-cart", {
  items: [],

  get count() {
    return this.items.reduce(function (total, item) {
      return total + item.quantity;
    }, 0);
  },

  get total() {
    return this.items.reduce(function (total, item) {
      return total + item.price * item.quantity;
    }, 0);
  },

  add: function (product) {
    var current = this.items.find(function (item) {
      return item.id === product.id;
    });

    if (current) {
      this.items = this.items.map(function (item) {
        if (item.id !== product.id) return item;
        return {
          id: item.id,
          name: item.name,
          price: item.price,
          quantity: item.quantity + 1
        };
      });
      return;
    }

    this.items = this.items.concat([{
      id: product.id,
      name: product.name,
      price: product.price,
      quantity: 1
    }]);
  },

  remove: function (id) {
    this.items = this.items.filter(function (item) {
      return item.id !== id;
    });
  },

  clear: function () {
    this.items = [];
    return true;
  },

  money: function (amount) {
    return "$" + Number(amount).toFixed(2);
  }
});

kit.component("shop-checkout", {
  name: "",
  email: "",
  address: "",
  placed: false,
  orderName: "",

  get ready() {
    return this.name.trim() !== "" &&
      this.email.includes("@") &&
      this.address.trim() !== "";
  },

  completeOrder: function () {
    this.orderName = this.name;
    this.placed = true;
  }
});

kit.component("shop-dialog", {
  visible: false,

  init: function () {
    var host = document.getElementById("shop-confirm-dialog");
    if (!host) return;
    host.addEventListener("keydown", function (event) {
      if (event.key !== "Tab") return;
      var controls = host.querySelectorAll("button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex='-1'])");
      if (!controls.length) {
        event.preventDefault();
        host.focus();
        return;
      }
      var first = controls[0];
      var last = controls[controls.length - 1];
      if (event.shiftKey && (document.activeElement === first || !host.contains(document.activeElement))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    });
  },

  open: function (triggerID, callback) {
    var state = shopDialogState();
    if (!state || typeof callback !== "function") return;
    var generation = state.generation + 1;
    state.generation = generation;
    state.callback = callback;
    state.originID = String(triggerID || "");
    this.visible = true;
    setTimeout(function () {
      if (state.generation !== generation) return;
      var cancel = document.getElementById("shop-dialog-cancel");
      if (!cancel || !cancel.isConnected) return;
      cancel.focus();
      shopSetBackgroundBlocked(true);
    }, 0);
  },

  close: function () {
    var state = shopDialogState();
    var origin = state && document.getElementById(state.originID);
    var generation = state ? state.generation + 1 : 0;
    if (state) {
      state.generation = generation;
      state.callback = null;
      state.originID = "";
    }
    shopSetBackgroundBlocked(false);
    this.visible = false;
    shopFocusLater(origin, state, generation);
  },

  confirm: function () {
    var state = shopDialogState();
    var callback = state && state.callback;
    var origin = state && document.getElementById(state.originID);
    var generation = state ? state.generation + 1 : 0;
    if (state) {
      state.generation = generation;
      state.callback = null;
      state.originID = "";
    }
    shopSetBackgroundBlocked(false);
    this.visible = false;
    shopFocusLater(origin, state, generation);
    if (callback) callback();
  }
});

})();
