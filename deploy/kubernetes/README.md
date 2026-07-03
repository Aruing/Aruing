# 集群部署

本地命令行闭环跑通后，这里再放集群部署清单

预计文件：

- `deployment.yaml`：在集群中运行 aruing
- `serviceaccount.yaml`：为程序提供集群身份
- `rbac.yaml`：只授予读取权限
- `configmap.yaml`：保存非敏感运行配置

第一版部署不要加入写权限、容器内执行、修改、删除或自动修复能力
