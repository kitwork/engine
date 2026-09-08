package javascript

import (
	"bytes"
	"reflect"
	"testing"
)

func TestApp1170AddsStructuredSQLiteCommits(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	previous := serviceVersionMap(appComponentServices("1.16.0"))
	current := serviceVersionMap(appComponentServices("1.17.0"))
	if previous["studioSqlite"] != "1.0.0" || current["studioSqlite"] != "1.1.0" {
		t.Fatal("SQLite exact versions changed unexpectedly")
	}
	current["studioSqlite"] = "1.0.0"
	if !reflect.DeepEqual(previous, current) {
		t.Fatal("app@1.17.0 changed unrelated service pins")
	}
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.16.0.js"), readVanillaFile(t, "component", "app", "1.17.0.js")) {
		t.Fatal("app@1.17.0 changed app behavior")
	}
	artifact, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.17.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(artifact.JavaScript, []byte(`services["studioSqlite"] = "1.1.0"`)) ||
		bytes.Contains(artifact.JavaScript, []byte(`actions["studioSqlite"]["`)) || validAuthoredServiceAction("studioSqlite", "commit") {
		t.Fatal("SQLite commit service was omitted or gained authored action authority")
	}
}

func TestStudioSQLite110CommitContract(t *testing.T) {
	source := readVanillaFile(t, "service", "studioSqlite", "1.1.0.js")
	script := `
"use strict";
var assert = require("node:assert/strict");
globalThis.setTimeout = function () { return 1; };
globalThis.clearTimeout = function () {};
var host;
globalThis.document = {};
Object.defineProperty(document, Symbol.for("kitjs:assembly"), { value: { nativeHost: { call: function (action, params) { return host(action, params); } } } });
var service;
var kit = { service: function (name, methods) { assert.equal(name, "studioSqlite"); service = methods; } };
` + string(source) + `
var operation = "O".repeat(32), handle = "H".repeat(32), calls = [];
var columns = [
 { key:"id",position:1,name:"id",type:"INTEGER",nullable:"No",keyLabel:"PRIMARY KEY",defaultValue:"",primaryOrder:1,generated:false },
 { key:"value",position:2,name:"value",type:"TEXT",nullable:"Yes",keyLabel:"",defaultValue:"",primaryOrder:0,generated:false },
 { key:"derived",position:3,name:"derived",type:"TEXT",nullable:"Yes",keyLabel:"",defaultValue:"",primaryOrder:0,generated:true }
];
var page = { format:"kitwork-studio-native-sqlite-table", version:1, ok:true, source:"sqlite",database:"test.db",schema:"main",readOnly:true,
 catalogVersion:1,name:"items",type:"table",columns:columns,rows:[[1,"before","generated"]],totalRows:1,page:1,pageSize:100,pageCount:1,pageStart:1,pageEnd:1,hasPreviousPage:false,hasNextPage:false,durationMs:2,
 editing:{supported:true,keyColumns:["id"],reason:""} };
var statements = [{sql:'BEGIN IMMEDIATE',parameters:[],durationMs:1,affectedRows:0},
 {sql:'UPDATE "main"."items" SET "value" = ? WHERE "id" IS ?',parameters:["after",1],durationMs:3,affectedRows:1},
 {sql:'COMMIT',parameters:[],durationMs:1,affectedRows:0}];
var committed = { format:"kitwork-studio-native-sqlite-commit",version:1,ok:true,source:"sqlite",database:"test.db",schema:"main",readOnly:false,
 name:"items",catalogVersion:1,affectedRows:1,durationMs:5,statements:statements };
var nextCommit = function () { return committed; };
var nextPage = function () { return page; };
host = function(action, params) {
 calls.push({action:action,params:params});
 if (action === "studioSqlite.beginOpen") return {operation:operation};
 if (action === "studioSqlite.pollOpen") return {status:"connected",handle:handle,name:"test.db",database:"test.db",schema:"main",readOnly:true};
 if (action === "studioSqlite.releaseOpen" || action === "studioSqlite.close") return true;
 if (action === "studioSqlite.tablePage") { assert.equal(params.includeEditing,true); return nextPage(); }
 if (action === "studioSqlite.commit") return nextCommit(params);
 throw new Error("unexpected host action " + action);
};
var options = {type:"table",name:"items",catalogVersion:1,page:1,pageSize:100};
function changes(value) { return {name:"items",catalogVersion:1,changes:[{kind:"update",original:{id:1,value:"before",derived:"generated"},values:{value:value}}]}; }
function failure(code, details) {
 return {format:"kitwork-studio-native-sqlite-commit",version:1,ok:false,source:"sqlite",code:code,message:"host message must not leak",details:details || {
  name:"items",catalogVersion:1,statements:[{sql:"ROLLBACK",parameters:[],durationMs:1,affectedRows:0}],failedChange:0,rolledBack:true}};
}
(async function () {
 assert.deepEqual(Object.keys(service).sort(), ["catalog","close","commit","open","query","tablePage"]);
 var ref = await service.open();
 assert.throws(function(){service.commit(ref,changes("after"));}, function(error){return error.code === "SCHEMA_CHANGED";});
 var loaded = await service.tablePage(ref,options);
 assert.deepEqual(loaded.editing.keyColumns,["id"]);
 assert(Object.isFrozen(loaded.editing) && Object.isFrozen(loaded.editing.keyColumns));
 var input = changes("'; DROP TABLE items; --");
 var result = await service.commit(ref,input);
 var forwarded = calls.filter(function(call){return call.action === "studioSqlite.commit";}).at(-1).params;
 assert.equal(forwarded.handle,handle);
 assert.equal(forwarded.changes[0].values.value,input.changes[0].values.value);
 assert(Object.isFrozen(forwarded.changes) && Object.isFrozen(forwarded.changes[0].original));
 assert(Object.isFrozen(result.statements[1].parameters));
 assert.equal(result.affectedRows,1);
 assert.equal(result.statements[1].sql,statements[1].sql);
 assert.throws(function(){service.commit(ref,changes("twice"));},function(error){return error.code === "SCHEMA_CHANGED";});
 await service.tablePage(ref,options);
 var before = calls.length;
 var malformed = [
  {name:"items",catalogVersion:1,changes:[]},
  {name:"items",catalogVersion:1,changes:Array.from({length:101},function(){return changes("x").changes[0];})},
  {name:"items",catalogVersion:1,changes:[{kind:"update",original:{id:1},values:{value:"x"}}]},
  {name:"items",catalogVersion:1,changes:[{kind:"insert",values:{derived:"x"}}]},
  {name:"items",catalogVersion:1,changes:[{kind:"delete",original:{id:1,value:"before",derived:"generated"},values:{value:"x"}}]},
  changes("x".repeat(256*1024+1)), changes(NaN), changes(Number.MAX_SAFE_INTEGER+1), changes(undefined),
  changes({type:"blob",base64:"AR==",bytes:1}),
  Object.assign(changes("x"),{sql:"DELETE FROM items"})
 ];
 malformed.push({name:"items",catalogVersion:1,changes:Array.from({length:9},function(){return {kind:"insert",values:{value:"x".repeat(256*1024)}};})});
 for (var bad of malformed) assert.throws(function(){service.commit(ref,bad);}, TypeError);
 var getterCalls=0, accessor=changes("x");
 Object.defineProperty(accessor.changes[0].values,"value",{enumerable:true,get:function(){getterCalls++;return "x";}});
 assert.throws(function(){service.commit(ref,accessor);},TypeError);
 assert.equal(getterCalls,0);
 assert.equal(calls.length,before,"invalid changes reached native host");
 var blob=changes({type:"blob",base64:"AQ==",bytes:1});
 nextCommit=function(params){assert.equal(params.changes[0].values.value.bytes,1);return committed;};
 await service.commit(ref,blob);
 await service.tablePage(ref,options);
 nextCommit=function(params){assert.equal(Object.keys(params.changes[0].values).length,0);return committed;};
 await service.commit(ref,{name:"items",catalogVersion:1,changes:[{kind:"insert",values:{}}]});
 await service.tablePage(ref,options);
 nextCommit=function(params){assert.equal(params.changes[0].original.value,"before");assert.equal(params.changes[0].values,undefined);return committed;};
 await service.commit(ref,{name:"items",catalogVersion:1,changes:[{kind:"delete",original:{id:1,value:"before",derived:"generated"}}]});
 await service.tablePage(ref,options);
 nextCommit=function(){return failure("CONSTRAINT_FAILED");};
 await assert.rejects(service.commit(ref,changes("bad")),function(error){
  assert.equal(error.code,"CONSTRAINT_FAILED"); assert(error.details.rolledBack); assert(Object.isFrozen(error.details.statements));
  assert(!error.message.includes("host message")); return true;
 });
 nextCommit=function(){return committed;};
 assert.equal((await service.commit(ref,changes("corrected"))).affectedRows,1,"constraint failures must allow corrected retry");
 await service.tablePage(ref,options);
 nextCommit=function(){return failure("ROW_CONFLICT");};
 await assert.rejects(service.commit(ref,changes("stale")),function(error){return error.code === "ROW_CONFLICT";});
 nextCommit=function(){return failure("SCHEMA_CHANGED");};
 await assert.rejects(service.commit(ref,changes("stale")),function(error){return error.code === "SCHEMA_CHANGED";});
 assert.throws(function(){service.commit(ref,changes("stale"));},function(error){return error.code === "SCHEMA_CHANGED";});
 await service.tablePage(ref,options);
 var releaseCommit;
 nextCommit=function(){return new Promise(function(resolve){releaseCommit=resolve;});};
 var pending=service.commit(ref,changes("pending"));
 await assert.rejects(service.commit(ref,changes("duplicate")),function(error){return error.code === "BUSY";});
 await assert.rejects(service.close(ref),function(error){return error.code === "BUSY";});
 releaseCommit(committed); await pending;
 await service.tablePage(ref,options);
 var resolvePage;
 nextPage=function(){return new Promise(function(resolve){resolvePage=resolve;});};
 var stalePage=service.tablePage(ref,options);
 nextCommit=function(){return committed;};
 await service.commit(ref,changes("newer"));
 resolvePage(page); await stalePage;
 assert.throws(function(){service.commit(ref,changes("stale response"));},function(error){return error.code === "SCHEMA_CHANGED";});
 nextPage=function(){return page;};
 await service.tablePage(ref,options);
 nextCommit=function(){return Object.assign({},committed,{database:"another.db"});};
 await assert.rejects(service.commit(ref,changes("x")),function(error){return error.code === "FAILED";});
 nextCommit=function(){var bad=failure("ROW_CONFLICT");bad.details.statements[0].path="private";return bad;};
 await assert.rejects(service.commit(ref,changes("x")),function(error){return error.code === "FAILED" && !error.details;});
 page.editing={supported:false,keyColumns:[],reason:"No primary or unique key"};
 await service.tablePage(ref,options);
 assert.throws(function(){service.commit(ref,changes("x"));},function(error){return error.code === "READ_ONLY_TABLE";});
 await service.close(ref);
 assert.throws(function(){service.commit(ref,changes("x"));},TypeError);
})().catch(function(error){console.error(error.stack || error);process.exitCode=1;});
`
	runNativeHostNode(t, script)
}
