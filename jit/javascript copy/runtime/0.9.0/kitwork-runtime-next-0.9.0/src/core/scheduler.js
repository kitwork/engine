"use strict";

var utils = require("./utils.js");
var enqueueMicrotask = utils.enqueueMicrotask;
var nodeContains = utils.nodeContains;
var isNode = utils.isNode;

function createScheduler(runtime) {
  function normalizeBoundary(app, boundary) {
    if (!boundary) return app.root;
    if (boundary.host && isNode(boundary.host)) return boundary.host;
    if (boundary.element && isNode(boundary.element)) return boundary.element;
    if (isNode(boundary)) return boundary;
    return app.root;
  }

  function invalidate(app, boundary, mutation) {
    if (!app || app.destroyed) return;
    var target = normalizeBoundary(app, boundary);
    if (!target || !target.isConnected || !nodeContains(app.root, target)) target = app.root;

    var existing = Array.from(app.dirtyBoundaries);
    for (var i = 0; i < existing.length; i++) {
      var current = existing[i];
      if (nodeContains(current, target)) return;
      if (nodeContains(target, current)) app.dirtyBoundaries.delete(current);
    }

    app.dirtyBoundaries.add(target);
    if (runtime.options && runtime.options.development && mutation) {
      app.lastMutation = mutation;
    }
    schedule(app);
  }

  function schedule(app) {
    if (!app || app.destroyed || app.scheduled) return;
    app.scheduled = true;
    enqueueMicrotask(function () {
      app.scheduled = false;
      flush(app);
    });
  }

  function flush(app) {
    if (!app || app.destroyed) return;
    if (app.rendering) {
      app.renderAgain = true;
      return;
    }

    app.rendering = true;
    try {
      var boundaries = Array.from(app.dirtyBoundaries);
      app.dirtyBoundaries.clear();
      if (!boundaries.length) boundaries.push(app.root);
      runtime.renderBoundaries(app, boundaries);
    } finally {
      app.rendering = false;
    }

    if (app.renderAgain || app.dirtyBoundaries.size) {
      app.renderAgain = false;
      schedule(app);
    }
  }

  return {
    invalidate: invalidate,
    schedule: schedule,
    flush: flush
  };
}

module.exports = {
  createScheduler: createScheduler
};
