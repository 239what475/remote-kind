#!/bin/bash
set -euo pipefail

yum install -y containerd
mkdir -p /etc/containerd
containerd config default > /etc/containerd/config.toml
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
sed -i 's|config_path = ""|config_path = "/etc/containerd/certs.d"|' /etc/containerd/config.toml

{{- range $registry, $mirror := .Mirrors }}
mkdir -p /etc/containerd/certs.d/{{ $registry }}
cat > /etc/containerd/certs.d/{{ $registry }}/hosts.toml << 'HOSTS_EOF'
server = "https://{{ $registry }}"
[host."https://{{ $mirror }}"]
  capabilities = ["pull", "resolve"]
HOSTS_EOF

{{- end }}
systemctl restart containerd
sleep 2

cat > /etc/yum.repos.d/kubernetes.repo << 'REPO'
[kubernetes]
name=Kubernetes
baseurl=https://pkgs.k8s.io/core:/stable:/{{ .K8sMinor }}/rpm/
enabled=1
gpgcheck=0
REPO

yum install -y kubelet kubeadm kubectl crictl containernetworking-plugins
systemctl enable containerd kubelet

# ACK4 installs CNI plugins to /usr/libexec/cni, but kubelet expects /opt/cni/bin
ln -sf /usr/libexec/cni/* /opt/cni/bin/

kubeadm config images pull --image-repository=registry.k8s.io --kubernetes-version={{ .K8sVersion }}
crictl pull {{ .FlannelImage }}
crictl pull {{ .FlannelCNIImage }}

echo 'net.bridge.bridge-nf-call-iptables = 1' > /etc/sysctl.d/k8s.conf
echo 'net.bridge.bridge-nf-call-ip6tables = 1' >> /etc/sysctl.d/k8s.conf
echo 'net.ipv4.ip_forward = 1' >> /etc/sysctl.d/k8s.conf
sysctl --system

sync
systemctl stop containerd
