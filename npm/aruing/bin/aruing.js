#!/usr/bin/env node
// 只选择同版本平台包并透传终端交互，不承担诊断逻辑或自更新
'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const pkg = require('../package.json');

// 使用 Node 架构名称选择平台包，不把 x64 与 Go 的 amd64 混用
function platformPackage(platform = process.platform, arch = process.arch) {
  const names = {
    'darwin-arm64': 'aruing-darwin-arm64',
    'darwin-x64': 'aruing-darwin-x64',
    'linux-x64': 'aruing-linux-x64',
  };
  const name = names[`${platform}-${arch}`];
  if (!name) throw new Error(`unsupported npm platform ${platform}/${arch}; use https://github.com/Aruing/Aruing/releases`);
  return name;
}

// 缺失依赖、跳过安装脚本或版本错配均明确失败，不在运行命令时隐式下载
function binaryPath() {
  const name = platformPackage();
  const manifest = require.resolve(`${name}/package.json`);
  if (JSON.parse(fs.readFileSync(manifest, 'utf8')).version !== pkg.version) {
    throw new Error(`${name} version does not match aruing ${pkg.version}`);
  }
  const binary = path.join(path.dirname(manifest), 'bin', 'aruing');
  fs.accessSync(binary, fs.constants.X_OK);
  return binary;
}

// 子进程保留终端流；取消信号和退出状态传回调用方
function run() {
  let binary;
  try {
    binary = binaryPath();
  } catch (error) {
    console.error(`aruing: ${error.message}\nReinstall with optional dependencies and scripts enabled:\n  npm install -g aruing --include=optional --ignore-scripts=false --foreground-scripts`);
    process.exitCode = 1;
    return;
  }
  const child = spawn(binary, process.argv.slice(2), { stdio: 'inherit' });
  const handlers = new Map(['SIGINT', 'SIGTERM', 'SIGHUP'].map(signal => {
    const handler = () => child.kill(signal);
    process.on(signal, handler);
    return [signal, handler];
  }));
  const cleanup = () => {
    for (const [signal, handler] of handlers) process.removeListener(signal, handler);
  };
  child.once('error', error => {
    cleanup();
    console.error(`aruing: cannot start binary: ${error.message}`);
    process.exitCode = 1;
  });
  child.once('exit', (code, signal) => {
    cleanup();
    if (signal) process.kill(process.pid, signal);
    else process.exitCode = code ?? 1;
  });
}

module.exports = { platformPackage, binaryPath };
if (require.main === module) run();
