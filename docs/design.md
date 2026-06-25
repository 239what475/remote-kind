# Remote-Kind Design Document

## 一、项目定义

**remote-kind** 是一个 CLI 工具，用阿里云 ECS 真实云资源一键创建和管理 Kubernetes 集群，体验对标 kind（Kubernetes in Docker）。

```
kind:        kind create cluster --name test    →  Docker 容器 = K8s 节点
remote-kind: remote-kind create cluster --name test →  ECS 虚拟机 = K8s 节点
```

## 二、为什么做

### 2.1 kind 的局限

kind 用 Docker 容器模拟 K8s 节点，虽然极快，但有本质缺陷：
- **网络模型不同** — Docker bridge/NAT vs 真实 VPC 网络
- **存储限制** — 无持久化存储，重启容器丢失数据
- **不能测云特性** — CLB/NLB Ingress、云盘 CSI、VPC CNI 都无法在 kind 中测试

### 2.2 为什么不直接用 ACK

ACK CLI 创建需要 15+ 个 JSON 字段，耗时 4-15 分钟。专有集群 2024 年停建，Serverless 2025 年对新用户关闭。自建标准 K8s 的需求反而在增长。

### 2.3 为什么只用阿里云

- 专一做深比广撒网更有竞争力
- kingc（GCP）和 Kina（Azure）已有验证，阿里云侧仍是空白

## 三、核心设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 语言 | **Go** | 单二进制分发 |
| 集群引导 | **kubeadm** | kind 同款，社区标准 |
| 命令执行 | **Cloud Assistant (RunCommand)** | 不需要 SSH，不需要公网 IP 打通网络 |
| 节点访问 | **SSH 密钥注入**（cloud-init） | 仅用于调试 |
| 镜像构建 | **Cloud Assistant + CreateImage** | 不用 Packer，复用已有 aliyun SDK 层 |
| API 入口 | **CP 公网 IP + cert SAN** | 简单直接，不需要 NLB |
| 基础设施管理 | **不引入 Terraform** | 无状态启动器，标签驱动资源发现 |
| 定位 | **开发/测试环境** | 非生产 |

## 四、架构

### 4.1 目录结构

```
cmd/remote-kind/main.go     # 唯一二进制入口
pkg/
  aliyun/                   # 阿里云 SDK 封装
    client.go               # 凭证 + ECS/VPC client 工厂
    ecs.go                  # 实例、安全组、镜像
    vpc.go                  # VPC、vSwitch
    command.go              # Cloud Assistant（RunCommand）
  bootstrap/                # 集群引导
    cloudinit.go            # cloud-init 模板
    scripts.go              # kubeadm/install 脚本模板（go:embed）
    templates/              # shell 脚本模板文件
  cluster/                  # 编排层
    create.go               # 创建集群（并行 CP + Workers）
    delete.go               # 标签驱动删除
    scale.go                # worker 扩缩容（含 drain）
    build.go                # 自定义镜像构建
    show.go / list.go       # 查询
    config.go               # 配置文件解析
    types.go / versions.go  # 数据模型 + 版本常量
    templates/              # kubeadm-config + kube-flannel 模板
```

### 4.2 网络模型

```
┌─────────────────────────────────────────────┐
│ VPC: remote-kind-demo (10.0.0.0/16)         │
│  vSwitch (10.0.1.0/24)                       │
│                                              │
│  control-plane (ECS, 有公网IP)               │
│  workers × N (ECS, 有公网IP)                 │
│                                              │
│  安全组: 6443 + 22 + 30000-32767 (0.0.0.0)  │
│         VPC 内部全通                          │
└─────────────────────────────────────────────┘

用户 kubectl ──→ CP 公网 IP:6443 (cert SAN)
用户 remote-kind ──→ ECS API (RunCommand) ──→ 实例
用户 ssh ──→ 公网 IP:22 (密钥认证)
```

### 4.3 创建流程

```
1. 读配置 → ApplyDefaults
2. 创建 VPC → vSwitch → SG（开 22/6443/30000-32767 + VPC 全通）
3. 查找自定义镜像（remote-kind-v1.36）
4. 并行：
   ├─ CP: RunInstances → WaitRunning → CloudAssistant → kubeadm init → token
   └─ Workers: RunInstances → WaitRunning → CloudAssistant
5. Workers: kubeadm join
6. 拉取 admin.conf → 合并到 ~/.kube/config
7. 安装 Flannel CNI（kubectl apply）
```

### 4.4 镜像构建流程

```
1. 创建临时 VPC + ECS（有公网 IP）
2. Cloud Assistant 执行安装脚本：
   - yum install containerd + kubelet/kubeadm/kubectl
   - 配置 containerd 镜像加速（动态生成 hosts.toml）
   - kubeadm config images pull + crictl pull flannel
   - sync → systemctl stop containerd
3. CreateImage API → 等待 Available
4. 删除临时 ECS + VPC
```

## 五、竞品参考

| 项目 | 平台 | 参考点 |
|------|------|--------|
| [kingc](https://pkg.go.dev/github.com/aojea/kingc) | GCP | 无状态 + 标签驱动，kind 维护者所写 |
| [kind](https://kind.sigs.k8s.io/) | Docker | CLI 体验 + 配置格式 + kubeconfig |
