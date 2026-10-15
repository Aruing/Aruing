# npm 包维护

主包 `aruing` 通过三个精确版本 optionalDependencies 选择平台包。
平台包安装时从同版本 GitHub Release 下载二进制，先校验 SHA-256 再安装。
仓库中的平台 `install.js` 与许可证由 `make npm-sync` 生成，修改安装逻辑请编辑 `npm/installer.cjs`。

## 本地检查

```sh
make npm-test npm-sync
node scripts/npm-prepare-release.mjs v0.1.5 /tmp/aruing-npm-candidate
node scripts/npm-publish.mjs /tmp/aruing-npm-candidate v0.1.5 --dry-run
```

输出目录必须尚不存在；准备脚本仅复制包白名单，不修改源码版本。
dry-run 会打包、检查四个包的内容和 registry 中的版本冲突，必须联网读取公开 registry，但不发布。
预发布 tag（如 `v0.1.5-rc.1`）对应 npm `next`，稳定版对应 `latest`。

## 正式发布与首次配置

1. 所有功能完成并通过版本级检查后，将发布快照经 PR 合入 production。
2. 在 production 上打版本 tag；现有 Release workflow 创建 Draft Release。
3. 维护者确认附件与 release notes，然后在 GitHub 页面发布 Release。
4. `npm-publish.yml` 自动 checkout 同一 tag，检查 production 归属和附件公开可下载。
5. workflow 准备四个包，先发布平台包，全部确认后才发布主包。

首次发包前，包管理者须确认四个包名均可使用且由预期账号管理：

- `aruing`
- `aruing-darwin-arm64`
- `aruing-darwin-x64`
- `aruing-linux-x64`

bootstrap 可使用具备相应发布权限的 npm granular token，由管理者存入 GitHub 仓库 secret `NPM_TOKEN`。
也可使用已配置的 npm trusted publishing：逐包设置 GitHub owner `Aruing`、仓库 `Aruing`、workflow 文件名 `npm-publish.yml`，允许直接 `npm publish`。
首次建包能否直接配置以 npm 当前界面为准；若尚不能配置，先用 bootstrap token 完成首次发布再切换。
workflow 固定使用支持 OIDC 的 npm CLI，声明 `id-token: write` 并附 provenance。
密钥只由管理者在 npm / GitHub 设置页操作，不写进源码、提交或日志。

必须由维护者人工发布 Draft；使用 `GITHUB_TOKEN` 自动发布 Release 不会触发后续 release workflow。
可在失败的 npm workflow 页面重跑：脚本只跳过完整性与目标 dist-tag 均相同的既有版本。
若 registry 内容或 dist-tag 不同，停止并由维护者检查，不覆盖包、不自动修改已有标签。

## 发布后验收

在三个支持平台的干净 npm prefix 中安装精确版本，检查 `version`、`connect`、`run`、`chat`。
确认 `aruing update` 拒绝自替换并引导 `npm update -g aruing`。
本地 fixture 与已有 Release 的安装冒烟均不能替代新版本发布后的 registry 验收。
