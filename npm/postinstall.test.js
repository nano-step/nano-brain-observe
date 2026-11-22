"use strict";

const test = require("node:test");
const assert = require("node:assert");
const { assetName, expectedSHA256 } = require("./postinstall");

test("assetName maps node platform/arch to release asset names", () => {
  assert.strictEqual(assetName("darwin", "arm64"), "nano-brain-observe-darwin-arm64");
  assert.strictEqual(assetName("linux", "x64"), "nano-brain-observe-linux-amd64");
  assert.strictEqual(assetName("win32", "x64"), "nano-brain-observe-windows-amd64.exe");
  assert.throws(() => assetName("freebsd", "x64"), /Unsupported platform/);
  assert.throws(() => assetName("linux", "ia32"), /Unsupported platform/);
});

test("expectedSHA256 finds the exact filename only", () => {
  const a = "a".repeat(64);
  const b = "b".repeat(64);
  const sums = `${a}  nano-brain-observe-linux-amd64\n${b}  nano-brain-observe-linux-amd64.exe\r\n`;
  assert.strictEqual(expectedSHA256(sums, "nano-brain-observe-linux-amd64"), a);
  assert.strictEqual(expectedSHA256(sums, "nano-brain-observe-linux-amd64.exe"), b);
  assert.strictEqual(expectedSHA256(sums, "nano-brain-observe-darwin-arm64"), null);
  assert.strictEqual(expectedSHA256("", "x"), null);
});
