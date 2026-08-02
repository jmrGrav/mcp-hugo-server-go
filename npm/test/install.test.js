'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const os = require('node:os');
const fs = require('node:fs');
const path = require('node:path');
const { expectedChecksum, sha256, installFrom, download } = require('../install.js');

test('expectedChecksum finds the matching line, ignores others', () => {
  const text = [
    'aaa11  mcp-hugo-server-go_darwin_amd64',
    'bbb22  mcp-hugo-server-go_linux_amd64',
  ].join('\n');
  assert.equal(expectedChecksum(text, 'mcp-hugo-server-go_linux_amd64'), 'bbb22');
});

test('expectedChecksum throws when the asset has no entry', () => {
  assert.throws(() => expectedChecksum('aaa11  something_else', 'mcp-hugo-server-go_linux_amd64'), /no entry/);
});

// End-to-end against a real local HTTP server — not a mocked fetch — so
// this actually exercises install.js's redirect-following and streaming
// download path, not just the pure-function helpers.
test('installFrom downloads, verifies checksum, and writes an executable binary', async (t) => {
  const fakeBinary = Buffer.from('#!/bin/sh\necho fake-binary\n');
  const checksum = sha256(fakeBinary);
  const assetName = 'mcp-hugo-server-go_linux_amd64';
  const checksumsText = `${checksum}  ${assetName}\n`;

  const server = http.createServer((req, res) => {
    if (req.url === `/${assetName}`) {
      res.writeHead(200);
      res.end(fakeBinary);
    } else if (req.url === '/checksums.txt') {
      res.writeHead(200);
      res.end(checksumsText);
    } else if (req.url === '/redirect-me') {
      res.writeHead(302, { Location: `/${assetName}` });
      res.end();
    } else {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const base = `http://127.0.0.1:${server.address().port}`;

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mcp-hugo-npm-test-'));
  t.after(() => fs.rmSync(tmpDir, { recursive: true, force: true }));

  // installFrom writes to <install.js's own dir>/bin, which isn't
  // overridable by design (it must match where bin/mcp-hugo-server-go.js
  // looks) — so this test only asserts on the returned dest path's content,
  // then cleans that real bin/ dir back up itself rather than redirecting
  // the write location.
  const dest = await installFrom(base, { platform: 'linux', arch: 'x64' });
  t.after(() => fs.rmSync(dest, { force: true }));

  assert.ok(fs.existsSync(dest));
  assert.deepEqual(fs.readFileSync(dest), fakeBinary);
  const mode = fs.statSync(dest).mode & 0o777;
  assert.equal(mode, 0o755);
});

test('installFrom rejects a tampered binary (checksum mismatch)', async (t) => {
  const realBinary = Buffer.from('real');
  const tamperedBinary = Buffer.from('tampered');
  const assetName = 'mcp-hugo-server-go_linux_amd64';
  const checksumsText = `${sha256(realBinary)}  ${assetName}\n`;

  const server = http.createServer((req, res) => {
    if (req.url === `/${assetName}`) {
      res.writeHead(200);
      res.end(tamperedBinary); // server serves something that doesn't match checksums.txt
    } else if (req.url === '/checksums.txt') {
      res.writeHead(200);
      res.end(checksumsText);
    } else {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const base = `http://127.0.0.1:${server.address().port}`;

  await assert.rejects(() => installFrom(base, { platform: 'linux', arch: 'x64' }), /checksum mismatch/);
});

// A Socket.dev supply-chain scan of the published package flagged
// download()'s redirect-following as having no destination allowlist —
// legitimate finding, since an unrestricted redirect could be steered to
// a host that serves both a malicious binary and a matching
// checksums.txt, defeating the checksum check entirely. These two tests
// prove the fix actually works both ways: rejects an untrusted redirect
// target by default, and still follows a redirect when the target is
// explicitly allowed.
test('download rejects a redirect to a host outside the default allowlist', async (t) => {
  const server = http.createServer((req, res) => {
    if (req.url === '/redirect-me') {
      res.writeHead(302, { Location: 'http://127.0.0.1:1/elsewhere' });
      res.end();
    } else {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const url = `http://127.0.0.1:${server.address().port}/redirect-me`;

  await assert.rejects(() => download(url), /refusing to follow redirect/);
});

test('download follows a redirect to an explicitly allowed host', async (t) => {
  const payload = Buffer.from('redirected-payload');
  const server = http.createServer((req, res) => {
    if (req.url === '/redirect-me') {
      res.writeHead(302, { Location: `http://127.0.0.1:${server.address().port}/final` });
      res.end();
    } else if (req.url === '/final') {
      res.writeHead(200);
      res.end(payload);
    } else {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const url = `http://127.0.0.1:${server.address().port}/redirect-me`;

  const result = await download(url, { allowedRedirectHosts: /^127\.0\.0\.1$/, requireHTTPS: false });
  assert.deepEqual(result, payload);
});
