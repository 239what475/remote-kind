package cluster

// Build-time variables set via ldflags:
//
//	go build -ldflags "-X github.com/239what475/remote-kind/pkg/cluster.Version=v0.1.0
//	                   -X github.com/239what475/remote-kind/pkg/cluster.GitCommit=$(git rev-parse --short HEAD)
//	                   -X github.com/239what475/remote-kind/pkg/cluster.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
	Release   = "false" // "true" suppresses debug headers (set via ldflags)
)

// Centralized version/image constants. To upgrade the cluster, change these
// values, rebuild the image, and create a new cluster.
const (
	K8sVersion        = "v1.36.2"
	ImageName         = "remote-kind-v1.36"
	KubeadmAPIVersion = "kubeadm.k8s.io/v1beta4"

	// Flannel (ghcr.io/flannel-io/*)
	FlannelImage    = "ghcr.io/flannel-io/flannel:v0.28.5"
	FlannelCNIImage = "ghcr.io/flannel-io/flannel-cni-plugin:v1.9.1-flannel1"
)
