// 同步平台安装器和许可证；平台包自包含，生成内容不得手工修改
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const root = new URL('../', import.meta.url);
const installer = readFileSync(new URL('npm/installer.cjs', root));
const license = readFileSync(new URL('LICENSE', root));
for (const name of ['aruing', 'aruing-darwin-arm64', 'aruing-darwin-x64', 'aruing-linux-x64']) {
  const directory = new URL(`npm/${name}/`, root);
  if (name !== 'aruing') writeFileSync(new URL('install.js', directory), installer);
  writeFileSync(new URL('LICENSE', directory), license);
  console.log(`synced ${fileURLToPath(directory)}`);
}
