# Remote-Kind 功能分析与路线图

> 生成日期：2026-06-25 | 基于代码审计 + kind/kops/k3s/Talos/Cluster API 调研

## 一、现有功能清单

### CLI 命令（7 个）
| 命令 | 状态 |
|------|------|
| `init` | ✅ 生成 remote-kind.yaml 模板 |
| `create cluster` | ✅ 创建集群（并行 CP + workers） |
| `delete cluster` | ✅ 标签驱动批量删除 |
| `get clusters` | ✅ 按 Tag 聚合展示 |
| `get nodes` | ✅ 按 Tag 过滤展示 |
| `get kubeconfig` | ✅ 从 CP 拉取 admin.conf |
| `version` | ✅ 打印版本号 |

### 集群生命周期
| 功能 | 状态 |
|------|------|
| VPC/vSwitch/SG 自动创建 | ✅ |
| CP + Workers 并行创建 | ✅ goroutine |
| kubeadm init + join | ✅ Cloud Assistant |
| kubeconfig 合并（kind- 前缀） | ✅ client-go |
| CNI 安装（Flannel） | ✅ kubectl apply |
| SSH 密钥注入 | ✅ cloud-init |
| 标签驱动资源发现 | ✅ |
| 创建失败自动清理 | ✅ --retain 控制 |
| 自定义镜像（build-image） | ✅ registry mirrors + 镜像预拉 |

### 配置模型
| 字段 | 状态 |
|------|------|
| Region / Zone | ✅ |
| ControlPlane（实例规格/磁盘） | ✅ |
| Workers（多组，副本数） | ✅ |
| Networking（4 个 CIDR） | ✅ |
| CNI（flannel/none） | ✅ |
| Mirrors（registry mirrors） | ✅ |
| SSHKeyFile | ✅ |
| ImageID | ❌ 字段存在但 findImage() 忽略它 |

## 二、代码质量问题

| 严重度 | 问题 | 位置 |
|--------|------|------|
| **P0** | `klog.Fatalf` 在库代码中（应 return error） | `create.go:219` |
| **P0** | `ImageID` 配置字段被忽略，写死 `"remote-kind-v1.36"` | `create.go` findImage() |
| **P0** | K8s 版本硬编码 `v1.36.2`，`DefaultK8sVersion` 常量未用 | `create.go` / `types.go` |
| **P0** | `delete cluster` 无 `--force` 时无确认提示 | `main.go` |
| **P1** | NLB 全部实现但从未接入 create/delete | `slb.go` → 死代码 |
| **P1** | `pkg/kubeconfig/merge.go` 从未被调用 | 死代码 |
| **P1** | `progress.go` 从未被调用 | 死代码 |
| **P1** | `kubeadm.go` InitScript 从未被调用 | 死代码 |
| **P1** | 创建失败中间资源泄漏（VPC 建了但 vSwitch 失败时） | `create.go` |
| **P1** | `build-image` defer DeleteVpc 被 kill 时不执行 | `build-image/main.go` |
| **P2** | klog 与 fmt.Print 混用，klog 未初始化 | 全局 |
| **P2** | 零单元测试 | 全局 |
| **P2** | server IP 替换用 strings.ReplaceAll 可能损坏 YAML | `main.go` mergeKubeconfig |

## 三、功能对标：remote-kind vs kind

| kind 功能 | remote-kind | 优先级 |
|-----------|-------------|--------|
| `create cluster` | ✅ | — |
| `delete cluster` | ✅ | — |
| `get clusters/nodes/kubeconfig` | ✅ | — |
| 声明式 YAML 配置 | ✅ | — |
| 预构建镜像 (base + node) | ✅ build-image | — |
| kubeconfig 合并 | ✅ | — |
| **`export logs`** | ❌ | **P1** |
| **`load docker-image`** | ❌ (需 push-image) | **P2** |
| **kubeadm ConfigPatches** | ❌ | **P2** |
| **K8s 版本参数化** | ❌ 硬编码 | **P0** |
| **`--wait` 等待就绪** | ❌ | **P1** |
| **JSON/YAML 输出** | ❌ 纯文本 | **P1** |
| **删除时清理 kubeconfig** | ❌ | **P1** |
| **Shell 补全** | ❌ | **P2** |
| **dry-run** | ❌ | **P2** |

## 四、功能对标：运维能力

| 能力 | 来源参考 | 优先级 |
|------|---------|--------|
| 节点添加/删除 (scale) | kops, Cluster API | **P1** |
| 集群升级（滚动更新） | kops, kubeadm | **P2** |
| 多 CP 高可用 | kind, k3s | **P2** |
| NLB 负载均衡 | kingc, ACK | **P2** |
| etcd 备份/恢复 | kops | P3 |
| 证书轮换 | kubeadm | P3 |
| Spot 实例支持 | kops | **P2** |
| 现有 VPC 复用 | ACK | P3 |
| 节点 SSH 隧道（等价 node-shell） | k3s | P3 |
| 审计日志收集 | Talos | P3 |

## 五、功能对标：云原生集成

| 能力 | 来源参考 | 优先级 |
|------|---------|--------|
| 云盘 CSI 驱动 | ACK | P3 |
| ALB Ingress Controller | ACK | P3 |
| Terway VPC CNI（替代 Flannel） | ACK | P3 |
| cluster-autoscaler / Karpenter | ACK, Cluster API | P3 |
| 阿里云监控集成 | ACK | P3 |
| 多架构镜像 (ARM64) | ACK | P3 |

## 六、优先级矩阵

### P0 — 必须立即修复（阻塞基本可用性）

| 编号 | 功能/修复 | 复杂度 | 估时 |
|------|----------|--------|------|
| P0-1 | 修复 `klog.Fatalf` → return error | 低 | 0.5h |
| P0-2 | `ImageID` 配置生效 | 低 | 1h |
| P0-3 | K8s 版本从配置读取（不硬编码） | 低 | 2h |
| P0-4 | `delete cluster` 确认提示 | 低 | 1h |
| P0-5 | 创建失败资源回滚（增量记录 + defer 清理） | 中 | 4h |
| P0-6 | `--name` 标志支持（不依赖 config 文件也可删） | 中 | 3h |

### P1 — 高优先级（核心体验）

| 编号 | 功能 | 复杂度 | 估时 |
|------|------|--------|------|
| P1-1 | `export logs` — 收集所有节点 journalctl + pod 日志 | 中 | 6h |
| P1-2 | `scale` — 节点添加/删除 | 中 | 8h |
| P1-3 | `--wait` — 等待 control-plane Ready 后返回 | 低 | 2h |
| P1-4 | `-o json/yaml` — get 命令结构化输出 | 低 | 3h |
| P1-5 | 删除时清理 kubeconfig context | 低 | 2h |
| P1-6 | 连接 build-image 和 create 的版本号 | 中 | 5h |
| P1-7 | 清理死代码（merge.go, progress.go, InitScript） | 低 | 2h |
| P1-8 | `DependencyViolation` 自动重试（delete 路径） | 低 | 2h |

### P2 — 中优先级（生态完善）

| 编号 | 功能 | 复杂度 | 估时 |
|------|------|--------|------|
| P2-1 | NLB 接入 create/delete | 高 | 16h |
| P2-2 | 多 CP 高可用（>1 CP + stacked etcd） | 高 | 24h |
| P2-3 | `push-image` — 类比 `kind load docker-image` | 高 | 12h |
| P2-4 | kubeadm ConfigPatches 支持 | 中 | 6h |
| P2-5 | 集群升级（`remote-kind upgrade`） | 高 | 20h |
| P2-6 | Shell 自动补全 | 低 | 1h |
| P2-7 | `--dry-run` — 预览将要创建的资源 | 中 | 4h |
| P2-8 | 单元测试（至少 config/cloudinit/kubeconfig） | 中 | 8h |
| P2-9 | Spot 实例支持 | 低 | 3h |
| P2-10 | klog 初始化 + 统一日志风格 | 低 | 2h |

### P3 — 低优先级（远期）

| 编号 | 功能 | 复杂度 | 估时 |
|------|------|--------|------|
| P3-1 | 云盘/OSS/NAS CSI 自动安装 | 高 | 20h |
| P3-2 | ALB Ingress Controller | 中 | 8h |
| P3-3 | Terway CNI 支持 | 高 | 24h |
| P3-4 | etcd 备份/恢复到 OSS | 中 | 12h |
| P3-5 | cluster-autoscaler 集成 | 高 | 20h |
| P3-6 | 现有 VPC 复用 | 中 | 8h |
| P3-7 | CI 集成（GitHub Actions / GitLab CI） | 中 | 8h |
| P3-8 | 多架构镜像 (ARM64) | 中 | 8h |
| P3-9 | 证书轮换 | 中 | 6h |
| P3-10 | 集群重命名 | 低 | 3h |

## 七、实施状态

### ✅ 阶段 1：修 bug + 补齐基础（已完成）
| 编号 | 功能 | 状态 |
|------|------|------|
| P0-1 | klog.Fatalf → return error | ✅ |
| P0-2 | 版本常量集中管理 | ✅ versions.go |
| P0-3 | K8s 版本从常量读取 | ✅ |
| P0-4 | delete 不依赖 config | ✅ --name 标志 |
| P0-5 | 创建失败资源回滚 | ✅ createdResources + defer cleanup |
| P0-6 | show 命令 | ✅ show cluster |

### ✅ 阶段 2：体验打磨（已完成）
| 编号 | 功能 | 状态 |
|------|------|------|
| P1-2 | scale（节点添加/删除） | ✅ |
| P1-3 | --wait 标志 | ✅ |
| P1-5 | 删除时清理 kubeconfig | ✅ |
| P1-7 | 清理死代码 | ✅ |

### ✅ P2 轻量项（已完成）
| 编号 | 功能 | 状态 |
|------|------|------|
| P2-6 | Shell 自动补全 | ✅ |
| P2-10 | klog 初始化 | ✅ |

### ⏸️ 延后（等真实测试）
| 编号 | 功能 | 原因 |
|------|------|------|
| P2-1 | NLB 接入 | 需真实测试才能验证 API 行为 |
| P2-2 | 多 CP 高可用 | 依赖 NLB |
| P2-9 | Spot 实例 | 需真实测试验证计费模式 |

### 阶段 3：高级能力（下月，~96h）
（原 P2 重型项，延后评估）

### 阶段 4：远期（按需）
（原 P3 全部，不做规划）

## 八、调研来源

- [kind 官方文档](https://kind.sigs.k8s.io/docs/design/principles/)
- [kind Features & Capabilities 2024-2025](https://prepare.sh/articles/kind-the-definitive-guide-to-local-kubernetes-for-developers-and-cicd)
- [Cluster API v1.12 Release Notes](https://kubernetes.io/blog/2026/01/27/cluster-api-v1-12-release/)
- [Talos Linux vs K3s (Sidero Labs)](https://www.siderolabs.com/blog/talos-linux-vs-k3s)
- [kingc (GCP) 源码](https://github.com/aojea/kingc)
- [阿里云 ACK 文档](https://www.alibabacloud.com/help/en/ack/)
