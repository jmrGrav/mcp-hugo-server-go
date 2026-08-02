'use strict';

// Maps Node's process.platform/process.arch to the exact GitHub Release
// asset name GoReleaser produces (see ../.goreleaser.yaml's archives
// name_template: "{{ .Binary }}_{{ .Os }}_{{ .Arch }}", format: binary).
// Verified against a real `goreleaser release --snapshot --clean
// --skip=publish` run — the uploaded asset names are exactly
// mcp-hugo-server-go_{os}_{arch}[.exe], not the dist/ folder names (which
// also carry a GOAMD64/GOARM64 microarchitecture suffix goreleaser strips
// for the archive name).
const PLATFORM_MAP = {
  'darwin-x64': 'darwin_amd64',
  'darwin-arm64': 'darwin_arm64',
  'linux-x64': 'linux_amd64',
  'linux-arm64': 'linux_arm64',
  'win32-x64': 'windows_amd64',
};

function resolveAssetName(platform, arch) {
  const key = `${platform}-${arch}`;
  const suffix = PLATFORM_MAP[key];
  if (!suffix) {
    const supported = Object.keys(PLATFORM_MAP).sort().join(', ');
    throw new Error(
      `mcp-hugo-server-go: no precompiled binary for platform "${platform}" arch "${arch}". ` +
        `Supported: ${supported}. Build from source instead: ` +
        'https://github.com/jmrGrav/mcp-hugo-server-go#installation'
    );
  }
  const ext = platform === 'win32' ? '.exe' : '';
  return `mcp-hugo-server-go_${suffix}${ext}`;
}

function binaryFileName(platform) {
  return platform === 'win32' ? 'mcp-hugo-server-go.exe' : 'mcp-hugo-server-go';
}

module.exports = { resolveAssetName, binaryFileName, PLATFORM_MAP };
