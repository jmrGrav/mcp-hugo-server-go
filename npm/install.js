'use strict';

// postinstall: downloads the precompiled Go binary matching this package's
// own version from GitHub Releases (esbuild/ripgrep pattern) — there is no
// Go build logic duplicated here, this package is a thin fetch-and-exec
// shim around the real binary built by .goreleaser.yaml / release.yml.
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { resolveAssetName, binaryFileName } = require('./platform.js');

const REPO = 'jmrGrav/mcp-hugo-server-go';
const BIN_DIR = path.join(__dirname, 'bin');

// Overridable only for tests (a local fixture server) — real installs
// always use the default GitHub Releases base.
function releaseBaseURL(version, envOverride = process.env.MCP_HUGO_NPM_BASE_URL) {
  if (envOverride) return envOverride;
  return `https://github.com/${REPO}/releases/download/v${version}`;
}

async function download(url, { redirects = 5, fetchImpl = fetch } = {}) {
  const res = await fetchImpl(url, { redirect: 'manual' });
  if (res.status >= 300 && res.status < 400 && res.headers.get('location')) {
    if (redirects <= 0) throw new Error(`too many redirects fetching ${url}`);
    return download(res.headers.get('location'), { redirects: redirects - 1, fetchImpl });
  }
  if (!res.ok) {
    throw new Error(`GET ${url} failed: ${res.status} ${res.statusText}`);
  }
  return Buffer.from(await res.arrayBuffer());
}

function sha256(buf) {
  return crypto.createHash('sha256').update(buf).digest('hex');
}

// checksums.txt lines look like: "<sha256>  <asset_name>"
function expectedChecksum(checksumsText, assetName) {
  for (const line of checksumsText.split('\n')) {
    const parts = line.trim().split(/\s+/);
    if (parts.length === 2 && parts[1] === assetName) return parts[0];
  }
  throw new Error(`checksums.txt has no entry for ${assetName}`);
}

async function installFrom(base, { platform = process.platform, arch = process.arch, fetchImpl } = {}) {
  const assetName = resolveAssetName(platform, arch);

  console.log(`mcp-hugo-server-go: downloading ${assetName}...`);
  const [binary, checksumsText] = await Promise.all([
    download(`${base}/${assetName}`, { fetchImpl }),
    download(`${base}/checksums.txt`, { fetchImpl }).then((buf) => buf.toString('utf8')),
  ]);

  const want = expectedChecksum(checksumsText, assetName);
  const got = sha256(binary);
  if (want !== got) {
    throw new Error(
      `mcp-hugo-server-go: checksum mismatch for ${assetName} — expected ${want}, got ${got}. Refusing to install a corrupted or tampered binary.`
    );
  }

  fs.mkdirSync(BIN_DIR, { recursive: true });
  const dest = path.join(BIN_DIR, binaryFileName(platform));
  fs.writeFileSync(dest, binary, { mode: 0o755 });
  if (platform !== 'win32') {
    fs.chmodSync(dest, 0o755);
  }
  console.log(`mcp-hugo-server-go: installed to ${dest}`);
  return dest;
}

async function main() {
  const version = require('./package.json').version;
  await installFrom(releaseBaseURL(version));
}

if (require.main === module) {
  main().catch((err) => {
    console.error(err.message || err);
    process.exit(1);
  });
}

module.exports = { releaseBaseURL, download, sha256, expectedChecksum, installFrom };
