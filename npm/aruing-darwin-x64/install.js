// 平台包安装器的唯一源码；同步脚本将它复制进各平台包，发布包运行时不依赖仓库
'use strict';

const { createHash } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

// 完整匹配资产文件名，拒绝缺失或重复的校验记录
function checksumFor(text, asset) {
  const hashes = text.split(/\r?\n/).flatMap(line => {
    const match = line.match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/);
    return match && match[2] === asset ? [match[1].toLowerCase()] : [];
  });
  if (hashes.length !== 1) throw new Error(`expected one checksum for ${asset}`);
  return hashes[0];
}

// 网络与响应体共用超时，重定向不得从加密连接降为明文
async function download(url, timeoutMs = 120000) {
  const signal = AbortSignal.timeout(timeoutMs);
  for (let redirects = 0; redirects <= 5; redirects++) {
    if (new URL(url).protocol !== 'https:') throw new Error('download requires HTTPS');
    const response = await fetch(url, { signal, redirect: 'manual' });
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      const location = response.headers.get('location');
      await response.body?.cancel();
      if (!location) throw new Error('redirect without Location');
      url = new URL(location, url).href;
      continue;
    }
    if (!response.ok) {
      await response.body?.cancel();
      throw new Error(`download HTTP ${response.status}: ${url}`);
    }
    return Buffer.from(await response.arrayBuffer());
  }
  throw new Error('too many download redirects');
}

// 只向文件描述符输出指定的普通文件，不将归档路径解压到文件系统
function extractBinary(archive, destination) {
  const listing = spawnSync('tar', ['-tvzf', archive], { encoding: 'utf8', timeout: 30000 });
  if (listing.error || listing.status !== 0) {
    throw new Error(`cannot list release archive: ${listing.error?.message || listing.stderr}`);
  }
  const entries = listing.stdout.trim().split('\n').filter(line => /\s(?:\.\/)?aruing$/.test(line));
  if (entries.length !== 1 || !entries[0].startsWith('-')) {
    throw new Error('archive must contain exactly one regular aruing binary');
  }
  const name = entries[0].endsWith(' ./aruing') ? './aruing' : 'aruing';
  const fd = fs.openSync(destination, 'wx', 0o755);
  try {
    const result = spawnSync('tar', ['-xOzf', archive, name], {
      stdio: ['ignore', fd, 'pipe'], encoding: 'utf8', timeout: 30000,
    });
    if (result.error || result.status !== 0) {
      throw new Error(`cannot extract aruing: ${result.error?.message || result.stderr}`);
    }
    if (!fs.fstatSync(fd).size) throw new Error('release binary is empty');
    fs.fchmodSync(fd, 0o755);
  } finally {
    fs.closeSync(fd);
  }
}

// 默认只能读取包版本对应的官方资产；下载函数仅供离线测试注入
async function install(packageDir, fetchBytes = download) {
  const pkg = JSON.parse(fs.readFileSync(path.join(packageDir, 'package.json'), 'utf8'));
  const assets = {
    '@aruing/aruing-darwin-arm64': 'aruing_darwin_arm64.tar.gz',
    '@aruing/aruing-darwin-x64': 'aruing_darwin_amd64.tar.gz',
    '@aruing/aruing-linux-x64': 'aruing_linux_amd64.tar.gz',
  };
  const asset = assets[pkg.name];
  if (!asset || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(pkg.version)) {
    throw new Error('invalid platform package name or version');
  }
  const base = `https://github.com/Aruing/Aruing/releases/download/v${pkg.version}`;
  const binDir = path.join(packageDir, 'bin');
  fs.mkdirSync(binDir, { recursive: true });
  const temporary = fs.mkdtempSync(path.join(binDir, '.install-'));
  try {
    const expected = checksumFor((await fetchBytes(`${base}/checksums.txt`)).toString('utf8'), asset);
    const bytes = await fetchBytes(`${base}/${asset}`);
    if (createHash('sha256').update(bytes).digest('hex') !== expected) {
      throw new Error(`checksum mismatch for ${asset}; refusing to install`);
    }
    const archive = path.join(temporary, 'release.tar.gz');
    const binary = path.join(temporary, 'aruing');
    fs.writeFileSync(archive, bytes);
    extractBinary(archive, binary);
    fs.renameSync(binary, path.join(binDir, 'aruing'));
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}

module.exports = { checksumFor, download, extractBinary, install };
if (require.main === module) {
  install(__dirname).catch(error => {
    console.error(`aruing install: ${error.message}\nRetry: npm install -g @aruing/aruing --foreground-scripts`);
    process.exitCode = 1;
  });
}
