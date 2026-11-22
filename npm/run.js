#!/usr/bin/env node
"use strict";

const { spawn } = require("child_process");
const fs = require("fs");
const { binaryPath, ensureBinary } = require("./postinstall");

// spawn (not execFileSync) so SIGINT/SIGTERM reach the long-running proxy and
// it can shut down cleanly.
function runBinary(bin) {
  const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });
  for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    process.on(sig, () => child.kill(sig));
  }
  child.on("error", (err) => {
    process.stderr.write(`nano-brain-observe: failed to execute ${bin}: ${err.message}\n`);
    process.exit(1);
  });
  child.on("exit", (code, signal) => process.exit(signal ? 1 : code));
}

const envBin = (process.env.NANO_BRAIN_OBSERVE_BIN || "").trim();
if (envBin) {
  if (!fs.existsSync(envBin)) {
    process.stderr.write(`nano-brain-observe: NANO_BRAIN_OBSERVE_BIN points to ${envBin}, which does not exist\n`);
    process.exit(1);
  }
  runBinary(envBin);
} else if (fs.existsSync(binaryPath())) {
  runBinary(binaryPath());
} else {
  // postinstall did not leave a binary (offline install, --ignore-scripts).
  process.stderr.write("nano-brain-observe: binary not present; downloading...\n");
  ensureBinary()
    .then(runBinary)
    .catch((err) => {
      process.stderr.write(`nano-brain-observe: could not obtain the binary: ${err.message}\n`);
      process.stderr.write("Set NANO_BRAIN_OBSERVE_BIN=/path/to/nano-brain-observe, or build from source: go build ./cmd/nano-brain-observe\n");
      process.exit(1);
    });
}
