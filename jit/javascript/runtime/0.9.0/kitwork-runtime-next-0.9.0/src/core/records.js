"use strict";

/*
 * Runtime ownership records.
 *
 * These records are intentionally plain data. Subsystems keep their private
 * maps, while the records provide one stable contract for ownership and
 * cleanup across hydration, rendering, events and Drive integration.
 */

function createAppRecord(root, name) {
  return {
    root: root,
    name: name || "main",
    scope: Object.create(null),
    scopeInitialized: false,
    aliases: new Map(),
    bindings: new Set(),
    structures: new Set(),
    components: new Set(),
    persisted: new Map(),
    dirtyBoundaries: new Set(),
    pendingComponents: new Set(),
    removedNodes: new Set(),
    reactiveCache: new WeakMap(),
    observer: null,
    scheduled: false,
    rendering: false,
    renderAgain: false,
    initialized: false,
    destroyed: false,
    renderCount: 0,
    errorDepth: 0,
    lastMutation: null,
    cleanups: []
  };
}

function createNodeRecord(node, app) {
  return {
    node: node,
    app: app || null,
    hydrated: false,
    scope: null,
    scopeInitialized: false,
    component: null,
    refOwner: null,
    refName: "",
    bindings: new Map(),
    eventBindings: new Map(),
    structure: null,
    loopFrame: null,
    persistKey: "",
    composing: false,
    fresh: false,
    cleanups: [],
    destroyed: false
  };
}

function createComponentRecord(app, host, name, parent) {
  return {
    app: app,
    host: host,
    name: name,
    parent: parent || null,
    target: Object.create(null),
    instance: null,
    refs: Object.create(null),
    alias: "",
    definition: null,
    hostSeed: Object.create(null),
    activated: false,
    mounted: false,
    mounting: false,
    unmounting: false,
    destroyed: false,
    mountCleanup: null,
    pendingEffects: new Set(),
    cleanups: [],
    tasks: new Set()
  };
}

function createBindingRecord(options, element, attributeName, directiveName, contract, source, compiled) {
  // Support the runtime's positional constructor and an object-form constructor
  // used by tools/tests. Keeping this normalization here prevents every directive
  // subsystem from knowing the record layout.
  if (!options || !options.root) {
    options = options || {};
  } else {
    options = {
      app: options,
      element: element,
      attributeName: attributeName,
      directiveName: directiveName,
      name: directiveName,
      contract: contract,
      source: source,
      compiled: compiled,
      mode: contract && contract.mode,
      phase: contract && contract.phase
    };
  }

  return {
    app: options.app,
    element: options.element,
    attributeName: options.attributeName || "",
    directiveName: options.directiveName || options.name || "",
    name: options.name || options.directiveName || "",
    source: options.source || "",
    mode: options.mode || (options.contract && options.contract.mode) || "binding",
    compiled: options.compiled || null,
    contract: options.contract || null,
    phase: options.phase || (options.contract && options.contract.phase) || "content",
    boundary: options.boundary || null,
    ownerBoundary: options.boundary || null,
    lastValue: undefined,
    initialized: false,
    disabled: false,
    destroyed: false,

    // DOM ownership snapshots.
    initialClasses: null,
    ownedClasses: new Set(),
    ownedStyles: new Set(),
    styleOriginals: new Map(),
    ownedAttributes: new Set(),
    attributeOriginals: new Map(),
    authorHidden: undefined,

    // Model/event state.
    modelSeeded: false,
    eventType: "",
    modifiers: null,
    pendingCount: 0,
    busySnapshot: null,
    consumed: false,
    timer: null,
    throttleAt: 0,
    lastRun: 0,
    directCleanup: null,
    directListenerCleanup: null,
    cleanup: null
  };
}

module.exports = {
  createAppRecord: createAppRecord,
  createNodeRecord: createNodeRecord,
  createComponentRecord: createComponentRecord,
  createBindingRecord: createBindingRecord
};
