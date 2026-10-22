// 版本准备与重试判断不依赖真实 registry，不执行 publish
'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');

test('release version rejects invalid tags and prerelease leading zeros', async () => {
  const { releaseVersion } = await import('../../scripts/npm-prepare-release.mjs');
  assert.equal(releaseVersion('v0.1.5'), '0.1.5');
  assert.equal(releaseVersion('v0.1.5-rc.1'), '0.1.5-rc.1');
  for (const tag of ['0.1.5', 'v01.1.5', 'v0.1', 'v0.1.5-01', 'v0.1.5-rc..1', 'v0.1.5/path', 'v0.1.5\n']) {
    assert.throws(() => releaseVersion(tag), /invalid/);
  }
});

test('prepare uses independent directory with exact dependencies and leaves sources untouched', async t => {
  const { prepare, packageNames, packageDirectory, mainPackage } = await import('../../scripts/npm-prepare-release.mjs');
  assert.deepEqual(packageNames, ['@aruing/aruing-darwin-arm64', '@aruing/aruing-darwin-x64', '@aruing/aruing-linux-x64', '@aruing/aruing']);
  assert.throws(() => packageDirectory('@other/aruing'), /unknown/);
  assert.throws(() => packageDirectory('@aruing/../aruing'), /unknown/);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-prepare-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const source = path.resolve(__dirname, '../aruing/package.json');
  const before = fs.readFileSync(source);
  for (const tag of ['v0.1.5', 'v0.1.6-rc.1']) {
    const out = path.join(dir, tag);
    const result = prepare(tag, out);
    assert.equal(result.tag, tag.includes('-') ? 'next' : 'latest');
    for (const name of packageNames) {
      const pkg = JSON.parse(fs.readFileSync(path.join(out, packageDirectory(name), 'package.json')));
      assert.equal(pkg.version, tag.slice(1));
      assert.equal(pkg.name, name);
      if (name === mainPackage) assert.deepEqual(pkg.optionalDependencies,
        Object.fromEntries(packageNames.filter(n => n !== mainPackage).map(n => [n, tag.slice(1)])));
      for (const file of pkg.files) assert.ok(fs.existsSync(path.join(out, packageDirectory(name), file)));
    }
    assert.throws(() => prepare(tag, out), /EEXIST/);
  }
  assert.deepEqual(fs.readFileSync(source), before);
  assert.throws(() => prepare('v0.1.5', undefined), /output/);
});

test('resuming a partial publish requires matching integrity and never downgrades latest', async () => {
  const { checkExisting } = await import('../../scripts/npm-publish.mjs');
  const metadata = { 'dist-tags': { latest: '0.1.5' }, versions: { '0.1.5': { dist: { integrity: 'sha512-identical' } } } };
  assert.equal(checkExisting(null, 'aruing', '0.1.5', 'sha512-identical', 'latest'), false);
  assert.equal(checkExisting(metadata, 'aruing', '0.1.5', 'sha512-identical', 'latest'), true);
  assert.throws(() => checkExisting(metadata, 'aruing', '0.1.5', 'sha512-different', 'latest'), /differs/);
  assert.throws(() => checkExisting(metadata, 'aruing', '0.1.4', 'sha512-identical', 'latest'), /downgrade/);
  assert.equal(checkExisting(metadata, 'aruing', '0.1.6-rc.1', 'sha512-new', 'next'), false);
  metadata['dist-tags'].latest = '0.1.4';
  assert.throws(() => checkExisting(metadata, 'aruing', '0.1.5', 'sha512-identical', 'latest'), /dist-tags/);
});

test('prepare rejects existing output directories without deleting caller files', async t => {
  const { prepare } = await import('../../scripts/npm-prepare-release.mjs');
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-existing-output-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const sentinel = path.join(directory, 'keep.txt');
  fs.writeFileSync(sentinel, 'caller-owned content');
  assert.throws(() => prepare('v0.1.5', directory), { code: 'EEXIST' });
  assert.equal(fs.readFileSync(sentinel, 'utf8'), 'caller-owned content');
  assert.deepEqual(fs.readdirSync(directory), ['keep.txt']);
});

// 模拟 registry 与命令执行边界，确保任何冲突在首次发布前暴露
test('publisher preflights every package before ordered publication and resumes identical versions', async t => {
  const { prepare, packageNames, mainPackage } = await import('../../scripts/npm-prepare-release.mjs');
  const { publishPackages } = await import('../../scripts/npm-publish.mjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-publish-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const out = path.join(dir, 'packages');
  prepare('v0.1.5', out);
  t.mock.method(console, 'log', () => {});
  const old = process.env.NODE_AUTH_TOKEN;
  process.env.NODE_AUTH_TOKEN = 'fixture-only';
  t.after(() => { if (old === undefined) delete process.env.NODE_AUTH_TOKEN; else process.env.NODE_AUTH_TOKEN = old; });
  const events = [], published = new Map();
  const command = (args, cwd) => {
    const name = JSON.parse(fs.readFileSync(path.join(cwd, 'package.json'))).name;
    if (args[0] === 'pack') return JSON.stringify([{ filename: 'fixture.tgz', integrity: `sha512-${name}` }]);
    if (args.includes('--dry-run') && published.has(name)) throw new Error('cannot publish over existing version, even in dry-run');
    events.push(`${args.includes('--dry-run') ? 'dry' : 'publish'}:${name}`);
    if (!args.includes('--dry-run')) published.set(name, {
      'dist-tags': { latest: '0.1.5' }, versions: { '0.1.5': { dist: { integrity: `sha512-${name}` } } },
    });
    return '';
  };
  await publishPackages(out, 'v0.1.5', true, { command, metadata: async n => published.get(n) });
  assert.deepEqual(events, [...packageNames.map(n => `dry:${n}`), ...packageNames.map(n => `publish:${n}`)]);
  events.length = 0;
  for (const name of packageNames.slice(1)) published.delete(name);
  await publishPackages(out, 'v0.1.5', true, { command, metadata: async n => published.get(n) });
  assert.deepEqual(events, [...packageNames.slice(1).map(n => `dry:${n}`), ...packageNames.slice(1).map(n => `publish:${n}`)]);
  events.length = 0;
  await publishPackages(out, 'v0.1.5', true, { command, metadata: async n => published.get(n) });
  assert.deepEqual(events, []);
  events.length = 0;
  published.get(mainPackage).versions['0.1.5'].dist.integrity = 'different';
  await assert.rejects(publishPackages(out, 'v0.1.5', true, { command, metadata: async n => published.get(n) }), /differs/);
  assert.ok(events.every(x => x.startsWith('dry:')));
});

// registry 边缘缓存滞后（v0.1.6 首发故障）：发布成功后立即可见的文档还是旧的，
// 单次查询会把「已发布但读不到」误判为失败并中断后续包
test('registry propagation lag is retried instead of aborting the run', async t => {
  const { prepare, packageNames } = await import('../../scripts/npm-prepare-release.mjs');
  const { publishPackages } = await import('../../scripts/npm-publish.mjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-lag-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const out = path.join(dir, 'packages');
  prepare('v0.1.5', out);
  t.mock.method(console, 'log', () => {});
  const old = process.env.NODE_AUTH_TOKEN;
  process.env.NODE_AUTH_TOKEN = 'fixture-only';
  t.after(() => { if (old === undefined) delete process.env.NODE_AUTH_TOKEN; else process.env.NODE_AUTH_TOKEN = old; });

  const published = new Map();
  const staleReads = new Map();
  const commands = [];
  const command = (args, cwd) => {
    const name = JSON.parse(fs.readFileSync(path.join(cwd, 'package.json'))).name;
    if (args[0] === 'pack') return JSON.stringify([{ filename: 'fixture.tgz', integrity: `sha512-${name}` }]);
    commands.push(`${args.includes('--dry-run') ? 'dry' : 'publish'}:${name}`);
    if (!args.includes('--dry-run')) {
      published.set(name, { 'dist-tags': { latest: '0.1.5' }, versions: { '0.1.5': { dist: { integrity: `sha512-${name}` } } } });
      staleReads.set(name, 2); // 发布后头两次查询仍返回旧文档
    }
    return '';
  };
  const metadata = async name => {
    const stale = staleReads.get(name);
    if (stale) {
      staleReads.set(name, stale - 1);
      return { 'dist-tags': { latest: '0.1.4' }, versions: { '0.1.4': { dist: { integrity: 'sha512-old' } } } };
    }
    return published.get(name);
  };
  const sleeps = [];
  await publishPackages(out, 'v0.1.5', true, { command, metadata, sleep: async ms => { sleeps.push(ms); } });

  assert.deepEqual(commands.filter(c => c.startsWith('publish:')), packageNames.map(n => `publish:${n}`));
  assert.equal(published.size, packageNames.length);
  assert.ok(sleeps.length >= packageNames.length * 2, 'confirmation should back off before concluding');
});

// 内容不同属于硬错误：不重试、不静默放过（重试不会自愈）
test('content mismatch fails fast without retry backoff', async t => {
  const { prepare } = await import('../../scripts/npm-prepare-release.mjs');
  const { publishPackages } = await import('../../scripts/npm-publish.mjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-mismatch-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const out = path.join(dir, 'packages');
  prepare('v0.1.5', out);
  t.mock.method(console, 'log', () => {});
  const old = process.env.NODE_AUTH_TOKEN;
  process.env.NODE_AUTH_TOKEN = 'fixture-only';
  t.after(() => { if (old === undefined) delete process.env.NODE_AUTH_TOKEN; else process.env.NODE_AUTH_TOKEN = old; });

  const command = (args, cwd) => {
    const name = JSON.parse(fs.readFileSync(path.join(cwd, 'package.json'))).name;
    if (args[0] === 'pack') return JSON.stringify([{ filename: 'fixture.tgz', integrity: 'sha512-other' }]);
    return '';
  };
  const metadata = async () => ({ 'dist-tags': { latest: '0.1.5' }, versions: { '0.1.5': { dist: { integrity: 'sha512-published' } } } });
  const sleeps = [];
  await assert.rejects(
    publishPackages(out, 'v0.1.5', true, { command, metadata, sleep: async ms => { sleeps.push(ms); } }),
    /differs/,
  );
  assert.deepEqual(sleeps, [], 'hard conflicts must not be retried');
});

// 发布命令超时被杀不代表没发出去：先查 registry，已落地就不再重复发
test('publish timeout that already landed is not published twice', async t => {
  const { prepare, packageNames } = await import('../../scripts/npm-prepare-release.mjs');
  const { publishPackages } = await import('../../scripts/npm-publish.mjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-timeout-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const out = path.join(dir, 'packages');
  prepare('v0.1.5', out);
  t.mock.method(console, 'log', () => {});
  const old = process.env.NODE_AUTH_TOKEN;
  process.env.NODE_AUTH_TOKEN = 'fixture-only';
  t.after(() => { if (old === undefined) delete process.env.NODE_AUTH_TOKEN; else process.env.NODE_AUTH_TOKEN = old; });

  const published = new Map();
  const publishes = [];
  const command = (args, cwd, options = {}) => {
    const name = JSON.parse(fs.readFileSync(path.join(cwd, 'package.json'))).name;
    if (args[0] === 'pack') return JSON.stringify([{ filename: 'fixture.tgz', integrity: `sha512-${name}` }]);
    if (args.includes('--dry-run')) return '';
    publishes.push(name);
    // 首个包：上传成功但本地超时被杀（v0.1.6 第二次失败形态）
    published.set(name, { 'dist-tags': { latest: '0.1.5' }, versions: { '0.1.5': { dist: { integrity: `sha512-${name}` } } } });
    if (publishes.length === 1) {
      assert.equal(options.timeoutMs, 600000, 'publish must use the long timeout');
      throw new Error('npm publish failed: spawnSync npm ETIMEDOUT');
    }
    return '';
  };
  const metadata = async name => published.get(name);
  await publishPackages(out, 'v0.1.5', true, { command, metadata, sleep: async () => {} });
  assert.deepEqual(publishes, packageNames, 'each package published exactly once');
});

// 发布超时但已落地、且 dist-tag 还没跟上：不能重发（npm 拒绝既有版本），
// 要按「已落地」处理并交给确认阶段等 tag 追平（pr-agent 采纳项）
test('publish timeout with stale dist-tag is not republished', async t => {
  const { prepare, packageNames } = await import('../../scripts/npm-prepare-release.mjs');
  const { publishPackages } = await import('../../scripts/npm-publish.mjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-stale-tag-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const out = path.join(dir, 'packages');
  prepare('v0.1.5', out);
  t.mock.method(console, 'log', () => {});
  const old = process.env.NODE_AUTH_TOKEN;
  process.env.NODE_AUTH_TOKEN = 'fixture-only';
  t.after(() => { if (old === undefined) delete process.env.NODE_AUTH_TOKEN; else process.env.NODE_AUTH_TOKEN = old; });

  const published = new Map();
  const tagLag = new Map(); // 版本已在、dist-tag 尚未跟上
  const publishes = [];
  const command = (args, cwd) => {
    const name = JSON.parse(fs.readFileSync(path.join(cwd, 'package.json'))).name;
    if (args[0] === 'pack') return JSON.stringify([{ filename: 'fixture.tgz', integrity: `sha512-${name}` }]);
    if (args.includes('--dry-run')) return '';
    publishes.push(name);
    published.set(name, { versions: { '0.1.5': { dist: { integrity: `sha512-${name}` } } } });
    tagLag.set(name, 2);
    if (publishes.length === 1) throw new Error('npm publish failed: spawnSync npm ETIMEDOUT');
    return '';
  };
  const metadata = async name => {
    const doc = published.get(name);
    if (!doc) return null;
    const lag = tagLag.get(name) ?? 0;
    if (lag > 0) {
      tagLag.set(name, lag - 1);
      return { 'dist-tags': { latest: '0.1.4' }, versions: doc.versions };
    }
    return { 'dist-tags': { latest: '0.1.5' }, versions: doc.versions };
  };
  await publishPackages(out, 'v0.1.5', true, { command, metadata, sleep: async () => {} });
  assert.deepEqual(publishes, packageNames, 'landed versions must not be republished because the tag lags');
});
