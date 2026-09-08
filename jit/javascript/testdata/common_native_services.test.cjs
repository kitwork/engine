"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const test = require("node:test");

// Fake clock, branded abort signals and JSON objects created inside the service
// realm make cancellation deterministic without granting any real device access.
function harness(name, host = true) {
  let now = 1750000000000, next = 0;
  const timers = new Map(), listeners = new WeakMap(), aborts = new WeakMap();
  class Target {
    addEventListener(type, fn) {
      let map = listeners.get(this);
      if (!map) listeners.set(this, map = new Map());
      const set = map.get(type) || new Set(); set.add(fn); map.set(type, set);
    }
    removeEventListener(type, fn) { listeners.get(this)?.get(type)?.delete(fn); }
    dispatchEvent(event) {
      for (const fn of [...(listeners.get(this)?.get(event.type) || [])]) fn(event);
    }
  }
  class Signal extends Target {
    constructor() { super(); aborts.set(this, false); }
    get aborted() {
      if (!aborts.has(this)) throw new TypeError("Invalid signal");
      return aborts.get(this);
    }
  }
  class Controller {
    constructor() { this.signal = new Signal(); }
    abort() { aborts.set(this.signal, true); this.signal.dispatchEvent({ type: "abort" }); }
  }
  const calls = [], diagnostics = [], registrations = [], doc = new Target();
  doc.visibilityState = "visible";
  const kit = {
    capabilities: { supports: id => h.support(id) },
    service(key, value, expression) { registrations.push({ key, expression }); kit[key] = Object.freeze(value); }
  };
  const context = vm.createContext({
    kit, document: doc, EventTarget: Target, AbortSignal: Signal, AbortController: Controller,
    setTimeout(fn, ms) { const id = ++next; timers.set(id, { at: now + ms, fn }); return id; },
    clearTimeout(id) { timers.delete(id); }, reportError(error) { diagnostics.push(error); }, __now: () => now
  });
  const h = {
    calls, diagnostics, registrations, doc, timers, Controller,
    eval: source => vm.runInContext(source, context),
    support: () => true,
    handle(action) {
      if (action.endsWith(".begin")) return h.eval('({operation:"A".repeat(32)})');
      if (action.endsWith(".release")) return true;
      if (action.endsWith(".poll")) return h.eval('({status:"pending"})');
      if (action.endsWith(".permission")) return "granted";
      if (action.endsWith(".status")) return h.eval(name === "biometric" ? '({available:true,enrolled:true})' : '({available:true,enabled:true})');
      if (action.endsWith(".pending")) return h.eval("[]");
      return true;
    },
    async flush() { for (let i = 0; i < 30; i++) await Promise.resolve(); },
    async advance(ms) {
      now += ms;
      for (let turn = 0; turn < 100; turn++) {
        await h.flush();
        const due = [...timers].filter(([, timer]) => timer.at <= now);
        if (!due.length) return;
        for (const [id, timer] of due) if (timers.delete(id)) timer.fn();
      }
      throw Error("Unbounded timer loop");
    },
    hide() { doc.visibilityState = "hidden"; doc.dispatchEvent({ type: "visibilitychange" }); },
    pagehide() { h.eval('EventTarget.prototype.dispatchEvent.call(globalThis,{type:"pagehide"})'); },
    count(suffix) { return calls.filter(x => x.action.endsWith("." + suffix)).length; },
    clean() {
      assert.equal(timers.size, 0, "No remaining timers");
      for (const set of listeners.get(doc)?.values() || []) assert.equal(set.size, 0, "No remaining lifecycle listeners");
    }
  };
  if (host) doc[Symbol.for("kitjs:assembly")] = { nativeHost: {
    call(action, params) { calls.push({ action, params }); return h.handle(action, params); }
  } };
  h.eval("Date.now = __now");
  vm.runInContext(fs.readFileSync(path.join(__dirname, "..", "service", name, name === "notifications" ? "1.2.0.js" : "1.0.0.js"), "utf8"), context);
  h.api = kit[name];
  return h;
}
const families = [
  {name:"biometric",method:"authenticate",ready:"true",options:'({reason:"Confirm test"})'},
  {name:"geolocation",method:"getCurrentPosition",ready:"({latitude:10.5,longitude:106.7,accuracy:25,timestamp:1750000000000})",options:"({timeoutMS:1000})"},
  {name:"nfc",method:"scan",ready:'({records:[{type:"text",value:"Synthetic Kitwork"}]})',options:"({timeoutMS:1000})"}
];
const code = expected => error => error.code === expected && Object.isFrozen(error);
const capture = promise => promise.then(value => ({ value }), error => ({ error }));

for (const {name,method,ready,options} of families) {
  test(name + ": sealed service, availability and passive status", async () => {
    const h = harness(name), status = name === "geolocation" ? "permission" : "status";
    assert.equal(h.registrations[0].expression, undefined);
    assert.equal(await h.api.available(), true);
    assert.ok(await h.api[status]()); assert.equal(h.count("begin"), 0);
    assert.throws(() => h.api.available(true));
    h.handle = () => h.eval('({available:true,enrolled:true,enabled:true})');
    await assert.rejects(h.api[status](), code("FAILED"));
    h.support = () => "yes"; await assert.rejects(h.api.available(), code("FAILED"));
    const browser = harness(name, false);
    assert.equal(await browser.api.available(), false);
    await assert.rejects(browser.api[method](), code("UNAVAILABLE"));
  });
  test(name + ": option descriptors reject accessors, symbols, inheritance and fake signals", () => {
    const h = harness(name);
    for (const source of ["null","[]","Object.create({x:1})","({unknown:true})","({signal:{aborted:false}})","({[Symbol()]:1})","Object.defineProperty({},'signal',{get(){throw Error('getter ran')},enumerable:true})"]) {
      assert.throws(() => h.api[method](h.eval(source)), error => error.name === "TypeError" && !error.message.includes("getter ran"));
    }
    assert.throws(() => h.api[method]({},{})); assert.equal(h.calls.length,0);
  });
  test(name + ": success copies/freezes data and releases once", async () => {
    const h=harness(name), normal=h.handle;
    h.handle=action=>action.endsWith(".poll")?h.eval('({status:"ready",value:'+ready+'})'):normal(action);
    assert.ok(Object.isFrozen(await h.api[method](h.eval(options))));
    assert.equal(h.count("release"),1); h.pagehide(); await h.flush();
    assert.equal(h.count("release"),1); h.clean();
  });
  test(name + ": pre-abort and initial hidden page invoke no native method", async () => {
    const h=harness(name), controller=new h.Controller(), opts=h.eval("({})");
    controller.abort(); opts.signal=controller.signal;
    await assert.rejects(h.api[method](opts),code("CANCELLED")); h.hide();
    await assert.rejects(h.api[method](),code("CANCELLED"));
    assert.equal(h.calls.length,0); h.clean();
  });
  test(name + ": abort during begin releases a late token exactly once", async () => {
    const h=harness(name), normal=h.handle; let resolveBegin;
    h.handle=action=>action.endsWith(".begin")?new Promise(resolve=>resolveBegin=resolve):normal(action);
    const controller=new h.Controller(), opts=h.eval("({})"); opts.signal=controller.signal;
    const result=capture(h.api[method](opts)); controller.abort();
    assert.equal((await result).error.code,"CANCELLED");
    resolveBegin(h.eval('({operation:"B".repeat(32)})')); await h.flush();
    assert.equal(h.count("poll"),0); assert.equal(h.count("release"),1); h.clean();
  });
  test(name + ": never-settling begin times out and late completion releases", async () => {
    const h=harness(name), normal=h.handle; let resolveBegin;
    h.handle=action=>action.endsWith(".begin")?new Promise(resolve=>resolveBegin=resolve):normal(action);
    const result=capture(h.api[method]()); await h.advance(15000);
    assert.equal((await result).error.code,"TIMEOUT"); h.clean();
    resolveBegin(h.eval('({operation:"C".repeat(32)})')); await h.flush();
    assert.equal(h.count("release"),1); h.clean();
  });
  test(name + ": malformed begin cleans own token but never invokes accessors", async () => {
    const h=harness(name), normal=h.handle;
    h.handle=action=>action.endsWith(".begin")?h.eval('({operation:"D".repeat(32),extra:1})'):normal(action);
    await assert.rejects(h.api[method](),code("FAILED")); assert.equal(h.count("release"),1);
    h.handle=action=>action.endsWith(".begin")?h.eval('Object.defineProperty({},"operation",{enumerable:true,get(){throw Error("getter ran")}})'):normal(action);
    await assert.rejects(h.api[method](),code("FAILED")); assert.equal(h.count("release"),1); h.clean();
  });
  test(name + ": cleanup release has its own deadline", async () => {
    const h=harness(name), normal=h.handle;
    h.handle=action=>action.endsWith(".poll")?h.eval('({status:"ready",value:'+ready+'})'):action.endsWith(".release")?new Promise(()=>{}):normal(action);
    const result=capture(h.api[method]()); await h.flush(); await h.advance(3000);
    assert.ok((await result).value); assert.equal(h.count("release"),1);
    assert.equal(h.diagnostics[0].code,"TIMEOUT"); h.clean();
  });
  test(name + ": busy, visibility, navigation, timeout and native failures settle", async () => {
    const h=harness(name);
    let result=capture(h.api[method]()); await h.flush();
    await assert.rejects(h.api[method](),code("OVERLOADED")); h.pagehide();
    assert.equal((await result).error.code,"CANCELLED"); await h.flush(); h.clean();
    result=capture(h.api[method]()); await h.flush(); h.hide();
    assert.equal((await result).error.code,"CANCELLED"); await h.flush(); h.clean();
    h.doc.visibilityState="visible";
    result=capture(h.api[method](h.eval(options))); await h.flush();
    await h.advance(name==="biometric"?65000:6000);
    assert.equal((await result).error.code,"TIMEOUT"); await h.flush(); h.clean();
    const normal=h.handle;
    for(const failure of ["DENIED","UNAVAILABLE","TIMEOUT","OVERLOADED","FAILED","cancelled","unknown"]){
      h.handle=action=>action.endsWith(".poll")?h.eval(failure==="cancelled"?'({status:"cancelled"})':'({status:"failed",code:'+JSON.stringify(failure)+'})'):normal(action);
      await assert.rejects(h.api[method](),code(failure==="unknown"?"FAILED":failure==="cancelled"?"CANCELLED":failure)); h.clean();
    }
  });
}

test("geolocation: strict finite coordinates, timestamp and option ranges",async()=>{
  const h=harness("geolocation"), normal=h.handle;
  for(const value of ["({latitude:91,longitude:0,accuracy:1,timestamp:1})","({latitude:0,longitude:0,accuracy:-1,timestamp:1})","({latitude:0,longitude:0,accuracy:1,timestamp:1.5})","({latitude:0,longitude:NaN,accuracy:1,timestamp:1})","({latitude:0,longitude:0,accuracy:1,timestamp:1,extra:1})"]){
    h.handle=action=>action.endsWith(".poll")?h.eval('({status:"ready",value:'+value+'})'):normal(action);
    await assert.rejects(h.api.getCurrentPosition(),code("FAILED"));
  }
  for(const input of ["({highAccuracy:1})","({timeoutMS:999})","({timeoutMS:60001})","({maximumAgeMS:300001})","({maximumAgeMS:1.1})"])assert.throws(()=>h.api.getCurrentPosition(h.eval(input)));
});
test("biometric: control-free bounded reason and boolean-only success",async()=>{
  const h=harness("biometric"),normal=h.handle;
  for(const reason of ['""','"x".repeat(257)','"\\ud800"','"a\\nb"',"false"])assert.throws(()=>h.api.authenticate(h.eval("({reason:"+reason+"})")));
  h.handle=action=>action.endsWith(".poll")?h.eval('({status:"ready",value:false})'):normal(action);
  await assert.rejects(h.api.authenticate(),code("FAILED"));
});
test("NFC: strict arrays, records, UTF8 and aggregate size",async()=>{
  const h=harness("nfc"),normal=h.handle;
  const values=['({records:new Array(1)})','({records:[{type:"binary",value:"x"}]})','({records:[{type:"text",value:"😀".repeat(1025)}]})','({records:[{type:"text",value:"\\ud800"}]})','({records:Array.from({length:9},()=>({type:"text",value:"x".repeat(4096)}))})','({records:Array.from({length:33},()=>({type:"text",value:"x"}))})','({records:[{type:"text",get value(){throw Error("getter ran")}}]})','({records:Object.assign([],{extra:1})})','({records:new (class extends Array {})()})'];
  for(const value of values){
    h.handle=action=>action.endsWith(".poll")?h.eval('({status:"ready",value:'+value+'})'):normal(action);
    await assert.rejects(h.api.scan(),code("FAILED"));
  }
  assert.equal(h.count("release"),values.length);
});
test("notifications: schedule/list/cancel are sealed and native-only, old methods remain",async()=>{
  const h=harness("notifications");
  assert.deepEqual(Object.keys(h.api).sort(),["cancel","pending","permission","requestPermission","schedule","show"]);
  assert.equal(h.registrations[0].expression,undefined);
  assert.equal(await h.api.schedule(h.eval('({title:"Kit",body:"Synthetic",tag:"canary",at:Date.now()+5000,path:"/native-lab"})')),true);
  assert.equal(h.count("requestPermission"),0);assert.equal(h.count("schedule"),1);
  assert.equal(await h.api.cancel("canary"),true);
  h.handle=()=>h.eval('[{tag:"canary",at:1750000005000}]');
  const pending=await h.api.pending(); assert.ok(Object.isFrozen(pending)&&Object.isFrozen(pending[0]));
  const browser=harness("notifications",false);
  await assert.rejects(browser.api.schedule(browser.eval('({title:"Kit",body:"",tag:"x",at:Date.now()+5000})')),code("UNAVAILABLE"));
  await assert.rejects(browser.api.pending(),code("UNAVAILABLE"));
  await assert.rejects(browser.api.cancel("x"),code("UNAVAILABLE"));
});
test("notifications: payload, time and permission validation never prompt implicitly",async()=>{
  const h=harness("notifications");
  for(const input of ['({title:"Kit",body:"",tag:"x",at:Date.now()+999})','({title:"Kit",body:"",tag:"x",at:Date.now()+2592000001})','({title:"Kit",body:"",tag:"x",at:Date.now()+5000,extra:1})','({title:"Kit",body:"",tag:"x",get at(){throw Error("getter ran")}})','({title:"Kit",body:"",tag:"x".repeat(129),at:Date.now()+5000})','({title:"Kit",body:"",tag:"x",at:Date.now()+5000,path:"//external"})','({title:"Kit",body:"",at:Date.now()+5000})'])assert.throws(()=>h.api.schedule(h.eval(input)),error=>error.name==="TypeError"&&!error.message.includes("getter ran"));
  for(const state of ["prompt","denied"]){h.handle=()=>state;await assert.rejects(h.api.schedule(h.eval('({title:"Kit",body:"",tag:"x",at:Date.now()+5000})')),code("DENIED"));}
  assert.equal(h.count("schedule"),0);assert.equal(h.count("requestPermission"),0);
  assert.throws(()=>h.api.cancel(""));assert.throws(()=>h.api.cancel("x".repeat(129)));
});
test("notifications: pending shape/duplicates/size and acknowledgements fail closed",async()=>{
  const h=harness("notifications");
  for(const value of ['new Array(1)','Object.assign([],{extra:1})','new (class extends Array {})()','[{tag:"x".repeat(129),at:1}]','[{tag:"x",at:1},{tag:"x",at:2}]','[{tag:"x",at:1.5}]','[{tag:"x",at:1,extra:1}]','[{get tag(){throw Error("getter ran")},at:1}]','Array.from({length:33},(_,i)=>({tag:"x"+i,at:1}))']){
    h.handle=()=>h.eval(value);await assert.rejects(h.api.pending(),code("FAILED"));
  }
  h.handle=action=>action.endsWith(".permission")?"granted":false;
  await assert.rejects(h.api.cancel("x"),code("FAILED"));
  await assert.rejects(h.api.schedule(h.eval('({title:"Kit",body:"",tag:"x",at:Date.now()+5000})')),code("FAILED"));
});
