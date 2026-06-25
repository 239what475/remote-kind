#!/bin/bash
set -euo pipefail

echo "=== kubeadm join ==="
{{ .JoinCommand }} 2>&1 | tee /tmp/kubeadm-join.log
echo "join exit code: $?"
