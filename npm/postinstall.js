#!/usr/bin/env node
"use strict";

const https = require("https");
const fs = require("fs");
const path = require("path");
const os = require("os");
const crypto = require("crypto");
const { execFileSync } = require("child_process");

const VERSION = require("../package.json").version;
const REPO = "nano-step/nano-brain-observe";
const NAME = "nano-brain-observe";

const PLATFORMS = { darwin: "darwin", linux: "linux", win32: "windows" };
const ARCHES = { arm64: "arm64", x64: "amd64" };

function assetName(platform = os.platform(), arch = os.arch()) {
  if (!PLATFORMS[platform] || !ARCHES[arch]) {
    throw new Error(`Unsupported platform: ${platform}-${arch}`);
  }
  return `${NAME}-${PLATFORMS[platform]}-${ARCHES[arch]}${platform === "win32" ? ".exe" : ""}`;
}

function binaryPath() {
  return path.join(__dirname, os.platform() === "win32" ? `${NAME}.exe` : NAME);
}

function get(url, redirects = 5) {
  return new Promise((resolve, reject) => {
    https
      .get(url, { headers: { "User-Agent": `${NAME}-install` } }, (res) => {
        if ([301, 302, 303, 307, 308].includes(res.statusCode) && res.headers.location) {
          res.resume();
          if (redirects === 0) return reject(new Error(`Too many redirects for ${url}`));
          return resolve(get(new URL(res.headers.location, url).toString(), redirects - 1));
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(new Error(`GET ${url}: HTTP ${res.statusCode}`));
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks)));
        res.on("error", reject);
      })
      .on("error", reject);
  });
}

// expectedSHA256 returns the hex digest listed for filename in a SHA256SUMS
// file, or null.
function expectedSHA256(sums, filename) {
  for (const line of String(sums).split(/\r?\n/)) {
    const m = line.match(/^([a-f0-9]{64})\s+\*?(.+?)\s*$/i);
    if (m && m[2] === filename) return m[1].toLowerCase();
  }
  return null;
}

function installedVersion(bin) {
  try {
    return execFileSync(bin, ["--version"], { timeout: 5000 }).toString().trim();
  } catch (_) {
    return null;
  }
}

// ensureBinary downloads the release binary for this platform next to this
// file, verifies it against the release's SHA256SUMS and returns its path.
// It throws on failure and never leaves an unverified binary behind.
async function ensureBinary() {
  const bin = binaryPath();
  if (fs.existsSync(bin) && installedVersion(bin) === `v${VERSION}`) return bin;

  const asset = assetName();
  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  const [data, sums] = await Promise.all([get(`${base}/${asset}`), get(`${base}/SHA256SUMS`)]);
  const want = expectedSHA256(sums, asset);
  const got = crypto.createHash("sha256").update(data).digest("hex");
  if (!want || want !== got) {
    throw new Error(`SHA-256 mismatch for ${asset}: expected ${want || "(not listed)"}, got ${got}`);
  }
  // Write to a temp file and rename so a running proxy keeps its old inode.
  const tmp = `${bin}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, data, { mode: 0o755 });
  fs.renameSync(tmp, bin);
  return bin;
}

if (require.main === module) {
  ensureBinary()
    .then((bin) => console.error(`${NAME} v${VERSION} installed at ${bin}`))
    .catch((err) => {
      // Do not fail `npm install`; run.js retries the download on first use.
      console.error(`${NAME}: binary not installed (${err.message}); it will be downloaded on first run.`);
    });
}

module.exports = { assetName, binaryPath, ensureBinary, expectedSHA256 };
