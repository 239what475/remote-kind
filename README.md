# remote-kind

Kubernetes clusters on Alibaba Cloud ECS — like kind, but with real VMs.

## Install

```bash
go install github.com/what/remote-kind/cmd/remote-kind@latest
```

## Quick Start

```bash
# Generate config
remote-kind init

# Edit mirrors and SSH key
vim remote-kind.yaml

# Build custom image (once)
remote-kind build-image

# Create cluster
remote-kind create cluster

# Use it
kubectl --context kind-demo get nodes
ssh root@<CP_IP>

# Clean up
remote-kind delete cluster
```

## Commands

```
Cluster Management:
  create      Create a Kubernetes cluster
  delete      Delete a cluster and all its cloud resources
  scale       Scale worker nodes up or down
  show        Show cluster details

Operations:
  build-image Build a custom ECS image with pre-installed Kubernetes
  get         Get clusters, nodes, or kubeconfig
  init        Generate a default remote-kind.yaml
  ssh         SSH into a cluster node
  completion  Generate shell completion script
  version     Print version
```

## Configuration

See `remote-kind init` for a template. Key fields:

| Field | Description |
|-------|-------------|
| `spec.region` | Alibaba Cloud region |
| `spec.zone` | Availability zone |
| `spec.sshKey` | Path to SSH public key for node access |
| `spec.controlPlane.instanceType` | ECS instance type for control plane |
| `spec.workers[].replicas` | Number of worker nodes |
| `spec.cni` | CNI plugin (`flannel` or `none`) |
| `build.mirrors` | Registry mirrors for image building |

## Requirements

- Alibaba Cloud account with AK/SK configured in `~/.aliyun/config.json`
- `kubectl` in PATH (for kubeconfig merge and CNI install)
