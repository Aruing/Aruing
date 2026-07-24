// 配置包集中收敛进程级运行参数
//
// 当前只从环境变量读取 LLM 端点与 kubectl 路径，不解析配置文件、不热更新
// 命令行入口通过 Load 拿到 Config 后组装编排器，agent 与 tools 不直接读 env
//
// 本地调试可用仓库根目录 .env（由 Make load-dotenv 注入）；本包不解析 .env 文件
package config
