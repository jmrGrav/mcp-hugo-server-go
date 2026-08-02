'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { resolveAssetName, binaryFileName } = require('../platform.js');

// Every entry here is verified against a real `goreleaser release
// --snapshot --clean --skip=publish` run's uploaded asset names (see
// platform.js's own comment) — not guessed from the dist/ folder names,
// which carry an extra microarchitecture suffix goreleaser strips before
// upload.
const CASES = [
  ['darwin', 'x64', 'mcp-hugo-server-go_darwin_amd64'],
  ['darwin', 'arm64', 'mcp-hugo-server-go_darwin_arm64'],
  ['linux', 'x64', 'mcp-hugo-server-go_linux_amd64'],
  ['linux', 'arm64', 'mcp-hugo-server-go_linux_arm64'],
  ['win32', 'x64', 'mcp-hugo-server-go_windows_amd64.exe'],
];

test('resolveAssetName matches real GoReleaser asset names', () => {
  for (const [platform, arch, want] of CASES) {
    assert.equal(resolveAssetName(platform, arch), want);
  }
});

test('resolveAssetName rejects an unsupported platform/arch', () => {
  assert.throws(() => resolveAssetName('freebsd', 'x64'), /no precompiled binary/);
  assert.throws(() => resolveAssetName('win32', 'arm64'), /no precompiled binary/);
});

test('binaryFileName appends .exe only on win32', () => {
  assert.equal(binaryFileName('win32'), 'mcp-hugo-server-go.exe');
  assert.equal(binaryFileName('linux'), 'mcp-hugo-server-go');
  assert.equal(binaryFileName('darwin'), 'mcp-hugo-server-go');
});
