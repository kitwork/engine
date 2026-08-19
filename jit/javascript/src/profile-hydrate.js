; (function (global, document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var PROFILE = Symbol.for("kitjs:profile");
  var expected = "hydrate";
  var core = document[ASSEMBLY];
  if (!core || core.phase !== "drive") {
    delete document[ASSEMBLY];
    throw new Error("KitJS: hydrate profile marker loaded out of order");
  }

  try {
    if (core.reuse) {
      var active = global.kit && global.kit[PROFILE];
      if (active !== expected) {
        if (active === "kit") {
          throw new Error("KitJS: cannot install hydrate profile over active kit profile");
        }
        throw new Error("KitJS: active runtime has no compatible hydrate profile marker");
      }
      core.profile = expected;
      return;
    }
    if (!core.kit || core.OWN.call(core.kit, PROFILE)) {
      throw new Error("KitJS: hydrate profile marker cannot be installed");
    }
    Object.defineProperty(core.kit, PROFILE, { value: expected });
    core.profile = expected;
  } catch (error) {
    delete document[ASSEMBLY];
    throw error;
  }
})(globalThis, document);
