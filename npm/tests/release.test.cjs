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
  const { prepare, packageNames } = await import('../../scripts/npm-prepare-release.mjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-prepare-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const source = path.resolve(__dirname, '../aruing/package.json');
  const before = fs.readFileSync(source);
  for (const tag of ['v0.1.5', 'v0.1.6-rc.1']) {
    const out = path.join(dir, tag);
    const result = prepare(tag, out);
    assert.equal(result.tag, tag.includes('-') ? 'next' : 'latest');
    for (const name of packageNames) {
      const pkg = JSON.parse(fs.readFileSync(path.join(out, name, 'package.json')));
      assert.equal(pkg.version, tag.slice(1));
      assert.equal(pkg.name, name);
      if (name === 'aruing') assert.deepEqual(Object.values(pkg.optionalDependencies), Array(3).fill(tag.slice(1)));
      for (const file of pkg.files) assert.ok(fs.existsSync(path.join(out, name, file)));
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

// 模拟 registry 与命令执行边界，确保任何冲突在首次发布前暴露
test('publisher preflights every package before ordered publication and resumes identical versions', async t => {
  const { prepare, packageNames } = await import('../../scripts/npm-prepare-release.mjs');
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
    const name = path.basename(cwd);
    if (args[0] === 'pack') return JSON.stringify([{ filename: 'fixture.tgz', integrity: `sha512-${name}` }]);
    events.push(`${args.includes('--dry-run') ? 'dry' : 'publish'}:${name}`);
    if (!args.includes('--dry-run')) published.set(name, {
      'dist-tags': { latest: '0.1.5' }, versions: { '0.1.5': { dist: { integrity: `sha512-${name}` } } },
    });
    return '';
  };
  await publishPackages(out, 'v0.1.5', true, { command, metadata: async n => published.get(n) });
  assert.deepEqual(events, [...packageNames.map(n => `dry:${n}`), ...packageNames.map(n => `publish:${n}`)]);
  events.length = 0;
  await publishPackages(out, 'v0.1.5', true, { command, metadata: async n => published.get(n) });
  assert.deepEqual(events, packageNames.map(n => `dry:${n}`));
  events.length = 0;
  published.get('aruing').versions['0.1.5'].dist.integrity = 'different';
  await assert.rejects(publishPackages(out, 'v0.1.5', true, { command, metadata: async n => published.get(n) }), /differs/);
  assert.ok(events.every(x => x.startsWith('dry:')));
});
