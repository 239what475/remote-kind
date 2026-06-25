# Remote-Kind 功能分析与路线图

> 更新：2026-06-25 | 6 次 commit，全功能闭环测试通过

## 一、已实现功能

### CLI 命令（10 个）
```
remote-kind init                 生成 remote-kind.yaml 模板
remote-kind create cluster       创建集群（--wait, --retain）
remote-kind delete cluster       删除集群（--name, 自动清理 kubeconfig）
remote-kind show cluster         展示集群详情
remote-kind scale --workers N    扩缩容（含 cordon+drain）
remote-kind get clusters|nodes   列表查询
remote-kind get kubeconfig       输出 kubeconfig
remote-kind build-image          构建自定义 ECS 镜像（--force, --test）
remote-kind ssh <node>           SSH 到节点
remote-kind completion           生成 shell 补全脚本
```

### 集群生命周期
- VPC/vSwitch/SG 自动创建和清理
- CP + Workers 并行创建（goroutine）
- kubeadm init/join（Cloud Assistant RunCommand）
- 创建失败自动回滚（defer + 增量追踪）
- 标签驱动资源发现和删除
- Worker 扩缩容（kubectl drain → delete ECS）
- kubeconfig 合并（kind- 前缀）+ 删除时清理
- SSH 密钥注入（cloud-init）

### 镜像构建
- 动态 containerd 镜像加速（配置 `build.mirrors`）
- K8s 核心镜像 + Flannel 预拉取
- 基于 Cloud Assistant + CreateImage API
- 版本常量集中管理（`versions.go`）

### 代码质量
- 零 `_` 丢弃 error（全量检查或记录日志）
- go:embed 模板化脚本（非字符串拼接）
- `.golangci.yml`（errcheck/gosec/staticcheck/govet）
- gopls/golangci-lint 零问题
- 单二进制分发（无外部依赖）

## 二、延后项

| 功能 | 原因 |
|------|------|
| NLB 接入 | 需要真实环境测试 API 行为 |
| 多 CP 高可用 | 依赖 NLB |
| Spot/抢占式实例 | 需要真实测试计费模式 |

## 三、不做项

- export logs — `kubectl logs`/`ssh + journalctl` 够用
- `-o json/yaml` — 保持简单，不增加复杂度
- push-image — 依赖 ACR 实例管理，非核心
- kubeadm ConfigPatches — 重新创建集群即可
- 集群升级 — dev/test 不需要
- dry-run — 实际创建很快，不需要预览
