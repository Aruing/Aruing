// 先检查全部包再发布；仅跳过内容完全相同的已发布版本，主包始终最后处理
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { packageNames, packageDirectory, mainPackage, releaseVersion } from './npm-prepare-release.mjs';

// 命令级超时：查询/打包 2 分钟足够；发布要换 provenance 签名再上传，网络慢时
// 会超过 2 分钟（v0.1.6 首发曾被这条统一超时误杀），单独给到 10 分钟
const DEFAULT_TIMEOUT_MS = 120000;
const PUBLISH_TIMEOUT_MS = 600000;
// 发布后确认 registry 的重试节奏：边缘缓存滞后通常几秒，8 次 × 递增退避约 1 分钟
const CONFIRM_ATTEMPTS = 8;
const CONFIRM_BASE_DELAY_MS = 2000;
// 发布命令失败（含超时被杀）时的重试次数：被杀不代表没发出去，先查 registry
const PUBLISH_ATTEMPTS = 2;

const defaultSleep = ms => new Promise(resolve => setTimeout(resolve, ms));

// 任何 npm 命令失败立即中止，不吞掉认证错误或 registry 错误
function npm(args, cwd, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
  const result = spawnSync('npm', args, { cwd, encoding: 'utf8', timeout: timeoutMs });
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
    // 刚发布时旧文档里可能还没带上新 dist-tag：可重试；持续不一致才是需要人看的冲突
    throw retryable(new Error(`${name}@${version}: content matches but ${tag} differs; maintainer must review dist-tags`));
  }
  return true;
}

// 读取公开 registry；仅 404 表示未发布，其余错误都应在修改 registry 前暴露
async function metadataFor(name) {
  // 发布后立刻查询：带时间戳参数并禁用缓存，否则 CDN 常常还返回发布前的文档，
  // 让重试轮次白等（v0.1.6 首发四个包逐个被旧文档判为「未确认」）
  const url = `https://registry.npmjs.org/${encodeURIComponent(name)}?t=${Date.now()}`;
  const response = await fetch(url, { signal: AbortSignal.timeout(30000), cache: 'no-store' });
  if (response.status === 404) return null;
  if (!response.ok) throw new Error(`registry HTTP ${response.status} for ${name}`);
  return response.json();
}

// 标记「registry 尚未跟上」这类可重试的冲突（内容不同 / 拒绝降级不标记）
function retryable(error) {
  error.retryable = true;
  return error;
}

// 发布后确认：registry 边缘缓存滞后数秒是常态，单次查询会把「已发布但还读不到」
// 误判为失败并中断后续包，故有界重试 + 线性退避
async function confirmPublished(name, version, integrity, distTag, { metadata, sleep }, options = {}) {
  const attempts = options.attempts ?? CONFIRM_ATTEMPTS;
  const baseDelayMs = options.baseDelayMs ?? CONFIRM_BASE_DELAY_MS;
  let reason = 'version not visible in the registry yet';
  for (let attempt = 1; attempt <= attempts; attempt++) {
    if (attempt > 1) await sleep(baseDelayMs * (attempt - 1));
    try {
      if (checkExisting(await metadata(name), name, version, integrity, distTag)) return;
    } catch (error) {
      // 硬错误（发布内容不同 / 拒绝降级）重试不会自愈，立即抛出
      if (!error.retryable) throw error;
      reason = error.message;
    }
  }
  throw new Error(`${name}@${version}: ${reason} (checked ${attempts} times)`);
}

// 单包发布：spawnSync 超时被杀不代表没发出去，失败后先查 registry 再决定是否重试，
// 避免把已落地的发布重复推一次（npm 对既有版本一律拒绝）
async function publishOne(pkg, version, distTag, ctx) {
  for (let attempt = 1; attempt <= (ctx.publishAttempts ?? PUBLISH_ATTEMPTS); attempt++) {
    try {
      ctx.command(['publish', pkg.tarball, '--access', 'public', '--provenance', '--tag', distTag], pkg.cwd, { timeoutMs: PUBLISH_TIMEOUT_MS });
      return;
    } catch (error) {
      if (await alreadyLanded(pkg, version, ctx)) return;
      if (attempt === (ctx.publishAttempts ?? PUBLISH_ATTEMPTS)) throw error;
      await ctx.sleep(CONFIRM_BASE_DELAY_MS);
    }
  }
}

// 单次查询判断本次发布是否已落地：只看版本与完整性是否已在 registry，不要求
// dist-tag 跟上——版本与 tag 的传播可能分先后，把「tag 未跟上」当成未落地会去
// 重发一个已存在的版本，npm 对既有版本一律拒绝，反而让发布失败。tag 的追平由
// confirmPublished 继续等待
async function alreadyLanded(pkg, version, { metadata }) {
  const existing = (await metadata(pkg.name))?.versions?.[version];
  if (!existing) return false;
  if (existing.dist?.integrity !== pkg.integrity) throw new Error(`${pkg.name}@${version}: published content differs`);
  return true;
}

// 所有包先完成打包和冲突检查，待发布包再演练；显式发布仅供正式工作流调用
export async function publishPackages(directory, tag, execute = false, { command = npm, metadata = metadataFor, sleep = defaultSleep, publishAttempts = PUBLISH_ATTEMPTS } = {}) {
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
    const skip = checkExisting(await metadata(name), name, version, packed.integrity, distTag);
    // npm 连演练也拒绝既有版本，内容与标签均验证相同后直接跳过
    if (!skip) command(['publish', tarball, '--dry-run', '--access', 'public', '--tag', distTag], cwd);
    packages.push({ name, cwd, tarball, integrity: packed.integrity, skip });
    console.log(`${name}@${version}: ${skip ? 'already published; content verified' : 'ready'}`);
  }
  if (!execute) return;
  if (!process.env.NODE_AUTH_TOKEN && !process.env.ACTIONS_ID_TOKEN_REQUEST_URL) {
    throw new Error('publish requires NPM_TOKEN or configured GitHub Actions trusted publishing');
  }
  const ctx = { command, metadata, sleep, publishAttempts };
  for (const pkg of packages) {
    if (pkg.skip) continue;
    await publishOne(pkg, version, distTag, ctx);
    await confirmPublished(pkg.name, version, pkg.integrity, distTag, ctx);
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
