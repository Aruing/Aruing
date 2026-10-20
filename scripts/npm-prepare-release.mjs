// 将包白名单复制到独立目录，并从 tag 同步版本；不改源码包或重新构建二进制
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const mainPackage = '@aruing/aruing';
export const packageNames = ['@aruing/aruing-darwin-arm64', '@aruing/aruing-darwin-x64', '@aruing/aruing-linux-x64', mainPackage];
const sourceRoot = fileURLToPath(new URL('../npm/', import.meta.url));

// npm 全名含组织 scope，本地目录保留稳定名称；白名单外输入不得变为路径
export function packageDirectory(name) {
  if (!packageNames.includes(name)) throw new Error(`unknown npm package: ${name}`);
  return name.slice('@aruing/'.length);
}

// 接受项目采用的三段版本及预发布标签，拒绝路径片段和有前导零的数字标识
export function releaseVersion(tag) {
  const match = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/.exec(tag);
  if (!match || match[0] !== tag || (match[4] || '').split('.').some(x => /^\d+$/.test(x) && x.length > 1 && x[0] === '0')) {
    throw new Error(`invalid release tag: ${tag}; expected vX.Y.Z[-prerelease]`);
  }
  return tag.slice(1);
}

// 输出必须是全新目录；错误时只清理本次创建的目录，不覆盖调用者已有数据
export function prepare(tag, output) {
  const version = releaseVersion(tag);
  if (!output) throw new Error('output directory is required');
  output = path.resolve(output);
  fs.mkdirSync(output);
  try {
    for (const name of packageNames) {
      const source = path.join(sourceRoot, packageDirectory(name));
      const target = path.join(output, packageDirectory(name));
      const manifest = JSON.parse(fs.readFileSync(path.join(source, 'package.json'), 'utf8'));
      if (manifest.name !== name) throw new Error(`unexpected package name in ${source}`);
      manifest.version = version;
      if (name === mainPackage) {
        manifest.optionalDependencies = Object.fromEntries(packageNames.filter(n => n !== name).map(n => [n, version]));
      }
      fs.mkdirSync(target);
      for (const entry of manifest.files) fs.cpSync(path.join(source, entry), path.join(target, entry), { recursive: true });
      fs.writeFileSync(path.join(target, 'package.json'), `${JSON.stringify(manifest, null, 2)}\n`);
    }
  } catch (error) {
    fs.rmSync(output, { recursive: true, force: true });
    throw error;
  }
  return { version, tag: version.includes('-') ? 'next' : 'latest', output };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(JSON.stringify(prepare(process.argv[2], process.argv[3]))); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
