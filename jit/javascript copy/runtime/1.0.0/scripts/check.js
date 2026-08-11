"use strict";

const fs = require("fs");
const path = require("path");
const childProcess = require("child_process");

const root = path.resolve(__dirname, "..");
const targets = [path.join(root, "src"), path.join(root, "scripts"), path.join(root, "test")];
const files = [];

function collect(directory) {
  if (!fs.existsSync(directory)) return;
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const full = path.join(directory, entry.name);
    if (entry.isDirectory()) collect(full);
    else if (entry.isFile() && entry.name.endsWith(".js")) files.push(full);
  }
}

targets.forEach(collect);
files.sort();

const failures = [];
for (const file of files) {
  const result = childProcess.spawnSync(process.execPath, ["--check", file], {
    encoding: "utf8"
  });
  if (result.status !== 0) failures.push({ file, output: result.stderr || result.stdout });
}

const dist = path.join(root, "dist", "kitwork-runtime.js");
if (fs.existsSync(dist)) {
  const result = childProcess.spawnSync(process.execPath, ["--check", dist], { encoding: "utf8" });
  if (result.status !== 0) failures.push({ file: dist, output: result.stderr || result.stdout });
  else files.push(dist);
}

if (failures.length) {
  failures.forEach((failure) => {
    console.error("Syntax failure:", failure.file);
    console.error(failure.output);
  });
  process.exit(1);
}

console.log(`syntax check: passed (${files.length} files)`);
