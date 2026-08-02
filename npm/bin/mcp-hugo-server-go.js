#!/usr/bin/env node
'use strict';

// Thin exec shim — the real work happens in the binary install.js downloaded
// into this same directory. stdio is inherited untouched so the stdio MCP
// transport (JSON-RPC over stdin/stdout) passes through byte-for-byte; the
// binary's own exit code is propagated as this process's exit code.
const path = require('path');
const { spawnSync } = require('child_process');
const { binaryFileName } = require('../platform.js');

const binPath = path.join(__dirname, binaryFileName(process.platform));
const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit' });

if (result.error) {
  if (result.error.code === 'ENOENT') {
    console.error(
      `mcp-hugo-server-go: binary not found at ${binPath}. ` +
        'The postinstall download may have failed — try reinstalling this package.'
    );
  } else {
    console.error(result.error.message);
  }
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
