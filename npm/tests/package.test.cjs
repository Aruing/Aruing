// 离线验证安装失败边界与真实 launcher 子进程，不访问 registry 或真实集群
'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { once } = require('node:events');
const { createHash } = require('node:crypto');
const { spawn, spawnSync } = require('node:child_process');
const { install, checksumFor, download } = require('../installer.cjs');
const { platformPackage } = require('../aruing/bin/aruing.js');
const root = path.resolve(__dirname, '..');

// 每例独享目录，失败也回收；不接触用户全局安装
function temporary(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-npm-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// 测试归档使用真实 tar，文件内容只承担退出行为
function fixture(t, { link = false } = {}) {
  const dir = temporary(t);
  const content = path.join(dir, 'content');
  fs.mkdirSync(content);
  if (link) fs.symlinkSync('/bin/sh', path.join(content, 'aruing'));
  else fs.writeFileSync(path.join(content, 'aruing'), '#!/bin/sh\nprintf "fixture-version\\n"\n', { mode: 0o755 });
  const archive = path.join(dir, 'archive.tar.gz');
  const result = spawnSync('tar', ['-czf', archive, '-C', content, 'aruing']);
  assert.equal(result.status, 0, result.stderr.toString());
  const pkg = path.join(dir, 'package');
  fs.mkdirSync(pkg);
  fs.writeFileSync(path.join(pkg, 'package.json'), JSON.stringify({ name: 'aruing-darwin-arm64', version: '0.1.5' }));
  return { dir, pkg, bytes: fs.readFileSync(archive) };
}

// 将网络入口映射到本机 HTTP fixture，其余下载控制逻辑保持真实
async function server(t, handler) {
  const srv = http.createServer(handler);
  srv.listen(0, '127.0.0.1');
  await once(srv, 'listening');
  t.after(() => { srv.closeAllConnections(); srv.close(); });
  const fetch = global.fetch;
  t.mock.method(global, 'fetch', (url, options) => fetch(`http://127.0.0.1:${srv.address().port}${new URL(url).pathname}`, options));
}

test('checksum uses exact filename and rejects missing/duplicate entries', () => {
  const hash = 'a'.repeat(64);
  assert.equal(checksumFor(`${hash}  aruing.tar.gz\r\n`, 'aruing.tar.gz'), hash);
  assert.throws(() => checksumFor(`${hash}  other-aruing.tar.gz`, 'aruing.tar.gz'));
  assert.throws(() => checksumFor(`${hash}  x\n${hash}  x`, 'x'));
});

test('downloads redirects and installs only verified binary with executable permissions', async t => {
  const f = fixture(t);
  const hash = createHash('sha256').update(f.bytes).digest('hex');
  const seen = [];
  await server(t, (req, res) => {
    seen.push(req.url);
    if (req.url.endsWith('checksums.txt')) res.end(`${hash}  aruing_darwin_arm64.tar.gz\n`);
    else if (req.url.endsWith('.tar.gz')) { res.writeHead(302, { Location: 'https://fixture.test/asset' }); res.end(); }
    else res.end(f.bytes);
  });
  await install(f.pkg);
  assert.ok(seen[0].includes('/v0.1.5/'));
  const binary = path.join(f.pkg, 'bin/aruing');
  assert.equal(spawnSync(binary, { encoding: 'utf8' }).stdout.trim(), 'fixture-version');
  assert.ok(fs.statSync(binary).mode & 0o111);
  assert.deepEqual(fs.readdirSync(path.join(f.pkg, 'bin')), ['aruing']);
});

test('checksum mismatch preserves existing binary and clears temporary files', async t => {
  const f = fixture(t);
  fs.mkdirSync(path.join(f.pkg, 'bin'));
  fs.writeFileSync(path.join(f.pkg, 'bin/aruing'), 'previous');
  await assert.rejects(install(f.pkg, async url => url.endsWith('checksums.txt')
    ? Buffer.from(`${'0'.repeat(64)}  aruing_darwin_arm64.tar.gz`) : f.bytes), /checksum mismatch/);
  assert.equal(fs.readFileSync(path.join(f.pkg, 'bin/aruing'), 'utf8'), 'previous');
  assert.deepEqual(fs.readdirSync(path.join(f.pkg, 'bin')), ['aruing']);
});

test('bad archive and symlink cannot become installed binary', async t => {
  for (const link of [false, true]) {
    const f = fixture(t, { link });
    const bytes = link ? f.bytes : Buffer.from('not gzip');
    const hash = createHash('sha256').update(bytes).digest('hex');
    await assert.rejects(install(f.pkg, async url => url.endsWith('checksums.txt')
      ? Buffer.from(`${hash}  aruing_darwin_arm64.tar.gz`) : bytes), /archive/);
    assert.deepEqual(fs.readdirSync(path.join(f.pkg, 'bin')), []);
  }
});

test('HTTP errors, missing checksums and network failures do not leave binaries', async t => {
  const f = fixture(t);
  await server(t, (_req, res) => { res.writeHead(404); res.end(); });
  await assert.rejects(install(f.pkg), /HTTP 404/);
  await assert.rejects(install(f.pkg, async () => Buffer.from('no checksums')), /checksum/);
  await assert.rejects(install(f.pkg, async () => { throw new Error('network unavailable'); }), /network unavailable/);
  assert.deepEqual(fs.readdirSync(path.join(f.pkg, 'bin')), []);
});

test('download rejects downgrade, redirect loops and slow responses', async t => {
  await server(t, (req, res) => {
    if (req.url === '/slow') return;
    res.writeHead(302, { Location: req.url === '/downgrade' ? 'http://fixture.test/file' : 'https://fixture.test/loop' });
    res.end();
  });
  await assert.rejects(download('https://fixture.test/downgrade'), /HTTPS/);
  await assert.rejects(download('https://fixture.test/loop'), /redirects/);
  await assert.rejects(download('https://fixture.test/slow', 30), /timeout/i);
});

test('platform packages use Node CPU names and generated installers stay identical', () => {
  for (const [platform, arch] of [['darwin', 'arm64'], ['darwin', 'x64'], ['linux', 'x64']]) {
    const name = platformPackage(platform, arch);
    assert.equal(name, `aruing-${platform}-${arch}`);
    assert.deepEqual(fs.readFileSync(path.join(root, name, 'install.js')), fs.readFileSync(path.join(root, 'installer.cjs')));
  }
  assert.throws(() => platformPackage('win32', 'x64'), /unsupported/);
  assert.throws(() => platformPackage('linux', 'arm64'), /unsupported/);
});

// 组装真实包布局，使模块解析、进程退出与缺失依赖都走产品入口
function launcher(t, script) {
  const dir = temporary(t);
  const main = path.join(dir, 'node_modules/aruing');
  const platform = path.join(dir, 'node_modules', platformPackage());
  fs.cpSync(path.join(root, 'aruing'), main, { recursive: true });
  fs.mkdirSync(path.join(platform, 'bin'), { recursive: true });
  fs.writeFileSync(path.join(platform, 'package.json'), JSON.stringify({ version: '0.1.5' }));
  fs.writeFileSync(path.join(platform, 'bin/aruing'), `#!${process.execPath}\n${script}`, { mode: 0o755 });
  return { cli: path.join(main, 'bin/aruing.js'), platform };
}

test('launcher forwards arguments, standard streams and nonzero exit code', t => {
  const { cli } = launcher(t, 'process.stdin.pipe(process.stdout); console.error(JSON.stringify(process.argv.slice(2))); process.exitCode = 7;');
  const result = spawnSync(process.execPath, [cli, 'argument with spaces', '--flag'], { input: 'input', encoding: 'utf8' });
  assert.equal(result.status, 7);
  assert.equal(result.stdout, 'input');
  assert.deepEqual(JSON.parse(result.stderr), ['argument with spaces', '--flag']);
});

test('launcher reports missing binaries, mismatched versions and omitted optional dependencies', t => {
  const { cli, platform } = launcher(t, '');
  fs.rmSync(path.join(platform, 'bin/aruing'));
  let result = spawnSync(process.execPath, [cli], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /ignore-scripts=false/);
  fs.writeFileSync(path.join(platform, 'package.json'), '{"version":"0.1.4"}');
  result = spawnSync(process.execPath, [cli], { encoding: 'utf8' });
  assert.match(result.stderr, /does not match/);
  fs.rmSync(platform, { recursive: true });
  result = spawnSync(process.execPath, [cli], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /include=optional/);
});

test('launcher forwards cancellation and preserves signal termination', { timeout: 10000 }, async t => {
  const { cli } = launcher(t, 'console.log("ready"); setInterval(() => {}, 1000);');
  const child = spawn(process.execPath, [cli], { stdio: ['ignore', 'pipe', 'pipe'] });
  t.after(() => { if (child.exitCode === null) child.kill('SIGKILL'); });
  await once(child.stdout, 'data');
  child.kill('SIGTERM');
  const [code, signal] = await once(child, 'exit');
  assert.equal(code, null);
  assert.equal(signal, 'SIGTERM');
});
