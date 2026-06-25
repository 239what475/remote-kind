#!/bin/bash
set -euo pipefail
cat > /tmp/kubeadm-config.yaml << 'CONF'
{{ .KubeadmConfig }}
CONF
kubeadm init --config=/tmp/kubeadm-config.yaml --ignore-preflight-errors=all 2>&1 | tee /tmp/kubeadm-init.log
mkdir -p $HOME/.kube
cp /etc/kubernetes/admin.conf $HOME/.kube/config
