# Remote-Kind Design Document

## 一、项目定义

**remote-kind** 是一个 CLI 工具，用阿里云真实云资源（ECS + VPC + NLB）一键创建和管理 Kubernetes 集群，体验对标 kind（Kubernetes in Docker），但运行在真实云环境而非 Docker 容器中。

```
kind:        kind create cluster --name test    →  Docker 容器 = K8s 节点
remote-kind: remote-kind create cluster --name test →  ECS 虚拟机 = K8s 节点
```

## 二、为什么做

### 2.1 kind 的局限

kind 用 Docker 容器模拟 K8s 节点，虽然极快（~10 秒），但有本质缺陷：
- **网络模型不同** — Docker bridge/NAT vs 真实 VPC 网络
- **存储限制** — 无持久化存储，重启容器丢失数据
- **内核差异** — 共享宿主机内核，无法测试内核相关功能
- **不能测云特性** — CLB/NLB Ingress、云盘 CSI、VPC CNI 都无法在 kind 中测试
- **资源受限** — 受限于本地机器资源

### 2.2 现有工具的体验鸿沟

| 工具 | 创建命令 | 前置条件 | 耗时 |
|------|---------|---------|------|
| **kind** | `kind create cluster` | Docker | 10-30s |
| kubeadm | 5+ 步手动操作 | VM + OS + containerd + ... | 手动 10min+ |
| kubespray | Ansible playbook | inventory + vars | 多分钟 |
| kops | 复杂配置 | DNS + S3 + state store | 分钟级 |
| ACK CLI | JSON 15+ 字段 | VPC + vSwitch + 安全组 + ... | 4-15min |
| **remote-kind** | `remote-kind create cluster` | 阿里云账号 + AK/SK | **目标 2-3min** |

### 2.3 为什么只用阿里云

- 专一做深比广撒网更有竞争力
- 阿里云在国内市场占有率高，社区生态成熟
- kingc（GCP）和 Kina（Azure）已有验证，阿里云侧仍是空白
- 阿里云 API 能力完全足以支撑此项目

## 三、Kind 架构关键分析

### 3.1 我们借鉴的

| kind 特性 | 实现方式 | remote-kind 对标 |
|-----------|---------|-----------------|
| **二层镜像** | base(img) + node(img with kubelet/kubeadm/预拉取镜像) | Packer 构建的自定义 ECS 镜像 |
| **kubeadm 引导** | docker exec → kubeadm init | SSH → kubeadm init |
| **声明式配置** | `kind-config.yaml` | `remote-kind.yaml` |
| **无状态启动器** | 不维护状态文件 | 不维护 Terraform state，内存跟踪资源 ID |
| **单命令创建** | `kind create cluster` | `remote-kind create cluster` |
| **一次性销毁** | `kind delete cluster` | `remote-kind delete cluster`（清理所有云资源） |

### 3.2 我们不借鉴的

- **Docker 容器作为节点** — 改用 ECS 虚拟机
- **CI 为首要场景** — remote-kind 定位 dev/test 环境，非 CI
- **生产环境明确不在范围内** — 同样适用

## 四、阿里云可行性验证

### 4.1 ECS 启动速度

| 场景 | 耗时 |
|------|------|
| Alibaba Cloud Linux 3 标准版 | ~90 秒 |
| Alibaba Cloud Linux 3 **Qboot 快速启动版** | **~30 秒** |
| cloud-init 完整执行 | +15-20 秒（可裁剪为 AliyunInit） |
| AWS EKS 优化 AMI（对比） | ~31-60 秒 |

**结论：使用 Qboot + 预构建镜像，ECS 创建到可用可控制在 ~30 秒，总集群创建时间目标 2-3 分钟。**

### 4.2 NLB 负载均衡

阿里云 NLB 完全满足 API Server 暴露需求：
- 四层 TCP 透传，支持 Proxy Protocol v1/v2
- 静态 IP，实例替换后 kubeconfig 不失效
- 按量付费：0.147 元/小时实例费 + 0.037 元/LCU/小时

### 4.3 市场定位验证

ACK 托管集群的 CLI 创建需要 15+ 个 JSON 字段（VPC ID、vSwitch ID、实例规格等），创建耗时 4-15 分钟。ACK 专有集群于 2024 年 8 月停建，ASK Serverless 于 2025 年 2 月对新用户关闭——自建标准 K8s 的远程托管需求反而在增长。

## 五、技术决策

### 5.1 已确定

| 决策 | 选择 | 原因 |
|------|------|------|
| 语言 | **Go** | 单二进制分发，kind 也是 Go |
| 云平台 | **阿里云**（仅） | 专一做深，不做多云 |
| 云 API 调用 | **阿里云 Go SDK** | 结构化错误处理，不 shell out |
| 集群引导 | **kubeadm** | kind 同款，社区标准 |
| 负载均衡 | **NLB** | 阿里云主推，TCP 透传，成本低 |
| 基础设施管理 | **不引入 Terraform** | 无状态启动器，不需要 state management |
| 目标用户 | **开发/测试环境** | 非生产，不做滚动升级/自动修复 |
| 生产就绪 | **不在范围内** | 同 kind 的设计哲学 |

### 5.2 待调研决策

- SSH 认证方式：密钥对 vs 密码 vs SSM 通道
- 安全组策略：全开（简单）vs 最小权限（安全）
- 镜像构建：Packer vs 阿里云镜像构建服务
- CLI 框架：cobra（与 kind 一致） vs 其他

## 六、已知风险和缓解

| 风险 | 缓解 |
|------|------|
| 用户忘记销毁产生持续费用 | 创建时打印销毁命令；可选 TTL 自动销毁 |
| ECS 启动失败/超时 | 超时重试机制；清晰的错误信息 |
| 云账号权限不足 | 文档明确列出所需 RAM 权限 |
| NLB 实例费持续产生 | delete 命令必须先于 ECS 清理 NLB |
| 镜像维护成本 | 跟随 K8s 版本节奏，CI 自动构建 |

## 七、竞品参考

| 项目 | 平台 | 与我们关系 |
|------|------|-----------|
| [kingc](https://pkg.go.dev/github.com/aojea/kingc) | GCP | 最接近的参考实现，kind 维护者所写，架构可直接借鉴 |
| [Kina](https://pypi.org/project/kina/) | Azure | Python CLI，封装 AKS（非裸 VM），体验参考 |
| [kind](https://kind.sigs.k8s.io/) | Docker | 体验标杆，CLI 设计、配置格式直接对标 |

## 八、参考来源

- kind 设计文档: https://kind.sigs.k8s.io/docs/design/principles/
- kind node image: https://kind.sigs.k8s.io/docs/design/node-image
- kingc 源码: https://pkg.go.dev/github.com/aojea/kingc
- Kina: https://pypi.org/project/kina/1.3.1/
- 阿里云 NLB 计费: https://help.aliyun.com/zh/slb/network-load-balancer/product-overview/nlb-billable-items
- ACK 创建集群 API: https://www.alibabacloud.com/help/en/ack/ack-edge/developer-reference/create-a-cluster
- EKS 启动速度优化: https://github.com/awslabs/amazon-eks-ami/issues/1099
- 自建 K8s on ECS (博客园): https://www.cnblogs.com/cmt/p/19036702
