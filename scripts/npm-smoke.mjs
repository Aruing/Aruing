// 临时 registry 安装候选 tarball，再下载已有 Release；不写真实 registry 或用户全局目录
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { once } from 'node:events';
import { spawn } from 'node:child_process';
import { prepare, packageNames, packageDirectory, mainPackage } from './npm-prepare-release.mjs';

// 异步子进程保证临时 registry 在 npm 安装期间仍能处理请求
function command(binary, args, cwd) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, args, { cwd, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    child.stdout.on('data', data => { stdout += data; });
    child.stderr.on('data', data => { stderr += data; });
    child.once('error', reject);
    child.once('close', (code, signal) => resolve({ code, signal, stdout, stderr }));
  });
}

// 非成功状态携带阶段信息，避免 optional dependency 失败被主安装成功掩盖
async function checked(binary, args, cwd) {
  const result = await command(binary, args, cwd);
  if (result.code !== 0) throw new Error(`${binary} ${args[0]} failed (${result.code ?? result.signal}): ${result.stderr}`);
  return result;
}

const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'aruing-npm-smoke-'));
let registry;
try {
  const candidate = path.join(temporary, 'packages');
  const { version } = prepare(process.argv[2], candidate);
  const packed = new Map();
  for (const name of packageNames) {
    const directory = path.join(candidate, packageDirectory(name));
    const [info] = JSON.parse((await checked('npm', ['pack', '--json'], directory)).stdout);
    packed.set(name, { info, directory, manifest: JSON.parse(fs.readFileSync(path.join(directory, 'package.json'))) });
  }
  registry = http.createServer((req, res) => {
    const name = decodeURIComponent(req.url.split('?')[0].slice(1));
    const entry = packed.get(name);
    if (entry) {
      const dist = { tarball: `http://127.0.0.1:${registry.address().port}/${entry.info.filename}`, integrity: entry.info.integrity };
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ name, 'dist-tags': { latest: version }, versions: { [version]: { ...entry.manifest, dist } } }));
      return;
    }
    const tarball = [...packed.values()].find(p => p.info.filename === name);
    if (!tarball) { res.writeHead(404); res.end('{}'); return; }
    fs.createReadStream(path.join(tarball.directory, name)).pipe(res);
  });
  registry.listen(0, '127.0.0.1');
  await once(registry, 'listening');
  const prefix = path.join(temporary, 'prefix');
  const userConfig = path.join(temporary, '.npmrc');
  fs.writeFileSync(userConfig, '');
  const main = packed.get(mainPackage);
  console.log(`installing candidate npm packages for ${version} on ${process.platform}/${process.arch}`);
  await checked('npm', ['install', '-g', path.join(main.directory, main.info.filename),
    '--prefix', prefix, '--registry', `http://127.0.0.1:${registry.address().port}`,
    '--cache', path.join(temporary, 'cache'), '--userconfig', userConfig,
    '--include=optional', '--ignore-scripts=false', '--foreground-scripts', '--no-audit', '--no-fund'], temporary);
  const binary = path.join(prefix, 'bin/aruing');
  const output = (await checked(binary, ['version'], temporary)).stdout.trim();
  if (!output.startsWith(`aruing version ${version}\n`) && output !== `aruing version ${version}`) {
    throw new Error(`binary version mismatch: ${output}`);
  }
  const update = await command(binary, ['update'], temporary);
  if (update.code === 0 || !update.stderr.includes('npm update -g @aruing/aruing')) {
    throw new Error('npm-managed binary did not refuse self-update');
  }
  for (const subcommand of ['connect', 'run', 'chat']) {
    const result = await command(binary, [subcommand, '--help'], temporary);
    if (!/usage:/i.test(result.stdout + result.stderr)) throw new Error(`${subcommand} help is unavailable`);
  }
  console.log(JSON.stringify({ platform: process.platform, arch: process.arch, version,
    installation: 'passed', checksum: 'verified by postinstall', npmUpdateGuard: 'passed',
    commandHelp: 'connect/run/chat passed', diagnosis: 'not exercised; requires cluster and LLM' }, null, 2));
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
} finally {
  if (registry) { registry.closeAllConnections(); registry.close(); }
  fs.rmSync(temporary, { recursive: true, force: true });
}
