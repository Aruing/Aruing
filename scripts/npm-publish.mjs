// 先检查全部包再发布；仅跳过内容完全相同的已发布版本，主包始终最后处理
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { packageNames, packageDirectory, mainPackage, releaseVersion } from './npm-prepare-release.mjs';

// 任何 npm 命令失败立即中止，不吞掉认证错误或 registry 错误
function npm(args, cwd) {
  const result = spawnSync('npm', args, { cwd, encoding: 'utf8', timeout: 120000 });
  if (result.error || result.status !== 0) throw new Error(`npm ${args[0]} failed: ${result.error?.message || result.stderr}`);
  return result.stdout;
}

// 对稳定版本比较三个数字段，阻止旧 release 重新指向 latest
function newerStable(remote, local) {
  if (!/^\d+\.\d+\.\d+$/.test(remote || '')) return false;
  const a = remote.split('.').map(BigInt), b = local.split('.').map(BigInt);
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] > b[i];
  return false;
}

// 只有精确版本完整性相等才可续跑；同名不同内容绝不覆盖或忽略
export function checkExisting(metadata, name, version, integrity, tag) {
  if (tag === 'latest' && newerStable(metadata?.['dist-tags']?.latest, version)) {
    throw new Error(`${name}: newer stable release already exists; refusing to downgrade latest`);
  }
  const existing = metadata?.versions?.[version];
  if (!existing) return false;
  if (existing.dist?.integrity !== integrity) throw new Error(`${name}@${version}: published content differs`);
  if (metadata['dist-tags']?.[tag] !== version) {
    throw new Error(`${name}@${version}: content matches but ${tag} differs; maintainer must review dist-tags`);
  }
  return true;
}

// 读取公开 registry；仅 404 表示未发布，其余错误都应在修改 registry 前暴露
async function metadataFor(name) {
  const response = await fetch(`https://registry.npmjs.org/${encodeURIComponent(name)}`, { signal: AbortSignal.timeout(30000) });
  if (response.status === 404) return null;
  if (!response.ok) throw new Error(`registry HTTP ${response.status} for ${name}`);
  return response.json();
}

// 所有包先完成打包、演练和冲突检查；默认仅演练，显式发布仅供正式工作流调用
export async function publishPackages(directory, tag, execute = false, { command = npm, metadata = metadataFor } = {}) {
  const version = releaseVersion(tag);
  const distTag = version.includes('-') ? 'next' : 'latest';
  const packages = [];
  for (const name of packageNames) {
    const cwd = path.resolve(directory, packageDirectory(name));
    const manifest = JSON.parse(fs.readFileSync(path.join(cwd, 'package.json'), 'utf8'));
    if (manifest.name !== name || manifest.version !== version) throw new Error(`package version/name mismatch: ${name}`);
    if (name === mainPackage) {
      const expected = Object.fromEntries(packageNames.filter(n => n !== name).map(n => [n, version]));
      if (JSON.stringify(manifest.optionalDependencies) !== JSON.stringify(expected)) throw new Error('optional dependency version mismatch');
    }
    const [packed] = JSON.parse(command(['pack', '--json'], cwd));
    const tarball = path.join(cwd, packed.filename);
    command(['publish', tarball, '--dry-run', '--access', 'public', '--tag', distTag], cwd);
    const skip = checkExisting(await metadata(name), name, version, packed.integrity, distTag);
    packages.push({ name, cwd, tarball, integrity: packed.integrity, skip });
    console.log(`${name}@${version}: ${skip ? 'already published; content verified' : 'ready'}`);
  }
  if (!execute) return;
  if (!process.env.NODE_AUTH_TOKEN && !process.env.ACTIONS_ID_TOKEN_REQUEST_URL) {
    throw new Error('publish requires NPM_TOKEN or configured GitHub Actions trusted publishing');
  }
  for (const pkg of packages) {
    if (pkg.skip) continue;
    command(['publish', pkg.tarball, '--access', 'public', '--provenance', '--tag', distTag], pkg.cwd);
    if (!checkExisting(await metadata(pkg.name), pkg.name, version, pkg.integrity, distTag)) {
      throw new Error(`${pkg.name}@${version}: registry has not confirmed publication; rerun after checking registry`);
    }
    console.log(`published ${pkg.name}@${version}`);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (!['--dry-run', '--publish'].includes(process.argv[4])) {
    console.error('usage: node scripts/npm-publish.mjs <package-dir> <vX.Y.Z> <--dry-run|--publish>');
    process.exitCode = 1;
  } else {
    publishPackages(process.argv[2], process.argv[3], process.argv[4] === '--publish').catch(error => {
      console.error(error.message); process.exitCode = 1;
    });
  }
}
