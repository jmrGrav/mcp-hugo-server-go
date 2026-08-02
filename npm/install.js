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

function releaseBaseURL(version) {
  return `https://github.com/${REPO}/releases/download/v${version}`;
}

// GitHub redirects release-asset downloads to its object storage CDN
// (objects.githubusercontent.com) before serving the actual bytes. A
// redirect-following download with no destination check would let
// anything in the chain — a compromised proxy, a DNS hijack — silently
// redirect this to an attacker-controlled host that also serves a
// checksums.txt matching its own malicious binary (the SHA-256 check
// alone can't defend against that, since it only proves the binary
// matches whatever checksums.txt says — not that checksums.txt itself
// came from GitHub). Restricting redirects to GitHub's own hosts closes
// that gap.
const ALLOWED_REDIRECT_HOSTS = /(^|\.)github\.com$|(^|\.)githubusercontent\.com$/;

async function download(
  url,
  { redirects = 5, fetchImpl = fetch, allowedRedirectHosts = ALLOWED_REDIRECT_HOSTS, requireHTTPS = true } = {}
) {
  const res = await fetchImpl(url, { redirect: 'manual' });
  if (res.status >= 300 && res.status < 400 && res.headers.get('location')) {
    if (redirects <= 0) throw new Error(`too many redirects fetching ${url}`);
    const location = new URL(res.headers.get('location'), url);
    if ((requireHTTPS && location.protocol !== 'https:') || !allowedRedirectHosts.test(location.hostname)) {
      throw new Error(
        `mcp-hugo-server-go: refusing to follow redirect from ${url} to untrusted host ${location.hostname} — expected an https://*.github.com or https://*.githubusercontent.com URL.`
      );
    }
    return download(location.href, { redirects: redirects - 1, fetchImpl, allowedRedirectHosts, requireHTTPS });
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
